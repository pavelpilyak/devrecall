package llm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/config"
)

func TestFromConfig_Ollama(t *testing.T) {
	store := mustTempStore(t)
	cfg := &config.Config{}
	cfg.LLM.Provider = "ollama"
	cfg.LLM.Model = "llama3.2"

	p, err := FromConfig(cfg, store)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestFromConfig_OllamaDefault(t *testing.T) {
	store := mustTempStore(t)
	cfg := &config.Config{} // empty provider defaults to ollama

	p, err := FromConfig(cfg, store)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestFromConfig_OpenAI(t *testing.T) {
	store := mustTempStore(t)
	store.Save("llm", "openai", APIKeyToken{APIKey: "sk-test"})

	cfg := &config.Config{}
	cfg.LLM.Provider = "openai"
	cfg.LLM.Model = "gpt-4o"

	p, err := FromConfig(cfg, store)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestFromConfig_OpenAI_NoKey(t *testing.T) {
	store := mustTempStore(t)
	cfg := &config.Config{}
	cfg.LLM.Provider = "openai"

	_, err := FromConfig(cfg, store)
	if err == nil {
		t.Fatal("expected error when no API key stored")
	}
}

func TestFromConfig_Anthropic(t *testing.T) {
	store := mustTempStore(t)
	store.Save("llm", "anthropic", APIKeyToken{APIKey: "sk-ant-test"})

	cfg := &config.Config{}
	cfg.LLM.Provider = "anthropic"

	p, err := FromConfig(cfg, store)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestFromConfig_Anthropic_NoKey(t *testing.T) {
	store := mustTempStore(t)
	cfg := &config.Config{}
	cfg.LLM.Provider = "anthropic"

	_, err := FromConfig(cfg, store)
	if err == nil {
		t.Fatal("expected error when no API key stored")
	}
}

func TestFromConfig_UnknownProvider(t *testing.T) {
	store := mustTempStore(t)
	cfg := &config.Config{}
	cfg.LLM.Provider = "gemini"

	_, err := FromConfig(cfg, store)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func mustTempStore(t *testing.T) *auth.FileTokenStore {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "devrecall-test-"+t.Name())
	os.MkdirAll(dir, 0o700)
	t.Cleanup(func() { os.RemoveAll(dir) })
	store, err := auth.NewFileTokenStore(dir)
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}
	return store
}
