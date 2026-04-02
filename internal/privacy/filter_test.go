package privacy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/internal/config"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

var ts = time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)

func gitActivity(title string) models.Activity {
	meta, _ := json.Marshal(map[string]any{
		"repo":          "backend-api",
		"sha":           "abc123def",
		"author_name":   "Alice",
		"author_email":  "alice@example.com",
		"files_changed": 3,
		"insertions":    47,
		"deletions":     12,
	})
	return models.Activity{
		Source:    models.SourceGit,
		Type:      models.TypeCommit,
		Title:     title,
		Content:   "diff --git a/main.go ...",
		Metadata:  string(meta),
		Timestamp: ts,
	}
}

func slackActivity(title string, withThread bool) models.Activity {
	meta := map[string]any{
		"channel_id":   "C123",
		"channel_name": "backend",
		"reply_count":  5,
		"participants": []string{"alice", "bob"},
	}
	if withThread {
		meta["thread_msgs"] = []map[string]string{
			{"user": "alice", "text": "Should we switch to blue-green?"},
			{"user": "bob", "text": "Yes, let's do it."},
		}
		meta["summary"] = map[string]any{
			"topic":     "Deployment strategy",
			"decisions": []string{"Switch to blue-green"},
		}
	}
	metaJSON, _ := json.Marshal(meta)
	return models.Activity{
		Source:    models.SourceSlack,
		Type:      models.TypeMessage,
		Title:     title,
		Content:   "Raw message text",
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}
}

func TestApply_FullMode_PreservesEverything(t *testing.T) {
	a := gitActivity("Fix auth refresh")
	cfg := config.PrivacyConfig{Git: config.PrivacyFull}

	result := Apply([]models.Activity{a}, cfg)

	if len(result) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(result))
	}
	if result[0].Title != a.Title {
		t.Error("title should be preserved")
	}
	if result[0].Content != a.Content {
		t.Error("content should be preserved")
	}
	if result[0].Metadata != a.Metadata {
		t.Error("metadata should be preserved")
	}
}

func TestApply_DefaultsToFull(t *testing.T) {
	a := gitActivity("Fix auth refresh")
	cfg := config.PrivacyConfig{} // empty = default to full

	result := Apply([]models.Activity{a}, cfg)

	if result[0].Title != a.Title || result[0].Content != a.Content {
		t.Error("empty config should default to full mode")
	}
}

func TestApply_SummaryMode_Git(t *testing.T) {
	a := gitActivity("Fix auth refresh")
	cfg := config.PrivacyConfig{Git: config.PrivacySummary}

	result := Apply([]models.Activity{a}, cfg)

	if result[0].Title != "Fix auth refresh" {
		t.Error("summary mode should keep title")
	}
	if result[0].Content != "" {
		t.Error("summary mode should strip content")
	}
	// Git metadata is already summary-level, should be preserved.
	if result[0].Metadata != a.Metadata {
		t.Error("git metadata should be preserved in summary mode")
	}
}

func TestApply_SummaryMode_Slack(t *testing.T) {
	a := slackActivity("Thread in #backend", true)
	cfg := config.PrivacyConfig{Slack: config.PrivacySummary}

	result := Apply([]models.Activity{a}, cfg)

	if result[0].Title != "Thread in #backend" {
		t.Error("summary mode should keep title")
	}
	if result[0].Content != "" {
		t.Error("summary mode should strip content")
	}

	// thread_msgs should be stripped.
	var meta map[string]json.RawMessage
	json.Unmarshal([]byte(result[0].Metadata), &meta)
	if _, ok := meta["thread_msgs"]; ok {
		t.Error("summary mode should strip thread_msgs")
	}
	// summary should be preserved.
	if _, ok := meta["summary"]; !ok {
		t.Error("summary mode should preserve summary")
	}
	// channel_name should be preserved.
	if _, ok := meta["channel_name"]; !ok {
		t.Error("summary mode should preserve channel_name")
	}
}

func TestApply_MetadataMode_Git(t *testing.T) {
	a := gitActivity("Fix auth refresh")
	cfg := config.PrivacyConfig{Git: config.PrivacyMetadata}

	result := Apply([]models.Activity{a}, cfg)

	if result[0].Title != "" {
		t.Errorf("metadata mode should strip title, got %q", result[0].Title)
	}
	if result[0].Content != "" {
		t.Error("metadata mode should strip content")
	}

	// Should keep repo and stats, strip author info and SHA.
	var meta map[string]any
	json.Unmarshal([]byte(result[0].Metadata), &meta)
	if meta["repo"] != "backend-api" {
		t.Error("metadata mode should keep repo")
	}
	if meta["files_changed"] != float64(3) {
		t.Error("metadata mode should keep files_changed")
	}
	if _, ok := meta["sha"]; ok {
		t.Error("metadata mode should strip SHA")
	}
	if _, ok := meta["author_name"]; ok {
		t.Error("metadata mode should strip author_name")
	}
}

func TestApply_MetadataMode_Slack(t *testing.T) {
	a := slackActivity("Thread in #backend", true)
	cfg := config.PrivacyConfig{Slack: config.PrivacyMetadata}

	result := Apply([]models.Activity{a}, cfg)

	if result[0].Title != "" {
		t.Errorf("metadata mode should strip title, got %q", result[0].Title)
	}
	if result[0].Content != "" {
		t.Error("metadata mode should strip content")
	}

	var meta map[string]any
	json.Unmarshal([]byte(result[0].Metadata), &meta)
	if meta["channel_name"] != "backend" {
		t.Error("metadata mode should keep channel_name")
	}
	if _, ok := meta["summary"]; ok {
		t.Error("metadata mode should strip summary")
	}
	if _, ok := meta["thread_msgs"]; ok {
		t.Error("metadata mode should strip thread_msgs")
	}
	if _, ok := meta["participants"]; ok {
		t.Error("metadata mode should strip participants")
	}
}

func TestApply_MixedSources(t *testing.T) {
	activities := []models.Activity{
		gitActivity("Fix bug"),
		slackActivity("Message", false),
	}
	cfg := config.PrivacyConfig{
		Git:   config.PrivacyFull,
		Slack: config.PrivacyMetadata,
	}

	result := Apply(activities, cfg)

	// Git should be full.
	if result[0].Title != "Fix bug" {
		t.Error("git full: title should be preserved")
	}
	// Slack should be metadata-only.
	if result[1].Title != "" {
		t.Error("slack metadata: title should be stripped")
	}
}

func TestApply_DoesNotModifyOriginal(t *testing.T) {
	a := gitActivity("Fix bug")
	original := a.Title
	cfg := config.PrivacyConfig{Git: config.PrivacyMetadata}

	Apply([]models.Activity{a}, cfg)

	if a.Title != original {
		t.Error("Apply should not modify the original activity")
	}
}

func TestModeFor_Defaults(t *testing.T) {
	cfg := config.PrivacyConfig{}

	for _, source := range []string{"git", "slack", "calendar", "jira", "linear", "unknown"} {
		if mode := cfg.ModeFor(source); mode != config.PrivacyFull {
			t.Errorf("ModeFor(%q) = %q, want %q", source, mode, config.PrivacyFull)
		}
	}
}
