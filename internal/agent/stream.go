package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pavelpiliak/devrecall/internal/llm"
)

// AgentEventType identifies the kind of event RunStream emits.
type AgentEventType string

const (
	// AgentEventThinking marks the start of an LLM round-trip.
	AgentEventThinking AgentEventType = "thinking"
	// AgentEventToken is one chunk of assistant text streamed from the model.
	AgentEventToken AgentEventType = "token"
	// AgentEventToolCall is emitted right before a tool executes.
	AgentEventToolCall AgentEventType = "tool_call"
	// AgentEventToolResult is emitted after the executor returns (success or error).
	AgentEventToolResult AgentEventType = "tool_result"
	// AgentEventDone is the terminal success event; the channel is closed
	// after this is sent.
	AgentEventDone AgentEventType = "done"
	// AgentEventError is the terminal failure event; the channel is closed
	// after this is sent.
	AgentEventError AgentEventType = "error"
)

// AgentEvent is one entry in a streaming agent run.
//
// Only the fields relevant to Type are populated. The receiver should
// switch on Type before reading any other field.
type AgentEvent struct {
	Type       AgentEventType  `json:"type"`
	Step       int             `json:"step,omitempty"`
	Token      string          `json:"token,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolArgs   json.RawMessage `json:"tool_args,omitempty"`
	ToolResult json.RawMessage `json:"tool_result,omitempty"`
	ToolError  string          `json:"tool_error,omitempty"`
	DurationMs int64           `json:"duration_ms,omitempty"`
	// Content is set on the Done event with the final assistant text.
	Content string `json:"content,omitempty"`
	// Err carries the error on Error events. Marshalled as a string for SSE.
	Err string `json:"error,omitempty"`
}

// RunStream executes the agent loop and emits an AgentEvent stream.
//
// The returned channel is closed after a terminal Done or Error event.
// If the underlying provider does not support streaming (SupportsStreaming
// is false), RunStream falls back to the buffered ChatWithTools path and
// emits a single token event per assistant turn.
//
// The caller MUST drain the channel until it is closed; abandoning it
// before completion will block the loop on its next send.
func (l *Loop) RunStream(ctx context.Context, messages []llm.Message) <-chan AgentEvent {
	out := make(chan AgentEvent, 16)
	go l.runStream(ctx, messages, out)
	return out
}

func (l *Loop) runStream(ctx context.Context, messages []llm.Message, out chan<- AgentEvent) {
	defer close(out)

	llmTools := l.registry.LLMTools()
	convo := append([]llm.Message(nil), messages...)

	emit := func(ev AgentEvent) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for step := 1; step <= l.opts.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			emit(AgentEvent{Type: AgentEventError, Err: err.Error()})
			return
		}

		emit(AgentEvent{Type: AgentEventThinking, Step: step})

		resp, err := l.streamOneTurn(ctx, convo, llmTools, step, out)
		if err != nil {
			emit(AgentEvent{Type: AgentEventError, Err: fmt.Sprintf("agent step %d: %v", step, err)})
			return
		}

		// No tool calls → final answer.
		if len(resp.ToolCalls) == 0 {
			emit(AgentEvent{Type: AgentEventDone, Step: step, Content: resp.Content})
			return
		}

		// Record the assistant turn with its tool calls.
		convo = append(convo, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call sequentially, emitting events.
		for _, call := range resp.ToolCalls {
			emit(AgentEvent{
				Type:     AgentEventToolCall,
				Step:     step,
				ToolName: call.Name,
				ToolArgs: call.Arguments,
			})

			ts := l.executeOne(ctx, step, call)
			ev := AgentEvent{
				Type:       AgentEventToolResult,
				Step:       step,
				ToolName:   call.Name,
				ToolArgs:   call.Arguments,
				ToolResult: ts.Result,
				ToolError:  ts.Error,
				DurationMs: ts.DurationMs,
			}
			emit(ev)

			convo = append(convo, l.toolResultMessage(call, ts))
		}
	}

	emit(AgentEvent{
		Type: AgentEventError,
		Err:  fmt.Sprintf("%v (limit=%d)", ErrMaxStepsExceeded, l.opts.MaxSteps),
	})
}

// streamOneTurn pulls one provider turn — streaming when supported,
// buffered when not — and forwards token events to out as they arrive.
// It returns the fully assembled ChatResponse for the agent loop to act on.
func (l *Loop) streamOneTurn(
	ctx context.Context,
	convo []llm.Message,
	llmTools []llm.Tool,
	_ int,
	out chan<- AgentEvent,
) (llm.ChatResponse, error) {
	if !l.provider.SupportsStreaming() {
		resp, err := l.provider.ChatWithTools(ctx, convo, llmTools, l.opts.ChatOpts)
		if err != nil {
			return llm.ChatResponse{}, err
		}
		// Synthesise a single token event so callers see assistant text
		// even on non-streaming providers.
		if resp.Content != "" {
			select {
			case out <- AgentEvent{Type: AgentEventToken, Token: resp.Content}:
			case <-ctx.Done():
				return llm.ChatResponse{}, ctx.Err()
			}
		}
		return resp, nil
	}

	stream, err := l.provider.ChatWithToolsStream(ctx, convo, llmTools, l.opts.ChatOpts)
	if err != nil {
		return llm.ChatResponse{}, err
	}

	var (
		text     strings.Builder
		// toolCalls preserves the order in which tool calls were announced
		// so the agent loop executes them in the model's intended sequence.
		toolCalls   []*llm.ToolCall
		toolByID    = map[string]*llm.ToolCall{}
		argsBuf     = map[string]*strings.Builder{}
		streamErr   error
	)

	for ev := range stream {
		switch ev.Type {
		case llm.StreamEventToken:
			text.WriteString(ev.Token)
			select {
			case out <- AgentEvent{Type: AgentEventToken, Token: ev.Token}:
			case <-ctx.Done():
				return llm.ChatResponse{}, ctx.Err()
			}

		case llm.StreamEventToolCallStart:
			if ev.ToolCall == nil {
				continue
			}
			tc := &llm.ToolCall{
				ID:        ev.ToolCall.ID,
				Name:      ev.ToolCall.Name,
				Arguments: append(json.RawMessage(nil), ev.ToolCall.Arguments...),
			}
			toolCalls = append(toolCalls, tc)
			toolByID[tc.ID] = tc
			argsBuf[tc.ID] = &strings.Builder{}
			argsBuf[tc.ID].Write(tc.Arguments)

		case llm.StreamEventToolCallDelta:
			if ev.ToolCall == nil {
				continue
			}
			buf, ok := argsBuf[ev.ToolCall.ID]
			if !ok {
				// Some providers omit the start event and only stream deltas;
				// treat this as an implicit start.
				tc := &llm.ToolCall{
					ID:   ev.ToolCall.ID,
					Name: ev.ToolCall.Name,
				}
				toolCalls = append(toolCalls, tc)
				toolByID[tc.ID] = tc
				buf = &strings.Builder{}
				argsBuf[tc.ID] = buf
			}
			buf.Write(ev.ToolCall.Arguments)
			if tc := toolByID[ev.ToolCall.ID]; tc != nil && tc.Name == "" {
				tc.Name = ev.ToolCall.Name
			}

		case llm.StreamEventToolCallEnd:
			if ev.ToolCall == nil {
				continue
			}
			if tc, ok := toolByID[ev.ToolCall.ID]; ok {
				if ev.ToolCall.Name != "" {
					tc.Name = ev.ToolCall.Name
				}
				if len(ev.ToolCall.Arguments) > 0 {
					tc.Arguments = append(json.RawMessage(nil), ev.ToolCall.Arguments...)
				}
			}

		case llm.StreamEventDone:
			// nothing to do; loop will exit when channel closes
		case llm.StreamEventError:
			if ev.Err != nil {
				streamErr = ev.Err
			} else {
				streamErr = errors.New("stream error")
			}
		}
	}

	if streamErr != nil {
		return llm.ChatResponse{}, streamErr
	}

	// Finalise tool-call arguments from accumulated deltas.
	finalCalls := make([]llm.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if buf, ok := argsBuf[tc.ID]; ok && buf.Len() > 0 {
			tc.Arguments = json.RawMessage(buf.String())
		}
		if len(tc.Arguments) == 0 {
			tc.Arguments = json.RawMessage(`{}`)
		}
		finalCalls = append(finalCalls, *tc)
	}

	return llm.ChatResponse{
		Content:   text.String(),
		ToolCalls: finalCalls,
	}, nil
}
