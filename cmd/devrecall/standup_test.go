package main

import (
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/internal/summarizer"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// TestStandupPipeline tests the query→summarize pipeline without config/git dependencies.
func TestStandupPipeline(t *testing.T) {
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	// Seed activities across two days.
	targetDay := time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		{
			Source: models.SourceGit, SourceID: "repo:aaa", Type: models.TypeCommit,
			Title: "Fix auth refresh", Metadata: `{"repo":"backend-api","sha":"aaa1111222","files_changed":3,"insertions":47,"deletions":12}`,
			Timestamp: targetDay.Add(10 * time.Hour),
		},
		{
			Source: models.SourceGit, SourceID: "repo:bbb", Type: models.TypeCommit,
			Title: "Add tests", Metadata: `{"repo":"backend-api","sha":"bbb2222333","files_changed":1,"insertions":10,"deletions":0}`,
			Timestamp: targetDay.Add(14 * time.Hour),
		},
		{
			// Different day — should be excluded.
			Source: models.SourceGit, SourceID: "repo:ccc", Type: models.TypeCommit,
			Title: "Other day work", Metadata: `{"repo":"other","sha":"ccc","files_changed":1,"insertions":1,"deletions":0}`,
			Timestamp: targetDay.AddDate(0, 0, -1).Add(10 * time.Hour),
		},
	}
	if _, err := db.InsertActivities(activities); err != nil {
		t.Fatalf("InsertActivities: %v", err)
	}

	// Query for target day only.
	dayStart := targetDay
	dayEnd := targetDay.AddDate(0, 0, 1)
	filtered, err := db.ListActivities(storage.ActivityFilter{
		Source: models.SourceGit,
		After:  dayStart,
		Before: dayEnd,
	})
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("got %d activities, want 2", len(filtered))
	}

	// Generate standup.
	s := summarizer.NewTemplateSummarizer()
	report, err := s.Standup(filtered)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if report == "" {
		t.Fatal("expected non-empty report")
	}

	// Verify content.
	wantSubstrings := []string{
		"Friday (2026-03-27):",
		"backend-api: Fix auth refresh",
		"backend-api: Add tests",
	}
	for _, want := range wantSubstrings {
		if !contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}

	// Should NOT contain the other-day activity.
	if contains(report, "Other day work") {
		t.Errorf("report should not contain other-day activity:\n%s", report)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
