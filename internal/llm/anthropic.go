package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/pavelpiliak/devrecall/internal/collector/ratelimit"
)

const (
	defaultAnthropicURL = "https://api.anthropic.com"
	anthropicAPIVersion = "2023-06-01"
)

// Anthropic talks to the Anthropic Messages API.
type Anthropic struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewAnthropic creates an Anthropic provider.
func NewAnthropic(apiKey, model, baseURL string) *Anthropic {
	if baseURL == "" {
		baseURL = defaultAnthropicURL
	}
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &Anthropic{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 2 * time.Minute},
	}
}

func (a *Anthropic) Name() string { return "anthropic" }

func (a *Anthropic) Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error) {
	model := opts.Model
	if model == "" {
		model = a.model
	}

	// Anthropic separates system from messages.
	var system string
	type apiMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var apiMsgs []apiMsg
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		apiMsgs = append(apiMsgs, apiMsg{Role: m.Role, Content: m.Content})
	}

	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	reqBody := map[string]any{
		"model":      model,
		"messages":   apiMsgs,
		"max_tokens": maxTokens,
	}
	if system != "" {
		reqBody["system"] = system
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	resp, err := ratelimit.Do(ctx, a.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", anthropicAPIVersion)
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("anthropic: invalid API key — check your key at console.anthropic.com")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding anthropic response: %w", err)
	}

	for _, block := range result.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("anthropic returned no text content")
}

// SupportsTools reports whether this provider supports tool calling.
// Anthropic supports tool use on all current Claude models.
func (a *Anthropic) SupportsTools(_ context.Context) bool { return true }

// SupportsStreaming reports whether streaming tool calls are supported.
func (a *Anthropic) SupportsStreaming() bool { return true }

// anthropicContentBlock is a single content block in an Anthropic message.
// Used both for sending (tool_use, tool_result) and receiving.
type anthropicContentBlock struct {
	Type string `json:"type"`
	// text block
	Text string `json:"text,omitempty"`
	// tool_use block (server -> us, and replayed back)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result block (us -> server)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicBuildMessages converts our generic Message slice into Anthropic's
// content-block message format and extracts the system prompt.
func anthropicBuildMessages(messages []Message) (system string, out []anthropicMessage) {
	var sysParts []string
	for _, m := range messages {
		switch m.Role {
		case "system":
			sysParts = append(sysParts, m.Content)
		case "user":
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: []anthropicContentBlock{{Type: "text", Text: m.Content}},
			})
		case "assistant":
			var blocks []anthropicContentBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
		case "tool":
			out = append(out, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
		}
	}
	return strings.Join(sysParts, "\n"), out
}

func anthropicBuildTools(tools []Tool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out
}

func (a *Anthropic) buildToolRequestBody(messages []Message, tools []Tool, opts ChatOpts, stream bool) ([]byte, error) {
	model := opts.Model
	if model == "" {
		model = a.model
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	system, apiMsgs := anthropicBuildMessages(messages)
	apiTools := anthropicBuildTools(tools)

	reqBody := map[string]any{
		"model":      model,
		"messages":   apiMsgs,
		"max_tokens": maxTokens,
	}
	if system != "" {
		reqBody["system"] = system
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}
	if len(apiTools) > 0 {
		reqBody["tools"] = apiTools
	}
	if stream {
		reqBody["stream"] = true
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		// Surface exactly which tool_use Input is malformed so we can chase
		// the corruption back to its source. The error string itself
		// (e.g. `json: error calling MarshalJSON for type json.RawMessage:
		// invalid character '{' after top-level value`) hides which call
		// caused it; this dump prints every assistant tool_use we tried
		// to send so the bad one stands out.
		log.Printf("anthropic: build request body failed: %v", err)
		for _, m := range messages {
			if m.Role != "assistant" {
				continue
			}
			for _, tc := range m.ToolCalls {
				log.Printf("anthropic:   assistant tool_use id=%q name=%q args=%q",
					tc.ID, tc.Name, string(tc.Arguments))
			}
		}
		return nil, err
	}
	return body, nil
}

// ChatWithTools sends a tool-calling chat request and returns the buffered
// result.
func (a *Anthropic) ChatWithTools(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (ChatResponse, error) {
	jsonBody, err := a.buildToolRequestBody(messages, tools, opts, false)
	if err != nil {
		return ChatResponse{}, err
	}

	resp, err := ratelimit.Do(ctx, a.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", anthropicAPIVersion)
		return req, nil
	})
	if err != nil {
		return ChatResponse{}, fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ChatResponse{}, fmt.Errorf("anthropic: invalid API key — check your key at console.anthropic.com")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Content    []anthropicContentBlock `json:"content"`
		StopReason string                  `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ChatResponse{}, fmt.Errorf("decoding anthropic response: %w", err)
	}

	var out ChatResponse
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			out.Content += block.Text
		case "tool_use":
			args := block.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			})
		}
	}
	return out, nil
}

// ChatWithToolsStream sends a streaming tool-calling chat request and
// returns a channel of StreamEvents. The channel is closed when the stream
// ends (after either a "done" or "error" event).
func (a *Anthropic) ChatWithToolsStream(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (<-chan StreamEvent, error) {
	jsonBody, err := a.buildToolRequestBody(messages, tools, opts, true)
	if err != nil {
		return nil, err
	}

	resp, err := ratelimit.Do(ctx, a.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", anthropicAPIVersion)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic stream request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic stream returned %d: %s", resp.StatusCode, respBody)
	}

	ch := make(chan StreamEvent, 16)
	go a.parseAnthropicStream(resp.Body, ch)
	return ch, nil
}

// anthropicBlockState tracks an in-flight content block while parsing SSE.
type anthropicBlockState struct {
	kind         string // "text" or "tool_use"
	toolID       string
	toolName     string
	toolArgsJSON strings.Builder
}

func (a *Anthropic) parseAnthropicStream(body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	blocks := map[int]*anthropicBlockState{}
	var eventName string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if eventName != "" && len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				a.handleAnthropicEvent(eventName, data, blocks, ch)
			}
			eventName = ""
			dataLines = nil
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: StreamEventError, Err: fmt.Errorf("anthropic stream read: %w", err)}
		return
	}
	ch <- StreamEvent{Type: StreamEventDone}
}

func (a *Anthropic) handleAnthropicEvent(name, data string, blocks map[int]*anthropicBlockState, ch chan<- StreamEvent) {
	switch name {
	case "content_block_start":
		var ev struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type  string `json:"type"`
				ID    string `json:"id"`
				Name  string `json:"name"`
				Text  string `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return
		}
		state := &anthropicBlockState{kind: ev.ContentBlock.Type}
		if ev.ContentBlock.Type == "tool_use" {
			state.toolID = ev.ContentBlock.ID
			state.toolName = ev.ContentBlock.Name
			ch <- StreamEvent{
				Type: StreamEventToolCallStart,
				ToolCall: &ToolCall{
					ID:        state.toolID,
					Name:      state.toolName,
					Arguments: json.RawMessage(`{}`),
				},
			}
		} else if ev.ContentBlock.Type == "text" && ev.ContentBlock.Text != "" {
			ch <- StreamEvent{Type: StreamEventToken, Token: ev.ContentBlock.Text}
		}
		blocks[ev.Index] = state
	case "content_block_delta":
		var ev struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return
		}
		state := blocks[ev.Index]
		if state == nil {
			return
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				ch <- StreamEvent{Type: StreamEventToken, Token: ev.Delta.Text}
			}
		case "input_json_delta":
			state.toolArgsJSON.WriteString(ev.Delta.PartialJSON)
			ch <- StreamEvent{
				Type: StreamEventToolCallDelta,
				ToolCall: &ToolCall{
					ID:        state.toolID,
					Name:      state.toolName,
					Arguments: json.RawMessage(ev.Delta.PartialJSON),
				},
			}
		}
	case "content_block_stop":
		var ev struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return
		}
		state := blocks[ev.Index]
		if state == nil {
			return
		}
		if state.kind == "tool_use" {
			args := state.toolArgsJSON.String()
			if args == "" {
				args = "{}"
			}
			ch <- StreamEvent{
				Type: StreamEventToolCallEnd,
				ToolCall: &ToolCall{
					ID:        state.toolID,
					Name:      state.toolName,
					Arguments: json.RawMessage(args),
				},
			}
		}
		delete(blocks, ev.Index)
	case "error":
		ch <- StreamEvent{Type: StreamEventError, Err: fmt.Errorf("anthropic stream: %s", data)}
	}
}
