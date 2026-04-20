package storage

import (
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

func TestPruneActivities(t *testing.T) {
	db := mustOpen(t)

	// Insert activities at different times.
	old := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	db.InsertActivity(models.Activity{
		Source: "git", SourceID: "r:old1", Type: "commit",
		Title: "Old commit", Timestamp: old,
	})
	db.InsertActivity(models.Activity{
		Source: "git", SourceID: "r:old2", Type: "commit",
		Title: "Another old commit", Timestamp: old.Add(24 * time.Hour),
	})
	db.InsertActivity(models.Activity{
		Source: "git", SourceID: "r:new1", Type: "commit",
		Title: "Recent commit", Timestamp: recent,
	})

	// Prune activities older than 2026-01-01.
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := db.PruneActivities(cutoff)
	if err != nil {
		t.Fatalf("PruneActivities: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	// Only the recent activity should remain.
	acts, err := db.ListActivities(ActivityFilter{})
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(acts) != 1 {
		t.Fatalf("remaining = %d, want 1", len(acts))
	}
	if acts[0].Title != "Recent commit" {
		t.Errorf("remaining title = %q", acts[0].Title)
	}
}

func TestPruneActivities_NoneOldEnough(t *testing.T) {
	db := mustOpen(t)

	db.InsertActivity(models.Activity{
		Source: "git", SourceID: "r:a1", Type: "commit",
		Title: "A commit", Timestamp: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
	})

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := db.PruneActivities(cutoff)
	if err != nil {
		t.Fatalf("PruneActivities: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}

func TestPruneSummaries(t *testing.T) {
	db := mustOpen(t)

	db.InsertSummary(models.Summary{
		PeriodType: "daily", PeriodStart: "2025-01-15", PeriodEnd: "2025-01-16",
		SummaryText: "Old daily", ActivityCount: 5,
	})
	db.InsertSummary(models.Summary{
		PeriodType: "quarterly", PeriodStart: "2025-01-01", PeriodEnd: "2025-04-01",
		SummaryText: "Old quarterly", ActivityCount: 100,
	})
	db.InsertSummary(models.Summary{
		PeriodType: "daily", PeriodStart: "2026-03-15", PeriodEnd: "2026-03-16",
		SummaryText: "Recent daily", ActivityCount: 3,
	})

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Prune summaries but keep quarterly.
	deleted, err := db.PruneSummaries(cutoff, []string{"quarterly"})
	if err != nil {
		t.Fatalf("PruneSummaries: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only old daily, quarterly kept)", deleted)
	}

	// The quarterly and recent daily should remain.
	all, err := db.ListSummaries("", 50)
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("remaining = %d, want 2", len(all))
	}
}

func TestPruneSummaries_KeepAll(t *testing.T) {
	db := mustOpen(t)

	db.InsertSummary(models.Summary{
		PeriodType: "daily", PeriodStart: "2025-01-15", PeriodEnd: "2025-01-16",
		SummaryText: "Old daily", ActivityCount: 5,
	})

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Keep all types = nothing deleted.
	deleted, err := db.PruneSummaries(cutoff, []string{"daily", "weekly", "monthly", "quarterly"})
	if err != nil {
		t.Fatalf("PruneSummaries: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}

func TestCountActivitiesBefore(t *testing.T) {
	db := mustOpen(t)

	db.InsertActivity(models.Activity{
		Source: "git", SourceID: "r:a1", Type: "commit",
		Title: "Old", Timestamp: time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
	})
	db.InsertActivity(models.Activity{
		Source: "git", SourceID: "r:a2", Type: "commit",
		Title: "New", Timestamp: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
	})

	count, err := db.CountActivitiesBefore(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CountActivitiesBefore: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestCountSummariesBefore(t *testing.T) {
	db := mustOpen(t)

	db.InsertSummary(models.Summary{
		PeriodType: "daily", PeriodStart: "2025-01-15", PeriodEnd: "2025-01-16",
		SummaryText: "Old daily", ActivityCount: 5,
	})
	db.InsertSummary(models.Summary{
		PeriodType: "quarterly", PeriodStart: "2025-01-01", PeriodEnd: "2025-04-01",
		SummaryText: "Old quarterly", ActivityCount: 100,
	})

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Without keeping anything.
	count, err := db.CountSummariesBefore(cutoff, nil)
	if err != nil {
		t.Fatalf("CountSummariesBefore: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	// Keeping quarterly.
	count2, err := db.CountSummariesBefore(cutoff, []string{"quarterly"})
	if err != nil {
		t.Fatalf("CountSummariesBefore: %v", err)
	}
	if count2 != 1 {
		t.Errorf("count = %d, want 1 (only daily)", count2)
	}
}
