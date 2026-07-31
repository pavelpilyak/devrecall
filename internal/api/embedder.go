package api

import (
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/embedding"
)

// Embedder returns a lazily-built, process-wide Embedder, or nil if one can't
// be constructed (e.g. the OpenAI provider is selected but no key is stored).
//
// Caching the instance matters: the default ONNX embedder loads its model and
// hugot session on first use and memoizes them *on the instance*, so building a
// fresh one per request would reload the model every time. Callers that run per
// HTTP request — search, chat — must go through here rather than calling
// embedding.FromConfig directly.
//
// The cache is keyed on the config it was built from, so a provider change via
// WatchConfig or POST /api/llm/config rebuilds it on the next call instead of
// silently serving a stale embedder.
func (s *Server) Embedder() embedding.Embedder {
	if s.embedderFactory != nil {
		return s.embedderFactory()
	}

	cfg := s.Cfg()
	key := embedderKey{emb: cfg.Embedding, llmBaseURL: cfg.LLM.BaseURL}

	s.embMu.Lock()
	defer s.embMu.Unlock()

	if s.embBuilt && s.embKey == key {
		return s.embedder
	}

	emb, err := embedding.FromConfig(cfg, s.tokenStore)
	if err != nil {
		// Embeddings are optional everywhere they're used: search degrades to
		// keyword-only and semantic_search_activities reports the failure at
		// call time. Cache the nil so we don't retry a doomed build per request.
		emb = nil
	}
	s.embedder = emb
	s.embKey = key
	s.embBuilt = true
	return emb
}

// embedderKey captures everything embedding.FromConfig reads from config, so a
// change to any of it invalidates the cached embedder. The OpenAI path also
// reads the token store, which isn't captured here — a newly added key is
// picked up on the next config change or server restart.
type embedderKey struct {
	emb        config.EmbeddingConfig
	llmBaseURL string
}
