package summarizer

import (
	"fmt"
	"os"
	"path/filepath"
)

// PromptType identifies a prompt template.
type PromptType string

const (
	PromptStandup    PromptType = "standup"
	PromptWeekly     PromptType = "weekly"
	PromptBragDoc    PromptType = "brag"
	PromptPerfReview PromptType = "perf-review"
)

// AllPromptTypes returns all available prompt types.
func AllPromptTypes() []PromptType {
	return []PromptType{PromptStandup, PromptWeekly, PromptBragDoc, PromptPerfReview}
}

// builtinPrompts maps prompt types to their default system prompts.
var builtinPrompts = map[PromptType]string{
	PromptStandup:    standupSystemPrompt,
	PromptWeekly:     weeklySystemPrompt,
	PromptBragDoc:    bragDocSystemPrompt,
	PromptPerfReview: perfReviewSystemPrompt,
}

// PromptLoader resolves prompt templates. It checks for user overrides in
// a prompts directory, falling back to built-in defaults.
type PromptLoader struct {
	dir string // e.g., ~/.devrecall/prompts
}

// NewPromptLoader creates a loader that checks dir for custom prompt files.
// If dir is empty, only built-in prompts are used.
func NewPromptLoader(dir string) *PromptLoader {
	return &PromptLoader{dir: dir}
}

// Load returns the prompt for the given type. If a custom file exists at
// <dir>/<type>.txt, it is used; otherwise the built-in default is returned.
func (l *PromptLoader) Load(pt PromptType) string {
	if l.dir != "" {
		path := filepath.Join(l.dir, string(pt)+".txt")
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
	}
	return builtinPrompts[pt]
}

// IsCustom returns true if a custom prompt file exists for the given type.
func (l *PromptLoader) IsCustom(pt PromptType) bool {
	if l.dir == "" {
		return false
	}
	path := filepath.Join(l.dir, string(pt)+".txt")
	_, err := os.Stat(path)
	return err == nil
}

// ExportDefaults writes all built-in prompt templates to the prompts directory
// so the user can customize them. Does not overwrite existing files.
func (l *PromptLoader) ExportDefaults() error {
	if l.dir == "" {
		return fmt.Errorf("no prompts directory configured")
	}

	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return fmt.Errorf("create prompts dir: %w", err)
	}

	for pt, content := range builtinPrompts {
		path := filepath.Join(l.dir, string(pt)+".txt")
		// Don't overwrite existing customizations.
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
