package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/embedding"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// fakeEmbedder returns a fixed vector, so a test can control exactly which
// stored activity is "nearest" without loading the real ONNX model.
type fakeEmbedder struct{ vec []float32 }

func (f fakeEmbedder) Embed(context.Context, string) ([]float32, error) { return f.vec, nil }
func (f fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = f.vec
	}
	return out, nil
}
func (f fakeEmbedder) Dimensions() int { return 384 }
func (f fakeEmbedder) Name() string    { return "fake" }

func pad384(v []float32) []float32 {
	out := make([]float32, 384)
	copy(out, v)
	return out
}

// The endpoint must return semantically-near activities that share no keywords
// with the query — the whole reason for fusing a vector arm into search. The
// pre-existing TestHandleSearch only covers the keyword path, since its test DB
// has no embeddings stored.
func TestHandleSearch_IncludesVectorMatches(t *testing.T) {
	srv, db := setupTestServer(t)
	srv.embedderFactory = func() embedding.Embedder {
		return fakeEmbedder{vec: pad384([]float32{1, 0})}
	}

	id, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "c1", Type: models.TypeCommit,
		Title: "Rotate expired credentials", Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	if err := db.InsertEmbedding(id, "fake", pad384([]float32{1, 0})); err != nil {
		t.Fatalf("InsertEmbedding: %v", err)
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	// No token in this query appears in the activity's title.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("GET", "/api/search?q=stale+session+problem", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Count   int `json:"count"`
		Results []struct {
			Activity models.Activity `json:"activity"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count == 0 {
		t.Fatal("got 0 results; the vector arm did not contribute to /api/search")
	}
	if resp.Results[0].Activity.ID != id {
		t.Errorf("top result = %d, want %d", resp.Results[0].Activity.ID, id)
	}
}

// With nothing embedded, search must not reach for an embedder at all — that
// guard is what keeps a first-run ONNX model download off the search path.
func TestHandleSearch_SkipsEmbedderWhenNothingEmbedded(t *testing.T) {
	srv, db := setupTestServer(t)
	called := false
	srv.embedderFactory = func() embedding.Embedder {
		called = true
		return fakeEmbedder{vec: pad384([]float32{1, 0})}
	}

	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "c1", Type: models.TypeCommit,
		Title: "Fix authentication bug", Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("GET", "/api/search?q=authentication", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if called {
		t.Error("embedder was built even though no embeddings are stored")
	}
}

func TestEmbedder_CachesInstance(t *testing.T) {
	s := &Server{}
	s.cfg.Store(&config.Config{Embedding: config.EmbeddingConfig{Provider: "onnx"}})

	first := s.Embedder()
	if first == nil {
		t.Fatal("Embedder() = nil, want an onnx embedder")
	}
	// The ONNX embedder memoizes its model+session on the instance, so handing
	// back the same pointer is the whole point of the cache.
	if second := s.Embedder(); second != first {
		t.Error("Embedder() returned a new instance on the second call; cache not working")
	}
}

func TestEmbedder_RebuildsWhenConfigChanges(t *testing.T) {
	s := &Server{}
	s.cfg.Store(&config.Config{Embedding: config.EmbeddingConfig{Provider: "onnx"}})

	first := s.Embedder()
	if first == nil {
		t.Fatal("Embedder() = nil")
	}

	// Simulate WatchConfig / POST /api/llm/config swapping the provider. A
	// stale cached embedder here would keep using the old provider forever.
	s.cfg.Store(&config.Config{
		Embedding: config.EmbeddingConfig{Provider: "ollama", Model: "all-minilm"},
	})

	second := s.Embedder()
	if second == nil {
		t.Fatal("Embedder() = nil after config change")
	}
	if second == first {
		t.Error("Embedder() returned the stale instance after the provider changed")
	}
	if second.Name() != "ollama" {
		t.Errorf("Embedder().Name() = %q, want %q", second.Name(), "ollama")
	}
}

func TestEmbedder_NilOnUnbuildableConfig(t *testing.T) {
	s := &Server{}
	s.cfg.Store(&config.Config{Embedding: config.EmbeddingConfig{Provider: "nonsense"}})

	// Callers treat nil as "no vector arm" and degrade to keyword-only rather
	// than failing the request.
	if got := s.Embedder(); got != nil {
		t.Errorf("Embedder() = %v, want nil for an unknown provider", got)
	}
}
