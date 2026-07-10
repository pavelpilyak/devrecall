package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/llm"
)

// handleLLMHealth reports whether the configured LLM provider is reachable.
// Unlike POST /api/llm/test (which runs a real completion and returns a 502 on
// failure), this always responds 200 with an {ok, provider, model, error}
// body so the desktop app can poll it cheaply to drive the sidebar status
// without treating a down LLM as an HTTP error.
func (s *Server) handleLLMHealth(w http.ResponseWriter, r *http.Request) {
	cfg := s.Cfg()
	ok, msg := s.probeLLM(r.Context(), cfg)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       ok,
		"provider": cfg.LLM.Provider,
		"model":    cfg.LLM.Model,
		"error":    msg,
	})
}

// probeLLM does a lightweight reachability check of the configured provider.
// Ollama is probed via its /api/tags endpoint — no model is loaded and it also
// catches the "server up but model not pulled" case. BYOK providers use a
// one-token chat ping, which is the cheapest way to also validate the API key.
func (s *Server) probeLLM(ctx context.Context, cfg *config.Config) (bool, string) {
	if cfg.LLM.Provider == "" {
		return false, "no LLM provider configured"
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	if cfg.LLM.Provider == "ollama" {
		return probeOllama(ctx, cfg)
	}

	// BYOK (openai / anthropic): a minimal ping validates key + reachability.
	provider, err := llm.FromConfig(cfg, s.tokenStore)
	if err != nil {
		return false, err.Error()
	}
	if _, err := provider.Chat(ctx, []llm.Message{{Role: "user", Content: "ping"}}, llm.ChatOpts{MaxTokens: 1}); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// probeOllama checks that the local Ollama server is reachable and that the
// configured model is available, without triggering inference.
func probeOllama(ctx context.Context, cfg *config.Config) (bool, string) {
	base := cfg.LLM.BaseURL
	if base == "" {
		base = "http://localhost:11434"
	}
	url := strings.TrimRight(base, "/") + "/api/tags"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("Ollama not reachable at %s — is it installed and running?", base)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("Ollama returned HTTP %d", resp.StatusCode)
	}

	model := cfg.LLM.Model
	if model == "" {
		model = "gemma4"
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		// Server answered but body was unexpected; treat as reachable.
		return true, ""
	}
	for _, m := range tags.Models {
		// Ollama reports names like "gemma4:latest"; match the base name too.
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			return true, ""
		}
	}
	return false, fmt.Sprintf("Ollama is running but model %q is not pulled (try: ollama pull %s)", model, model)
}
