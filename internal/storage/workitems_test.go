package storage

import (
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

func insertActivityFull(t *testing.T, db *DB, source models.Source, sourceID string, typ models.ActivityType, title string, ts time.Time) int64 {
	t.Helper()
	id, err := db.InsertActivity(models.Activity{
		Source: source, SourceID: sourceID, Type: typ, Title: title, Timestamp: ts,
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	return id
}

func TestReplaceWorkItems_UpsertByKey(t *testing.T) {
	db := mustOpen(t)
	ts := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	// First pass: commit-derived work item with a poor title.
	err := db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", Title: "fix: handle 401", FirstSeen: ts, LastSeen: ts},
	}, nil)
	if err != nil {
		t.Fatalf("ReplaceWorkItems: %v", err)
	}

	first, err := db.GetWorkItemByKey("PROJ-1")
	if err != nil || first == nil {
		t.Fatalf("GetWorkItemByKey: %v, item=%v", err, first)
	}

	// Second pass: the Jira ticket arrived — same key, better title + status.
	err = db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", Title: "Fix auth token refresh", Status: "Done",
			StatusChangedAt: ts.Add(time.Hour), URL: "https://jira/PROJ-1",
			FirstSeen: ts, LastSeen: ts.Add(time.Hour)},
	}, nil)
	if err != nil {
		t.Fatalf("ReplaceWorkItems (2nd): %v", err)
	}

	second, err := db.GetWorkItemByKey("PROJ-1")
	if err != nil || second == nil {
		t.Fatalf("GetWorkItemByKey: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert changed work item ID: %d -> %d", first.ID, second.ID)
	}
	if second.Title != "Fix auth token refresh" {
		t.Errorf("title = %q, want upgraded title", second.Title)
	}
	if second.Status != "Done" {
		t.Errorf("status = %q, want Done", second.Status)
	}
	if second.URL != "https://jira/PROJ-1" {
		t.Errorf("url = %q", second.URL)
	}
	if second.StatusChangedAt.IsZero() {
		t.Error("status_changed_at not persisted")
	}
}

func TestReplaceWorkItems_StaleItemsDeleted(t *testing.T) {
	db := mustOpen(t)
	ts := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", FirstSeen: ts, LastSeen: ts},
		{Key: "PROJ-2", Kind: "ticket", FirstSeen: ts, LastSeen: ts},
	}, nil)

	// Next recompute no longer includes PROJ-2.
	db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", FirstSeen: ts, LastSeen: ts},
	}, nil)

	gone, err := db.GetWorkItemByKey("PROJ-2")
	if err != nil {
		t.Fatalf("GetWorkItemByKey: %v", err)
	}
	if gone != nil {
		t.Error("stale work item PROJ-2 should be deleted")
	}
}

func TestReplaceWorkItems_LinksAndTimeline(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	commitID := insertActivityFull(t, db, models.SourceGit, "repo:abc", models.TypeCommit, "fix bug", base)
	prID := insertActivityFull(t, db, models.SourceGitHub, "pr:owner/repo:5", models.TypePullRequest, "Fix the bug", base.Add(time.Hour))
	ticketID := insertActivityFull(t, db, models.SourceJira, "PROJ-1", models.TypeTicket, "Bug ticket", base.Add(-time.Hour))

	err := db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", Title: "Bug ticket", FirstSeen: base.Add(-time.Hour), LastSeen: base.Add(time.Hour)},
	}, []WorkItemLink{
		{ActivityID: commitID, Key: "PROJ-1", LinkKind: "issue_key"},
		{ActivityID: prID, Key: "PROJ-1", LinkKind: "issue_key"},
		{ActivityID: ticketID, Key: "PROJ-1", LinkKind: "self"},
	})
	if err != nil {
		t.Fatalf("ReplaceWorkItems: %v", err)
	}

	item, _ := db.GetWorkItemByKey("PROJ-1")
	if item == nil {
		t.Fatal("work item not found")
	}

	timeline, err := db.ListActivitiesByWorkItem(item.ID, 0)
	if err != nil {
		t.Fatalf("ListActivitiesByWorkItem: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("timeline has %d activities, want 3", len(timeline))
	}
	// Chronological: ticket, commit, PR.
	if timeline[0].ID != ticketID || timeline[1].ID != commitID || timeline[2].ID != prID {
		t.Errorf("timeline order = [%d %d %d], want [%d %d %d]",
			timeline[0].ID, timeline[1].ID, timeline[2].ID, ticketID, commitID, prID)
	}

	refs, err := db.ListActivityWorkItems([]int64{commitID, prID})
	if err != nil {
		t.Fatalf("ListActivityWorkItems: %v", err)
	}
	if len(refs[commitID]) != 1 || refs[commitID][0].Key != "PROJ-1" {
		t.Errorf("commit refs = %v, want PROJ-1", refs[commitID])
	}
	if len(refs[prID]) != 1 {
		t.Errorf("pr refs = %v", refs[prID])
	}

	// Re-run with no links: links are recomputed away, item without links stays.
	err = db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", FirstSeen: base, LastSeen: base},
	}, nil)
	if err != nil {
		t.Fatalf("ReplaceWorkItems (relink): %v", err)
	}
	refs, _ = db.ListActivityWorkItems([]int64{commitID})
	if len(refs) != 0 {
		t.Errorf("links should be cleared on recompute, got %v", refs)
	}
}

func TestListWorkItems_Filters(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", Title: "Auth refresh", Status: "Done",
			FirstSeen: base, LastSeen: base.AddDate(0, 0, 2)},
		{Key: "PROJ-2", Kind: "ticket", Title: "Search rework", Status: "In Progress",
			FirstSeen: base.AddDate(0, 0, 5), LastSeen: base.AddDate(0, 0, 6)},
		{Key: "pr:github:pr:o/r:9", Kind: "pr", Title: "Bump deps",
			FirstSeen: base.AddDate(0, 0, 10), LastSeen: base.AddDate(0, 0, 10)},
	}, nil)

	tests := []struct {
		name   string
		filter WorkItemFilter
		want   []string
	}{
		{"all newest-active first", WorkItemFilter{}, []string{"pr:github:pr:o/r:9", "PROJ-2", "PROJ-1"}},
		{"by status", WorkItemFilter{Status: "done"}, []string{"PROJ-1"}},
		{"by query on title", WorkItemFilter{Query: "search"}, []string{"PROJ-2"}},
		{"by query on key", WorkItemFilter{Query: "PROJ"}, []string{"PROJ-2", "PROJ-1"}},
		{"active window", WorkItemFilter{After: base.AddDate(0, 0, 4), Before: base.AddDate(0, 0, 8)}, []string{"PROJ-2"}},
		{"limit", WorkItemFilter{Limit: 1}, []string{"pr:github:pr:o/r:9"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := db.ListWorkItems(tt.filter)
			if err != nil {
				t.Fatalf("ListWorkItems: %v", err)
			}
			var keys []string
			for _, w := range items {
				keys = append(keys, w.Key)
			}
			if len(keys) != len(tt.want) {
				t.Fatalf("got keys %v, want %v", keys, tt.want)
			}
			for i := range keys {
				if keys[i] != tt.want[i] {
					t.Errorf("got keys %v, want %v", keys, tt.want)
					break
				}
			}
		})
	}
}

func TestActivityWorkItems_CascadeOnActivityDelete(t *testing.T) {
	db := mustOpen(t)
	ts := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	id := insertActivityFull(t, db, models.SourceGit, "repo:abc", models.TypeCommit, "fix", ts)
	db.ReplaceWorkItems([]models.WorkItem{
		{Key: "PROJ-1", Kind: "ticket", FirstSeen: ts, LastSeen: ts},
	}, []WorkItemLink{{ActivityID: id, Key: "PROJ-1", LinkKind: "issue_key"}})

	if _, err := db.Exec("DELETE FROM activities WHERE id = ?", id); err != nil {
		t.Fatalf("delete activity: %v", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM activity_work_items").Scan(&count)
	if count != 0 {
		t.Errorf("links not cascaded on activity delete: %d rows remain", count)
	}
}
