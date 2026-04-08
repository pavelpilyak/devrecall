// Package agent runs a tool-calling LLM in a bounded loop.
//
// The loop sends the conversation + tool catalogue to a
// llm.ToolCallingProvider, executes any tool calls the model emits via the
// internal/agent/tools registry, feeds the results back as tool messages,
// and repeats until the model produces a final text answer or the step cap
// is hit.
//
// All tool calls are read-only (the registry enforces this) and bounded
// by a per-tool timeout. The loop records every step in a trace so the
// caller can render or expose it (the chat REPL exposes it via /trace).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pavelpiliak/devrecall/internal/agent/tools"
	"github.com/pavelpiliak/devrecall/internal/llm"
)

// Defaults applied when LoopOptions fields are zero.
const (
	DefaultMaxSteps    = 8
	DefaultToolTimeout = 5 * time.Second
)

// LoopOptions controls the agent loop's behaviour.
type LoopOptions struct {
	// MaxSteps caps the number of LLM round-trips per Run. Hitting the cap
	// returns ErrMaxStepsExceeded plus the partial trace.
	MaxSteps int
	// ToolTimeout bounds each individual tool execution.
	ToolTimeout time.Duration
	// ChatOpts is forwarded to the provider on every call.
	ChatOpts llm.ChatOpts
}

// ErrMaxStepsExceeded is returned (wrapped) when the loop exhausts MaxSteps
// without the model producing a final text answer.
var ErrMaxStepsExceeded = errors.New("agent: max_steps exceeded")

// TraceStep is one entry in an agent run's trace: the tool that was called,
// the arguments the model produced, the result (or error), and how long
// the executor took.
type TraceStep struct {
	Step       int             `json:"step"`
	ToolName   string          `json:"tool_name"`
	ToolArgs   json.RawMessage `json:"tool_args,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	DurationMs int64           `json:"duration_ms"`
}

// Result is the buffered output of an agent Run.
type Result struct {
	Content string      // final assistant text answer
	Steps   int         // number of LLM round-trips actually used
	Trace   []TraceStep // tool-call history (in chronological order)
}

// Loop drives a tool-calling LLM through a bounded read-loop.
//
// Construct one with NewLoop and call Run for each user turn.
type Loop struct {
	provider llm.ToolCallingProvider
	registry *tools.Registry
	opts     LoopOptions
}

// NewLoop constructs an agent loop. Zero-valued option fields fall back to
// the package defaults (DefaultMaxSteps, DefaultToolTimeout).
func NewLoop(provider llm.ToolCallingProvider, registry *tools.Registry, opts LoopOptions) *Loop {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = DefaultMaxSteps
	}
	if opts.ToolTimeout <= 0 {
		opts.ToolTimeout = DefaultToolTimeout
	}
	return &Loop{
		provider: provider,
		registry: registry,
		opts:     opts,
	}
}

// Run executes the agent loop for one turn.
//
// The caller passes the full message history (system + prior turns + the
// current user message). Run returns the final assistant text plus the
// trace of all tool calls executed during the turn.
//
// Even on error, the partially-built trace is returned in Result.Trace so
// callers can show what the agent attempted before failing.
func (l *Loop) Run(ctx context.Context, messages []llm.Message) (Result, error) {
	llmTools := l.registry.LLMTools()
	// Work on a local copy so we don't mutate the caller's slice.
	convo := append([]llm.Message(nil), messages...)
	var trace []TraceStep

	for step := 1; step <= l.opts.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return Result{Steps: step - 1, Trace: trace}, err
		}

		resp, err := l.provider.ChatWithTools(ctx, convo, llmTools, l.opts.ChatOpts)
		if err != nil {
			return Result{Steps: step - 1, Trace: trace}, fmt.Errorf("agent step %d: %w", step, err)
		}

		sanitizeToolCalls(resp.ToolCalls, fmt.Sprintf("buffered step=%d provider=%s", step, l.provider.Name()))

		// Final answer: model produced text and no tool calls.
		if len(resp.ToolCalls) == 0 {
			return Result{
				Content: resp.Content,
				Steps:   step,
				Trace:   trace,
			}, nil
		}

		// Record the assistant message with its tool calls.
		convo = append(convo, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call sequentially with a timeout, append the
		// result as a tool message, and record a trace entry per call.
		for _, call := range resp.ToolCalls {
			ts := l.executeOne(ctx, step, call)
			trace = append(trace, ts)
			convo = append(convo, l.toolResultMessage(call, ts))
		}
	}

	return Result{Steps: l.opts.MaxSteps, Trace: trace}, fmt.Errorf("%w (limit=%d)", ErrMaxStepsExceeded, l.opts.MaxSteps)
}

// executeOne runs a single tool call with a timeout and returns its trace
// step (whether successful or not).
func (l *Loop) executeOne(ctx context.Context, step int, call llm.ToolCall) TraceStep {
	start := time.Now()
	toolCtx, cancel := context.WithTimeout(ctx, l.opts.ToolTimeout)
	defer cancel()

	result, err := l.registry.Execute(toolCtx, call.Name, call.Arguments)
	ts := TraceStep{
		Step:       step,
		ToolName:   call.Name,
		ToolArgs:   call.Arguments,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		ts.Error = err.Error()
	} else {
		ts.Result = result
	}
	return ts
}

// sanitizeToolCalls validates each ToolCall.Arguments is a single parseable
// JSON object. Anything else (empty, malformed, multiple top-level values
// like the `{}{"k":"v"}` Anthropic-deltas-bug pattern) is replaced with `{}`
// so downstream json.Marshal of the next request body can't blow up with
// "invalid character '{' after top-level value".
//
// When sanitization kicks in we log to stderr with the source context so we
// can see in the dev console exactly which provider/path produced bad args.
func sanitizeToolCalls(calls []llm.ToolCall, src string) {
	for i := range calls {
		raw := calls[i].Arguments
		if isValidJSONObject(raw) {
			continue
		}
		log.Printf("agent: sanitizing malformed tool args [%s] tool=%q id=%q raw=%q",
			src, calls[i].Name, calls[i].ID, string(raw))
		calls[i].Arguments = json.RawMessage(`{}`)
	}
}

// isValidJSONObject reports whether raw is a single, parseable JSON object.
// Empty `{}` is considered valid (the model is allowed to call argless tools).
func isValidJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var v map[string]interface{}
	return json.Unmarshal(raw, &v) == nil
}

// toolResultMessage builds the tool-role message that's fed back to the
// model after executing a tool call. Errors are surfaced as a JSON object
// with an "error" field so the model can react and retry/fall back.
func (l *Loop) toolResultMessage(call llm.ToolCall, ts TraceStep) llm.Message {
	var content string
	if ts.Error != "" {
		errBlob, _ := json.Marshal(map[string]string{"error": ts.Error})
		content = string(errBlob)
	} else if len(ts.Result) > 0 {
		content = string(ts.Result)
	} else {
		content = "{}"
	}
	return llm.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: call.ID,
	}
}
