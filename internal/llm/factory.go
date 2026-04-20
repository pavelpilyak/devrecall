package llm

import (
	"fmt"

	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/config"
)

// APIKeyToken is the structure stored in the token store for LLM API keys.
type APIKeyToken struct {
	APIKey string `json:"api_key"`
}

// FromConfig creates the appropriate LLM provider from the app configuration.
// It loads API keys from the token store as needed.
func FromConfig(cfg *config.Config, store auth.TokenStore) (Provider, error) {
	switch cfg.LLM.Provider {
	case "ollama", "":
		return NewOllama(cfg.LLM.BaseURL, cfg.LLM.Model), nil

	case "openai":
		var token APIKeyToken
		if err := store.Load("llm", "openai", &token); err != nil {
			return nil, fmt.Errorf("OpenAI API key not found — run 'devrecall auth openai' first")
		}
		return NewOpenAI(token.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL), nil

	case "anthropic":
		var token APIKeyToken
		if err := store.Load("llm", "anthropic", &token); err != nil {
			return nil, fmt.Errorf("Anthropic API key not found — run 'devrecall auth anthropic' first")
		}
		return NewAnthropic(token.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL), nil

	default:
		return nil, fmt.Errorf("unknown LLM provider %q (supported: ollama, openai, anthropic)", cfg.LLM.Provider)
	}
}
