package summarizer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptLoader_BuiltinDefaults(t *testing.T) {
	loader := NewPromptLoader("")

	for _, pt := range AllPromptTypes() {
		prompt := loader.Load(pt)
		if prompt == "" {
			t.Errorf("built-in prompt for %q should not be empty", pt)
		}
	}
}

func TestPromptLoader_CustomOverride(t *testing.T) {
	dir := t.TempDir()
	custom := "You are a custom standup bot."
	os.WriteFile(filepath.Join(dir, "standup.txt"), []byte(custom), 0o644)

	loader := NewPromptLoader(dir)

	// Custom prompt should be returned.
	got := loader.Load(PromptStandup)
	if got != custom {
		t.Errorf("Load(standup) = %q, want custom", got)
	}

	// Non-customized prompt should return built-in.
	got = loader.Load(PromptWeekly)
	if got != weeklySystemPrompt {
		t.Error("Load(weekly) should return built-in when no custom file")
	}
}

func TestPromptLoader_IsCustom(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "brag.txt"), []byte("custom"), 0o644)

	loader := NewPromptLoader(dir)

	if !loader.IsCustom(PromptBragDoc) {
		t.Error("IsCustom(brag) should be true when file exists")
	}
	if loader.IsCustom(PromptWeekly) {
		t.Error("IsCustom(weekly) should be false when no file")
	}
}

func TestPromptLoader_IsCustom_EmptyDir(t *testing.T) {
	loader := NewPromptLoader("")
	for _, pt := range AllPromptTypes() {
		if loader.IsCustom(pt) {
			t.Errorf("IsCustom(%q) should be false with empty dir", pt)
		}
	}
}

func TestPromptLoader_ExportDefaults(t *testing.T) {
	dir := t.TempDir()
	promptsDir := filepath.Join(dir, "prompts")

	loader := NewPromptLoader(promptsDir)
	if err := loader.ExportDefaults(); err != nil {
		t.Fatalf("ExportDefaults: %v", err)
	}

	// All 4 prompt files should exist.
	for _, pt := range AllPromptTypes() {
		path := filepath.Join(promptsDir, string(pt)+".txt")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s should not be empty", path)
		}
	}
}

func TestPromptLoader_ExportDefaults_NoOverwrite(t *testing.T) {
	dir := t.TempDir()
	promptsDir := filepath.Join(dir, "prompts")
	os.MkdirAll(promptsDir, 0o755)

	// Write a custom prompt.
	custom := "my custom standup prompt"
	os.WriteFile(filepath.Join(promptsDir, "standup.txt"), []byte(custom), 0o644)

	loader := NewPromptLoader(promptsDir)
	if err := loader.ExportDefaults(); err != nil {
		t.Fatalf("ExportDefaults: %v", err)
	}

	// Custom prompt should NOT be overwritten.
	data, _ := os.ReadFile(filepath.Join(promptsDir, "standup.txt"))
	if string(data) != custom {
		t.Errorf("ExportDefaults overwrote custom prompt: got %q", string(data))
	}

	// Other prompts should be created.
	_, err := os.Stat(filepath.Join(promptsDir, "weekly.txt"))
	if err != nil {
		t.Error("weekly.txt should have been created")
	}
}

func TestAllPromptTypes(t *testing.T) {
	types := AllPromptTypes()
	if len(types) != 4 {
		t.Errorf("expected 4 prompt types, got %d", len(types))
	}
}
