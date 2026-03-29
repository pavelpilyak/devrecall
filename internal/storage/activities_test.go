package storage

import (
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func mustOpen(t *testing.T) *DB {
	t.Helper()
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInsertActivity(t *testing.T) {
	db := mustOpen(t)

	id, err := db.InsertActivity(models.Activity{
		Source:   models.SourceGit,
		SourceID: "repo:abc123",
		Type:     models.TypeCommit,
		Title:    "Fix bug",
		Content:  "Detailed description",
		Metadata: `{"repo":"backend"}`,
		Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestInsertActivity_Upsert(t *testing.T) {
	db := mustOpen(t)

	a := models.Activity{
		Source:    models.SourceGit,
		SourceID:  "repo:abc123",
		Type:      models.TypeCommit,
		Title:     "Original title",
		Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC),
	}
	db.InsertActivity(a)

	a.Title = "Updated title"
	db.InsertActivity(a)

	activities, err := db.ListActivities(ActivityFilter{Source: models.SourceGit})
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1 (upsert should not duplicate)", len(activities))
	}
	if activities[0].Title != "Updated title" {
		t.Errorf("title = %q, want %q", activities[0].Title, "Updated title")
	}
}

func TestInsertActivities_Batch(t *testing.T) {
	db := mustOpen(t)

	batch := []models.Activity{
		{Source: models.SourceGit, SourceID: "r:a", Type: models.TypeCommit, Title: "First", Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)},
		{Source: models.SourceGit, SourceID: "r:b", Type: models.TypeCommit, Title: "Second", Timestamp: time.Date(2026, 3, 27, 11, 0, 0, 0, time.UTC)},
		{Source: models.SourceGit, SourceID: "r:c", Type: models.TypeCommit, Title: "Third", Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)},
	}

	n, err := db.InsertActivities(batch)
	if err != nil {
		t.Fatalf("InsertActivities: %v", err)
	}
	if n != 3 {
		t.Errorf("inserted %d, want 3", n)
	}

	all, _ := db.ListActivities(ActivityFilter{})
	if len(all) != 3 {
		t.Errorf("got %d activities, want 3", len(all))
	}
}

func TestListActivities_FilterBySource(t *testing.T) {
	db := mustOpen(t)

	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:1", Type: models.TypeCommit, Title: "Git commit", Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)})
	db.InsertActivity(models.Activity{Source: models.SourceSlack, SourceID: "s:1", Type: models.TypeMessage, Title: "Slack msg", Timestamp: time.Date(2026, 3, 27, 11, 0, 0, 0, time.UTC)})

	activities, _ := db.ListActivities(ActivityFilter{Source: models.SourceGit})
	if len(activities) != 1 {
		t.Fatalf("got %d, want 1", len(activities))
	}
	if activities[0].Title != "Git commit" {
		t.Errorf("title = %q", activities[0].Title)
	}
}

func TestListActivities_FilterByDateRange(t *testing.T) {
	db := mustOpen(t)

	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:old", Type: models.TypeCommit, Title: "Old", Timestamp: time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)})
	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:mid", Type: models.TypeCommit, Title: "Mid", Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)})
	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:new", Type: models.TypeCommit, Title: "New", Timestamp: time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC)})

	activities, _ := db.ListActivities(ActivityFilter{
		After:  time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC),
		Before: time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC),
	})
	if len(activities) != 1 {
		t.Fatalf("got %d, want 1", len(activities))
	}
	if activities[0].Title != "Mid" {
		t.Errorf("title = %q, want %q", activities[0].Title, "Mid")
	}
}

func TestListActivities_OrderDescending(t *testing.T) {
	db := mustOpen(t)

	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:1", Type: models.TypeCommit, Title: "Earlier", Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)})
	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:2", Type: models.TypeCommit, Title: "Later", Timestamp: time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)})

	activities, _ := db.ListActivities(ActivityFilter{})
	if len(activities) != 2 {
		t.Fatalf("got %d, want 2", len(activities))
	}
	if activities[0].Title != "Later" {
		t.Errorf("first should be most recent, got %q", activities[0].Title)
	}
}

func TestListActivities_Limit(t *testing.T) {
	db := mustOpen(t)

	for i := 0; i < 5; i++ {
		db.InsertActivity(models.Activity{
			Source: models.SourceGit, SourceID: "g:" + string(rune('a'+i)),
			Type: models.TypeCommit, Title: "Commit",
			Timestamp: time.Date(2026, 3, 27, 10+i, 0, 0, 0, time.UTC),
		})
	}

	activities, _ := db.ListActivities(ActivityFilter{Limit: 2})
	if len(activities) != 2 {
		t.Fatalf("got %d, want 2", len(activities))
	}
}

func TestListActivities_FilterByType(t *testing.T) {
	db := mustOpen(t)

	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:1", Type: models.TypeCommit, Title: "Commit", Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)})
	db.InsertActivity(models.Activity{Source: models.SourceGit, SourceID: "g:2", Type: models.TypeReview, Title: "Review", Timestamp: time.Date(2026, 3, 27, 11, 0, 0, 0, time.UTC)})

	activities, _ := db.ListActivities(ActivityFilter{Type: models.TypeReview})
	if len(activities) != 1 {
		t.Fatalf("got %d, want 1", len(activities))
	}
	if activities[0].Title != "Review" {
		t.Errorf("title = %q", activities[0].Title)
	}
}
