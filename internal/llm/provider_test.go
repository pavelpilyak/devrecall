package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllama_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}

		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "llama3.2" {
			t.Errorf("model = %q", body.Model)
		}
		if body.Stream {
			t.Error("stream should be false")
		}
		if len(body.Messages) != 2 {
			t.Errorf("got %d messages", len(body.Messages))
		}

		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{
				"role":    "assistant",
				"content": "Here is your standup.",
			},
		})
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "llama3.2")
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q", p.Name())
	}

	result, err := p.Chat(context.Background(), []Message{
		{Role: "system", Content: "You are a standup generator."},
		{Role: "user", Content: "Generate standup."},
	}, ChatOpts{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result != "Here is your standup." {
		t.Errorf("result = %q", result)
	}
}

func TestOllama_ChatWithOpts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model   string `json:"model"`
			Options *struct {
				Temperature float64 `json:"temperature"`
				NumPredict  int     `json:"num_predict"`
			} `json:"options"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "custom-model" {
			t.Errorf("model = %q, want custom-model", body.Model)
		}
		if body.Options == nil {
			t.Fatal("expected options")
		}
		if body.Options.Temperature != 0.7 {
			t.Errorf("temperature = %f", body.Options.Temperature)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"content": "ok"},
		})
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "llama3.2")
	_, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: "test"},
	}, ChatOpts{Model: "custom-model", Temperature: 0.7, MaxTokens: 100})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestOllama_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "nonexistent")
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenAI_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test-key" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}

		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "gpt-4o-mini" {
			t.Errorf("model = %q", body.Model)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "OpenAI standup"}},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAI("sk-test-key", "gpt-4o-mini", srv.URL)
	if p.Name() != "openai" {
		t.Errorf("Name() = %q", p.Name())
	}

	result, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: "standup"},
	}, ChatOpts{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result != "OpenAI standup" {
		t.Errorf("result = %q", result)
	}
}

func TestOpenAI_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := NewOpenAI("bad-key", "", srv.URL)
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenAI_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := NewOpenAI("key", "", srv.URL)
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAnthropic_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant-test" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != anthropicAPIVersion {
			t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
		}

		var body struct {
			Model    string `json:"model"`
			System   string `json:"system"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "claude-sonnet-4-20250514" {
			t.Errorf("model = %q", body.Model)
		}
		if body.System != "You are a standup generator." {
			t.Errorf("system = %q", body.System)
		}
		// System message should NOT appear in messages array.
		if len(body.Messages) != 1 {
			t.Errorf("got %d messages (system should be separate)", len(body.Messages))
		}
		if body.MaxTokens <= 0 {
			t.Error("max_tokens should be set")
		}

		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": "Anthropic standup"},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropic("sk-ant-test", "claude-sonnet-4-20250514", srv.URL)
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q", p.Name())
	}

	result, err := p.Chat(context.Background(), []Message{
		{Role: "system", Content: "You are a standup generator."},
		{Role: "user", Content: "standup"},
	}, ChatOpts{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result != "Anthropic standup" {
		t.Errorf("result = %q", result)
	}
}

func TestAnthropic_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := NewAnthropic("bad-key", "", srv.URL)
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAnthropic_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := NewAnthropic("key", "", srv.URL)
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOllama_Defaults(t *testing.T) {
	p := NewOllama("", "")
	if p.baseURL != defaultOllamaURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, defaultOllamaURL)
	}
	if p.model != "gemma4" {
		t.Errorf("model = %q", p.model)
	}
}

func TestOpenAI_Defaults(t *testing.T) {
	p := NewOpenAI("key", "", "")
	if p.baseURL != defaultOpenAIURL {
		t.Errorf("baseURL = %q", p.baseURL)
	}
	if p.model != "gpt-5.4-mini" {
		t.Errorf("model = %q", p.model)
	}
}

func TestAnthropic_Defaults(t *testing.T) {
	p := NewAnthropic("key", "", "")
	if p.baseURL != defaultAnthropicURL {
		t.Errorf("baseURL = %q", p.baseURL)
	}
	if p.model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", p.model)
	}
}
