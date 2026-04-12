package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/pavelpiliak/devrecall/internal/collector/ratelimit"
)

const defaultOllamaURL = "http://localhost:11434"

// Ollama talks to a local Ollama instance via its HTTP API.
type Ollama struct {
	baseURL string
	model   string
	client  *http.Client

	// toolCapMu guards the cached tool-capability lookups per model name.
	toolCapMu  sync.Mutex
	toolCapMap map[string]bool
}

// NewOllama creates a provider for a local Ollama instance.
// If baseURL is empty, defaults to http://localhost:11434.
// If model is empty, defaults to "gemma4".
func NewOllama(baseURL, model string) *Ollama {
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}
	if model == "" {
		model = "gemma4"
	}
	return &Ollama{
		baseURL:    baseURL,
		model:      model,
		client:     &http.Client{Timeout: 5 * time.Minute},
		toolCapMap: map[string]bool{},
	}
}

func (o *Ollama) Name() string { return "ollama" }

func (o *Ollama) Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error) {
	model := opts.Model
	if model == "" {
		model = o.model
	}

	type ollamaMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []ollamaMsg
	for _, m := range messages {
		msgs = append(msgs, ollamaMsg{Role: m.Role, Content: m.Content})
	}

	body := struct {
		Model    string      `json:"model"`
		Messages []ollamaMsg `json:"messages"`
		Stream   bool        `json:"stream"`
		Options  *struct {
			Temperature float64 `json:"temperature,omitempty"`
			NumPredict  int     `json:"num_predict,omitempty"`
		} `json:"options,omitempty"`
	}{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	if opts.Temperature > 0 || opts.MaxTokens > 0 {
		body.Options = &struct {
			Temperature float64 `json:"temperature,omitempty"`
			NumPredict  int     `json:"num_predict,omitempty"`
		}{
			Temperature: opts.Temperature,
			NumPredict:  opts.MaxTokens,
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	resp, err := ratelimit.Do(ctx, o.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("ollama request failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding ollama response: %w", err)
	}

	return result.Message.Content, nil
}

// SupportsStreaming reports whether streaming tool calls are supported.
// Ollama supports streaming on tool-capable models.
func (o *Ollama) SupportsStreaming() bool { return true }

// SupportsTools probes the configured Ollama model's capabilities via
// /api/show and reports whether it supports tool calling. Results are
// cached per model name. On any error (Ollama unreachable, model not
// found) it returns false so callers fall back to a clear error.
func (o *Ollama) SupportsTools(ctx context.Context) bool {
	return o.modelSupportsTools(ctx, o.model)
}

func (o *Ollama) modelSupportsTools(ctx context.Context, model string) bool {
	if model == "" {
		model = o.model
	}
	o.toolCapMu.Lock()
	if v, ok := o.toolCapMap[model]; ok {
		o.toolCapMu.Unlock()
		return v
	}
	o.toolCapMu.Unlock()

	body, _ := json.Marshal(map[string]any{"model": model})
	resp, err := ratelimit.Do(ctx, o.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/show", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	var info struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false
	}
	supports := false
	for _, c := range info.Capabilities {
		if c == "tools" {
			supports = true
			break
		}
	}
	o.toolCapMu.Lock()
	o.toolCapMap[model] = supports
	o.toolCapMu.Unlock()
	return supports
}

type ollamaToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type ollamaToolCallFn struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFn `json:"function"`
}

type ollamaAPIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

func ollamaBuildMessages(messages []Message) []ollamaAPIMessage {
	out := make([]ollamaAPIMessage, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "tool":
			out = append(out, ollamaAPIMessage{
				Role:       "tool",
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			})
		case "assistant":
			msg := ollamaAPIMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				msg.ToolCalls = append(msg.ToolCalls, ollamaToolCall{
					Function: ollamaToolCallFn{Name: tc.Name, Arguments: args},
				})
			}
			out = append(out, msg)
		default:
			out = append(out, ollamaAPIMessage{Role: m.Role, Content: m.Content})
		}
	}
	return out
}

func ollamaBuildTools(tools []Tool) []ollamaToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ollamaToolDef, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		def := ollamaToolDef{Type: "function"}
		def.Function.Name = t.Name
		def.Function.Description = t.Description
		def.Function.Parameters = schema
		out = append(out, def)
	}
	return out
}

func (o *Ollama) buildToolRequestBody(messages []Message, tools []Tool, opts ChatOpts, stream bool) ([]byte, string, error) {
	model := opts.Model
	if model == "" {
		model = o.model
	}
	type optionsBlock struct {
		Temperature float64 `json:"temperature,omitempty"`
		NumPredict  int     `json:"num_predict,omitempty"`
	}
	body := map[string]any{
		"model":    model,
		"messages": ollamaBuildMessages(messages),
		"stream":   stream,
	}
	if t := ollamaBuildTools(tools); len(t) > 0 {
		body["tools"] = t
	}
	if opts.Temperature > 0 || opts.MaxTokens > 0 {
		body["options"] = optionsBlock{
			Temperature: opts.Temperature,
			NumPredict:  opts.MaxTokens,
		}
	}
	jsonBody, err := json.Marshal(body)
	return jsonBody, model, err
}

// ChatWithTools sends a tool-calling chat request to Ollama and returns
// the buffered result. Returns an error if the model lacks tool capability.
func (o *Ollama) ChatWithTools(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (ChatResponse, error) {
	jsonBody, model, err := o.buildToolRequestBody(messages, tools, opts, false)
	if err != nil {
		return ChatResponse{}, err
	}
	if len(tools) > 0 && !o.modelSupportsTools(ctx, model) {
		return ChatResponse{}, fmt.Errorf("ollama model %q does not support tool calling — switch to a tool-capable model (qwen2.5, llama3.1+, mistral)", model)
	}

	resp, err := ratelimit.Do(ctx, o.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ollama request failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []ollamaToolCall `json:"tool_calls"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ChatResponse{}, fmt.Errorf("decoding ollama response: %w", err)
	}

	out := ChatResponse{Content: result.Message.Content}
	for i, tc := range result.Message.ToolCalls {
		args := tc.Function.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        fmt.Sprintf("call_%d", i),
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return out, nil
}

// ChatWithToolsStream sends a streaming tool-calling chat request to Ollama
// and returns a channel of StreamEvents. The channel is closed at end of
// stream.
func (o *Ollama) ChatWithToolsStream(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (<-chan StreamEvent, error) {
	jsonBody, model, err := o.buildToolRequestBody(messages, tools, opts, true)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 && !o.modelSupportsTools(ctx, model) {
		return nil, fmt.Errorf("ollama model %q does not support tool calling — switch to a tool-capable model (qwen2.5, llama3.1+, mistral)", model)
	}

	resp, err := ratelimit.Do(ctx, o.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ollama stream request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama stream returned %d: %s", resp.StatusCode, respBody)
	}

	ch := make(chan StreamEvent, 16)
	go o.parseOllamaStream(resp.Body, ch)
	return ch, nil
}

func (o *Ollama) parseOllamaStream(body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	callIdx := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk struct {
			Message struct {
				Content   string           `json:"content"`
				ToolCalls []ollamaToolCall `json:"tool_calls"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}
		if chunk.Message.Content != "" {
			ch <- StreamEvent{Type: StreamEventToken, Token: chunk.Message.Content}
		}
		// Ollama emits whole tool calls atomically (no streaming partials).
		for _, tc := range chunk.Message.ToolCalls {
			args := tc.Function.Arguments
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			id := fmt.Sprintf("call_%d", callIdx)
			callIdx++
			ch <- StreamEvent{
				Type: StreamEventToolCallStart,
				ToolCall: &ToolCall{
					ID:        id,
					Name:      tc.Function.Name,
					Arguments: json.RawMessage(`{}`),
				},
			}
			ch <- StreamEvent{
				Type: StreamEventToolCallEnd,
				ToolCall: &ToolCall{
					ID:        id,
					Name:      tc.Function.Name,
					Arguments: args,
				},
			}
		}
		if chunk.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: StreamEventError, Err: fmt.Errorf("ollama stream read: %w", err)}
		return
	}
	ch <- StreamEvent{Type: StreamEventDone}
}
