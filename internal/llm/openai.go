package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultOpenAIURL = "https://api.openai.com/v1"

// OpenAI talks to the OpenAI Chat Completions API (or any compatible endpoint).
type OpenAI struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAI creates an OpenAI provider.
// baseURL can be overridden for compatible providers (Groq, Together, local vLLM).
func NewOpenAI(apiKey, model, baseURL string) *OpenAI {
	if baseURL == "" {
		baseURL = defaultOpenAIURL
	}
	if model == "" {
		model = "gpt-5.4-mini"
	}
	return &OpenAI{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 2 * time.Minute},
	}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error) {
	model := opts.Model
	if model == "" {
		model = o.model
	}

	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []chatMsg
	for _, m := range messages {
		msgs = append(msgs, chatMsg{Role: m.Role, Content: m.Content})
	}

	reqBody := map[string]any{
		"model":    model,
		"messages": msgs,
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}
	if opts.MaxTokens > 0 {
		reqBody["max_completion_tokens"] = opts.MaxTokens
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("openai: invalid API key — check your key at platform.openai.com")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("openai: rate limited — try again shortly")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding openai response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}

	return result.Choices[0].Message.Content, nil
}

// SupportsTools reports whether this provider supports tool calling.
// All OpenAI chat models from gpt-3.5+ and OpenAI-compatible providers
// (Groq, Together, etc.) on tool-capable models do.
func (o *OpenAI) SupportsTools(_ context.Context) bool { return true }

// SupportsStreaming reports whether streaming tool calls are supported.
func (o *OpenAI) SupportsStreaming() bool { return true }

type openaiToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openaiToolCallFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openaiToolCall struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function openaiToolCallFn `json:"function"`
}

type openaiAPIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

func openaiBuildMessages(messages []Message) []openaiAPIMessage {
	out := make([]openaiAPIMessage, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "tool":
			out = append(out, openaiAPIMessage{
				Role:       "tool",
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			})
		case "assistant":
			msg := openaiAPIMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				args := string(tc.Arguments)
				if args == "" {
					args = "{}"
				}
				msg.ToolCalls = append(msg.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiToolCallFn{
						Name:      tc.Name,
						Arguments: args,
					},
				})
			}
			out = append(out, msg)
		default:
			out = append(out, openaiAPIMessage{Role: m.Role, Content: m.Content})
		}
	}
	return out
}

func openaiBuildTools(tools []Tool) []openaiToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openaiToolDef, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		def := openaiToolDef{Type: "function"}
		def.Function.Name = t.Name
		def.Function.Description = t.Description
		def.Function.Parameters = schema
		out = append(out, def)
	}
	return out
}

func (o *OpenAI) buildToolRequestBody(messages []Message, tools []Tool, opts ChatOpts, stream bool) ([]byte, error) {
	model := opts.Model
	if model == "" {
		model = o.model
	}
	reqBody := map[string]any{
		"model":    model,
		"messages": openaiBuildMessages(messages),
	}
	if t := openaiBuildTools(tools); len(t) > 0 {
		reqBody["tools"] = t
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}
	if opts.MaxTokens > 0 {
		reqBody["max_completion_tokens"] = opts.MaxTokens
	}
	if stream {
		reqBody["stream"] = true
	}
	return json.Marshal(reqBody)
}

// ChatWithTools sends a tool-calling chat completion request and returns
// the buffered result.
func (o *OpenAI) ChatWithTools(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (ChatResponse, error) {
	jsonBody, err := o.buildToolRequestBody(messages, tools, opts, false)
	if err != nil {
		return ChatResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return ChatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ChatResponse{}, fmt.Errorf("openai: invalid API key — check your key at platform.openai.com")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return ChatResponse{}, fmt.Errorf("openai: rate limited — try again shortly")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("openai returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content   string           `json:"content"`
				ToolCalls []openaiToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ChatResponse{}, fmt.Errorf("decoding openai response: %w", err)
	}
	if len(result.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("openai returned no choices")
	}

	msg := result.Choices[0].Message
	out := ChatResponse{Content: msg.Content}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(args),
		})
	}
	return out, nil
}

// ChatWithToolsStream sends a streaming tool-calling chat completion and
// returns a channel of StreamEvents. The channel is closed at end of stream.
func (o *OpenAI) ChatWithToolsStream(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (<-chan StreamEvent, error) {
	jsonBody, err := o.buildToolRequestBody(messages, tools, opts, true)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai stream request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("openai stream returned %d: %s", resp.StatusCode, respBody)
	}

	ch := make(chan StreamEvent, 16)
	go o.parseOpenAIStream(resp.Body, ch)
	return ch, nil
}

// openaiToolCallState tracks an in-flight streaming tool call by its index.
type openaiToolCallState struct {
	id       string
	name     string
	argsJSON strings.Builder
	emitted  bool
}

func (o *OpenAI) parseOpenAIStream(body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	calls := map[int]*openaiToolCallState{}
	var emittedOrder []int

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string           `json:"content"`
					ToolCalls []openaiToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			ch <- StreamEvent{Type: StreamEventToken, Token: delta.Content}
		}
		for _, tc := range delta.ToolCalls {
			state, ok := calls[tc.Index]
			if !ok {
				state = &openaiToolCallState{}
				calls[tc.Index] = state
				emittedOrder = append(emittedOrder, tc.Index)
			}
			if tc.ID != "" {
				state.id = tc.ID
			}
			if tc.Function.Name != "" {
				state.name = tc.Function.Name
			}
			if !state.emitted && state.name != "" {
				state.emitted = true
				ch <- StreamEvent{
					Type: StreamEventToolCallStart,
					ToolCall: &ToolCall{
						ID:        state.id,
						Name:      state.name,
						Arguments: json.RawMessage(`{}`),
					},
				}
			}
			if tc.Function.Arguments != "" {
				state.argsJSON.WriteString(tc.Function.Arguments)
				ch <- StreamEvent{
					Type: StreamEventToolCallDelta,
					ToolCall: &ToolCall{
						ID:        state.id,
						Name:      state.name,
						Arguments: json.RawMessage(tc.Function.Arguments),
					},
				}
			}
		}
		if chunk.Choices[0].FinishReason != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: StreamEventError, Err: fmt.Errorf("openai stream read: %w", err)}
		return
	}
	for _, idx := range emittedOrder {
		state := calls[idx]
		if state == nil || !state.emitted {
			continue
		}
		args := state.argsJSON.String()
		if args == "" {
			args = "{}"
		}
		ch <- StreamEvent{
			Type: StreamEventToolCallEnd,
			ToolCall: &ToolCall{
				ID:        state.id,
				Name:      state.name,
				Arguments: json.RawMessage(args),
			},
		}
	}
	ch <- StreamEvent{Type: StreamEventDone}
}
