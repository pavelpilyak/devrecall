package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := Init()
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	expectedDir := filepath.Join(tmp, appDir)
	expectedPath := filepath.Join(expectedDir, configFile)

	if cfg.Path() != expectedPath {
		t.Errorf("Path() = %q, want %q", cfg.Path(), expectedPath)
	}

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatal("config file was not created on disk")
	}

	if !cfg.Git.Enabled {
		t.Error("Git.Enabled should default to true")
	}
	if cfg.LLM.Provider != "ollama" {
		t.Errorf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "ollama")
	}
	if cfg.LLM.Model != "llama3.2" {
		t.Errorf("LLM.Model = %q, want %q", cfg.LLM.Model, "llama3.2")
	}
}

func TestLoadReturnsInitedConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	orig, err := Init()
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.Git.Enabled != orig.Git.Enabled {
		t.Error("loaded config Git.Enabled differs from original")
	}
	if loaded.LLM.Provider != orig.LLM.Provider {
		t.Error("loaded config LLM.Provider differs from original")
	}
}

func TestLoadFailsWithoutInit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when config does not exist")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := Init()
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	cfg.Git.Emails = []string{"dev@example.com", "work@example.com"}
	cfg.Git.Repos = []string{"/home/user/project1", "/home/user/project2"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loaded.Git.Emails) != 2 || loaded.Git.Emails[0] != "dev@example.com" {
		t.Errorf("Git.Emails = %v, want [dev@example.com work@example.com]", loaded.Git.Emails)
	}
	if len(loaded.Git.Repos) != 2 {
		t.Errorf("Git.Repos length = %d, want 2", len(loaded.Git.Repos))
	}
}

func TestStringReturnsJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := Init()
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	s := cfg.String()
	if s == "" {
		t.Fatal("String() returned empty")
	}
	if s[0] != '{' {
		t.Errorf("String() should start with '{', got %q", s[:1])
	}
}
