package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllama_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q, want /api/embed", r.URL.Path)
		}

		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "all-minilm" {
			t.Errorf("model = %q, want all-minilm", body.Model)
		}
		if body.Input != "Fix auth token" {
			t.Errorf("input = %q", body.Input)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2, 0.3}},
		})
	}))
	defer srv.Close()

	e := NewOllama(srv.URL, "all-minilm", 384)
	if e.Name() != "ollama" {
		t.Errorf("Name() = %q", e.Name())
	}
	if e.Dimensions() != 384 {
		t.Errorf("Dimensions() = %d", e.Dimensions())
	}

	vec, err := e.Embed(context.Background(), "Fix auth token")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Errorf("len(vec) = %d", len(vec))
	}
}

func TestOllama_EmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if len(body.Input) != 2 {
			t.Errorf("got %d inputs, want 2", len(body.Input))
		}

		json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2}, {0.3, 0.4}},
		})
	}))
	defer srv.Close()

	e := NewOllama(srv.URL, "", 0)
	vecs, err := e.EmbedBatch(context.Background(), []string{"text1", "text2"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 2 {
		t.Errorf("len(vecs) = %d", len(vecs))
	}
}

func TestOllama_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	e := NewOllama(srv.URL, "nonexistent", 0)
	_, err := e.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOllama_Defaults(t *testing.T) {
	e := NewOllama("", "", 0)
	if e.baseURL != defaultOllamaURL {
		t.Errorf("baseURL = %q", e.baseURL)
	}
	if e.model != "all-minilm" {
		t.Errorf("model = %q", e.model)
	}
	if e.Dimensions() != 384 {
		t.Errorf("Dimensions() = %d", e.Dimensions())
	}
}

func TestOpenAI_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}

		var body struct {
			Model      string   `json:"model"`
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "text-embedding-3-small" {
			t.Errorf("model = %q", body.Model)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float32{0.1, 0.2, 0.3}, "index": 0},
			},
		})
	}))
	defer srv.Close()

	e := NewOpenAI("sk-test", "text-embedding-3-small", srv.URL, 1536)
	if e.Name() != "openai" {
		t.Errorf("Name() = %q", e.Name())
	}

	vec, err := e.Embed(context.Background(), "Fix auth")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Errorf("len(vec) = %d", len(vec))
	}
}

func TestOpenAI_EmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float32{0.1}, "index": 0},
				{"embedding": []float32{0.2}, "index": 1},
			},
		})
	}))
	defer srv.Close()

	e := NewOpenAI("sk-test", "", srv.URL, 0)
	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 2 {
		t.Errorf("len(vecs) = %d", len(vecs))
	}
}

func TestOpenAI_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := NewOpenAI("bad-key", "", srv.URL, 0)
	_, err := e.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenAI_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	e := NewOpenAI("key", "", srv.URL, 0)
	_, err := e.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenAI_Defaults(t *testing.T) {
	e := NewOpenAI("key", "", "", 0)
	if e.baseURL != defaultOpenAIURL {
		t.Errorf("baseURL = %q", e.baseURL)
	}
	if e.model != "text-embedding-3-small" {
		t.Errorf("model = %q", e.model)
	}
	if e.Dimensions() != 1536 {
		t.Errorf("Dimensions() = %d", e.Dimensions())
	}
}
