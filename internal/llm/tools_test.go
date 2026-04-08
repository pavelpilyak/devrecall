package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// helper: drain a stream channel into a slice with a sanity cap.
func drainStream(t *testing.T, ch <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
		if len(events) > 1000 {
			t.Fatal("stream produced too many events")
		}
	}
	return events
}

func sampleTool() Tool {
	return Tool{
		Name:        "list_activities",
		Description: "List activities in a date range",
		Schema: json.RawMessage(`{
			"type":"object",
			"properties":{"start":{"type":"string"},"end":{"type":"string"}},
			"required":["start","end"]
		}`),
	}
}

// ─── Anthropic ────────────────────────────────────────────────────────────────

func TestAnthropic_ChatWithTools_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string             `json:"model"`
			System   string             `json:"system"`
			Messages []anthropicMessage `json:"messages"`
			Tools    []anthropicTool    `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.System != "be helpful" {
			t.Errorf("system = %q", body.System)
		}
		if len(body.Tools) != 1 || body.Tools[0].Name != "list_activities" {
			t.Errorf("tools = %+v", body.Tools)
		}
		// Expect: user, assistant(text+tool_use), user(tool_result)
		if len(body.Messages) != 3 {
			t.Fatalf("got %d messages", len(body.Messages))
		}
		if body.Messages[1].Role != "assistant" {
			t.Errorf("msg[1].role = %q", body.Messages[1].Role)
		}
		if len(body.Messages[1].Content) != 2 || body.Messages[1].Content[1].Type != "tool_use" {
			t.Errorf("assistant content = %+v", body.Messages[1].Content)
		}
		if body.Messages[2].Content[0].Type != "tool_result" || body.Messages[2].Content[0].ToolUseID != "toolu_1" {
			t.Errorf("tool_result = %+v", body.Messages[2].Content[0])
		}

		// Respond with text + a tool_use block.
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Let me check."},
				{"type": "tool_use", "id": "toolu_2", "name": "list_activities", "input": map[string]string{"start": "2026-04-07", "end": "2026-04-08"}},
			},
			"stop_reason": "tool_use",
		})
	}))
	defer srv.Close()

	p := NewAnthropic("sk-ant", "claude-test", srv.URL)
	if !p.SupportsTools(context.Background()) {
		t.Error("SupportsTools = false")
	}
	if !p.SupportsStreaming() {
		t.Error("SupportsStreaming = false")
	}

	resp, err := p.ChatWithTools(context.Background(), []Message{
		{Role: "system", Content: "be helpful"},
		{Role: "user", Content: "what did I do today?"},
		{Role: "assistant", Content: "Checking…", ToolCalls: []ToolCall{
			{ID: "toolu_1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: "tool", ToolCallID: "toolu_1", Content: `{"now":"2026-04-08T10:00:00Z"}`},
	}, []Tool{sampleTool()}, ChatOpts{})
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}
	if resp.Content != "Let me check." {
		t.Errorf("content = %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_activities" || resp.ToolCalls[0].ID != "toolu_2" {
		t.Errorf("tool calls = %+v", resp.ToolCalls)
	}
	var args map[string]string
	if err := json.Unmarshal(resp.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("args unmarshal: %v", err)
	}
	if args["start"] != "2026-04-07" {
		t.Errorf("start = %q", args["start"])
	}
}

func TestAnthropic_ChatWithToolsStream_Events(t *testing.T) {
	// Anthropic SSE: simulate text + tool_use block streaming.
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start"}`,
		``,
		`event: content_block_start`,
		`data: {"index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":0,"delta":{"type":"text_delta","text":"Let "}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":0,"delta":{"type":"text_delta","text":"me check."}}`,
		``,
		`event: content_block_stop`,
		`data: {"index":0}`,
		``,
		`event: content_block_start`,
		`data: {"index":1,"content_block":{"type":"tool_use","id":"toolu_x","name":"list_activities"}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"start\":"}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"\"2026-04-07\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"index":1}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := NewAnthropic("sk-ant", "claude-test", srv.URL)
	ch, err := p.ChatWithToolsStream(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	}, []Tool{sampleTool()}, ChatOpts{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	events := drainStream(t, ch)

	var tokens []string
	var sawStart, sawEnd, sawDone bool
	for _, ev := range events {
		switch ev.Type {
		case StreamEventToken:
			tokens = append(tokens, ev.Token)
		case StreamEventToolCallStart:
			sawStart = true
			if ev.ToolCall == nil || ev.ToolCall.Name != "list_activities" {
				t.Errorf("start ToolCall = %+v", ev.ToolCall)
			}
		case StreamEventToolCallEnd:
			sawEnd = true
			if ev.ToolCall == nil {
				t.Fatal("end ToolCall = nil")
			}
			var args map[string]string
			if err := json.Unmarshal(ev.ToolCall.Arguments, &args); err != nil {
				t.Errorf("args unmarshal: %v (raw=%s)", err, string(ev.ToolCall.Arguments))
			}
			if args["start"] != "2026-04-07" {
				t.Errorf("start = %q", args["start"])
			}
		case StreamEventDone:
			sawDone = true
		}
	}
	if got := strings.Join(tokens, ""); got != "Let me check." {
		t.Errorf("tokens joined = %q", got)
	}
	if !sawStart || !sawEnd || !sawDone {
		t.Errorf("missing events: start=%v end=%v done=%v", sawStart, sawEnd, sawDone)
	}
}

// ─── OpenAI ───────────────────────────────────────────────────────────────────

func TestOpenAI_ChatWithTools_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string             `json:"model"`
			Messages []openaiAPIMessage `json:"messages"`
			Tools    []openaiToolDef    `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Function.Name != "list_activities" {
			t.Errorf("tools = %+v", body.Tools)
		}
		// system, user, assistant(tool_calls), tool
		if len(body.Messages) != 4 {
			t.Fatalf("messages = %d", len(body.Messages))
		}
		if body.Messages[2].Role != "assistant" || len(body.Messages[2].ToolCalls) != 1 {
			t.Errorf("assistant = %+v", body.Messages[2])
		}
		if body.Messages[2].ToolCalls[0].Function.Name != "current_time" {
			t.Errorf("tool name = %q", body.Messages[2].ToolCalls[0].Function.Name)
		}
		if body.Messages[3].Role != "tool" || body.Messages[3].ToolCallID != "call_1" {
			t.Errorf("tool message = %+v", body.Messages[3])
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_2",
								"type": "function",
								"function": map[string]any{
									"name":      "list_activities",
									"arguments": `{"start":"2026-04-07","end":"2026-04-08"}`,
								},
							},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAI("sk-test", "gpt-test", srv.URL)
	if !p.SupportsTools(context.Background()) {
		t.Error("SupportsTools = false")
	}

	resp, err := p.ChatWithTools(context.Background(), []Message{
		{Role: "system", Content: "be helpful"},
		{Role: "user", Content: "today?"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: `{"now":"2026-04-08"}`},
	}, []Tool{sampleTool()}, ChatOpts{})
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_2" || resp.ToolCalls[0].Name != "list_activities" {
		t.Errorf("tool calls = %+v", resp.ToolCalls)
	}
	var args map[string]string
	if err := json.Unmarshal(resp.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("args: %v", err)
	}
	if args["end"] != "2026-04-08" {
		t.Errorf("end = %q", args["end"])
	}
}

func TestOpenAI_ChatWithToolsStream_Events(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"content":"Let "}}]}`,
		`{"choices":[{"delta":{"content":"me check."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"list_activities","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"start\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"2026-04-07\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString("data: ")
		sb.WriteString(c)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sb.String())
	}))
	defer srv.Close()

	p := NewOpenAI("sk-test", "gpt-test", srv.URL)
	ch, err := p.ChatWithToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{sampleTool()}, ChatOpts{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	events := drainStream(t, ch)

	var tokens []string
	var sawStart, sawEnd, sawDone bool
	for _, ev := range events {
		switch ev.Type {
		case StreamEventToken:
			tokens = append(tokens, ev.Token)
		case StreamEventToolCallStart:
			sawStart = true
			if ev.ToolCall == nil || ev.ToolCall.Name != "list_activities" || ev.ToolCall.ID != "call_x" {
				t.Errorf("start ToolCall = %+v", ev.ToolCall)
			}
		case StreamEventToolCallEnd:
			sawEnd = true
			var args map[string]string
			if err := json.Unmarshal(ev.ToolCall.Arguments, &args); err != nil {
				t.Errorf("args: %v (raw=%s)", err, ev.ToolCall.Arguments)
			}
			if args["start"] != "2026-04-07" {
				t.Errorf("start = %q", args["start"])
			}
		case StreamEventDone:
			sawDone = true
		}
	}
	if joined := strings.Join(tokens, ""); joined != "Let me check." {
		t.Errorf("tokens = %q", joined)
	}
	if !sawStart || !sawEnd || !sawDone {
		t.Errorf("missing events: start=%v end=%v done=%v", sawStart, sawEnd, sawDone)
	}
}

// ─── Ollama ───────────────────────────────────────────────────────────────────

func TestOllama_SupportsTools_Cached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("path = %q", r.URL.Path)
		}
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion", "tools"},
		})
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "qwen2.5")
	if !p.SupportsTools(context.Background()) {
		t.Error("SupportsTools = false")
	}
	if !p.SupportsTools(context.Background()) {
		t.Error("second call = false")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (cached)", calls)
	}
}

func TestOllama_SupportsTools_Missing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion"},
		})
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "old-model")
	if p.SupportsTools(context.Background()) {
		t.Error("SupportsTools = true, want false")
	}
}

func TestOllama_ChatWithTools_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"tools"}})
			return
		case "/api/chat":
			var body struct {
				Model    string             `json:"model"`
				Messages []ollamaAPIMessage `json:"messages"`
				Tools    []ollamaToolDef    `json:"tools"`
				Stream   bool               `json:"stream"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Stream {
				t.Error("stream should be false")
			}
			if len(body.Tools) != 1 || body.Tools[0].Function.Name != "list_activities" {
				t.Errorf("tools = %+v", body.Tools)
			}
			if len(body.Messages) != 2 {
				t.Errorf("messages = %d", len(body.Messages))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{
						{
							"function": map[string]any{
								"name":      "list_activities",
								"arguments": map[string]string{"start": "2026-04-07", "end": "2026-04-08"},
							},
						},
					},
				},
				"done": true,
			})
		}
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "qwen2.5")
	resp, err := p.ChatWithTools(context.Background(), []Message{
		{Role: "user", Content: "today?"},
		{Role: "tool", ToolCallID: "call_0", Content: "{}"},
	}, []Tool{sampleTool()}, ChatOpts{})
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_activities" {
		t.Errorf("tool calls = %+v", resp.ToolCalls)
	}
	var args map[string]string
	if err := json.Unmarshal(resp.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("args: %v", err)
	}
	if args["start"] != "2026-04-07" {
		t.Errorf("start = %q", args["start"])
	}
}

func TestOllama_ChatWithTools_UnsupportedModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"completion"}})
			return
		}
		t.Errorf("unexpected request to %s", r.URL.Path)
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "old-model")
	_, err := p.ChatWithTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{sampleTool()}, ChatOpts{})
	if err == nil {
		t.Fatal("expected error for non-tool-capable model")
	}
	if !strings.Contains(err.Error(), "does not support tool calling") {
		t.Errorf("err = %v", err)
	}
}

func TestOllama_ChatWithToolsStream_Events(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"tools"}})
		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			io.WriteString(w, `{"message":{"role":"assistant","content":"Let "},"done":false}`+"\n")
			io.WriteString(w, `{"message":{"role":"assistant","content":"me check."},"done":false}`+"\n")
			io.WriteString(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"list_activities","arguments":{"start":"2026-04-07","end":"2026-04-08"}}}]},"done":true}`+"\n")
		}
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "qwen2.5")
	ch, err := p.ChatWithToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{sampleTool()}, ChatOpts{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	events := drainStream(t, ch)

	var tokens []string
	var sawStart, sawEnd, sawDone bool
	for _, ev := range events {
		switch ev.Type {
		case StreamEventToken:
			tokens = append(tokens, ev.Token)
		case StreamEventToolCallStart:
			sawStart = true
		case StreamEventToolCallEnd:
			sawEnd = true
			var args map[string]string
			if err := json.Unmarshal(ev.ToolCall.Arguments, &args); err != nil {
				t.Errorf("args: %v", err)
			}
			if args["start"] != "2026-04-07" {
				t.Errorf("start = %q", args["start"])
			}
		case StreamEventDone:
			sawDone = true
		}
	}
	if joined := strings.Join(tokens, ""); joined != "Let me check." {
		t.Errorf("tokens = %q", joined)
	}
	if !sawStart || !sawEnd || !sawDone {
		t.Errorf("missing events: start=%v end=%v done=%v", sawStart, sawEnd, sawDone)
	}
}

// ─── Interface guard ──────────────────────────────────────────────────────────

func TestProviders_ImplementToolCallingProvider(t *testing.T) {
	var _ ToolCallingProvider = (*Anthropic)(nil)
	var _ ToolCallingProvider = (*OpenAI)(nil)
	var _ ToolCallingProvider = (*Ollama)(nil)
}
