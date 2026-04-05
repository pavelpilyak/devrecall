package storage

import (
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func TestSearchFTS_BasicMatch(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()

	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Fix auth token refresh", Content: "Handle expired OAuth tokens",
		Timestamp: now,
	})
	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a2", Type: models.TypeCommit,
		Title: "Update README badges", Content: "Add CI status badge",
		Timestamp: now.Add(-time.Hour),
	})
	db.InsertActivity(models.Activity{
		Source: models.SourceSlack, SourceID: "s:m1", Type: models.TypeMessage,
		Title: "Discussion about auth flow", Content: "We need to fix the token expiry",
		Timestamp: now.Add(-2 * time.Hour),
	})

	results, err := db.SearchFTS("auth token", ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}

	// Should find both auth-related activities.
	if len(results) < 1 {
		t.Fatalf("got %d results, want at least 1", len(results))
	}

	// All results should mention auth or token.
	for _, r := range results {
		if r.Activity.Title == "Update README badges" {
			t.Error("README activity should not match 'auth token'")
		}
	}
}

func TestSearchFTS_SourceFilter(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()

	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Fix auth bug", Timestamp: now,
	})
	db.InsertActivity(models.Activity{
		Source: models.SourceSlack, SourceID: "s:m1", Type: models.TypeMessage,
		Title: "Discussion about auth", Timestamp: now,
	})

	results, err := db.SearchFTS("auth", ActivityFilter{Source: models.SourceSlack}, 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (only slack)", len(results))
	}
	if results[0].Activity.Source != models.SourceSlack {
		t.Errorf("source = %q, want slack", results[0].Activity.Source)
	}
}

func TestSearchFTS_DateFilter(t *testing.T) {
	db := mustOpen(t)
	old := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Fix auth in January", Timestamp: old,
	})
	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a2", Type: models.TypeCommit,
		Title: "Fix auth in March", Timestamp: recent,
	})

	after := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	results, err := db.SearchFTS("auth", ActivityFilter{After: after}, 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Activity.Title != "Fix auth in March" {
		t.Errorf("title = %q", results[0].Activity.Title)
	}
}

func TestSearchFTS_EmptyQuery(t *testing.T) {
	db := mustOpen(t)
	results, err := db.SearchFTS("", ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("empty query should return 0 results, got %d", len(results))
	}
}

func TestSearchFTS_NoMatch(t *testing.T) {
	db := mustOpen(t)
	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Fix auth bug", Timestamp: time.Now().UTC(),
	})

	results, err := db.SearchFTS("kubernetes", ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results for non-matching query", len(results))
	}
}
