package embedding

import (
	"fmt"
	"path/filepath"

	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/llm"
)

// FromConfig creates the appropriate Embedder from the app configuration.
// Priority: explicit config > onnx (default, zero-setup) > ollama > openai.
func FromConfig(cfg *config.Config, store auth.TokenStore) (Embedder, error) {
	ec := cfg.Embedding

	switch ec.Provider {
	case "onnx", "":
		// Default: bundled ONNX model, zero external dependencies.
		dir, err := config.Dir()
		if err != nil {
			return nil, err
		}
		return NewONNX(filepath.Join(dir, "models")), nil

	case "ollama":
		baseURL := ec.BaseURL
		if baseURL == "" {
			baseURL = cfg.LLM.BaseURL
		}
		model := ec.Model
		if model == "" {
			model = "all-minilm"
		}
		return NewOllama(baseURL, model, ec.Dimensions), nil

	case "openai":
		var token llm.APIKeyToken
		if err := store.Load("llm", "openai", &token); err != nil {
			return nil, fmt.Errorf("OpenAI API key not found — run 'devrecall auth openai' first")
		}
		baseURL := ec.BaseURL
		if baseURL == "" {
			baseURL = cfg.LLM.BaseURL
		}
		return NewOpenAI(token.APIKey, ec.Model, baseURL, ec.Dimensions), nil

	default:
		return nil, fmt.Errorf("unknown embedding provider %q (supported: onnx, ollama, openai)", ec.Provider)
	}
}
