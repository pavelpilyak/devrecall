package workitem

import (
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

func mustOpen(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insert(t *testing.T, db *storage.DB, a models.Activity) int64 {
	t.Helper()
	id, err := db.InsertActivity(a)
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	return id
}

func TestExtractIssueKeys(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     []string
	}{
		{"empty", "", nil},
		{"invalid json", "not json", nil},
		{"singular jira", `{"issue_key":"proj-1"}`, []string{"PROJ-1"}},
		{"plural", `{"issue_keys":["ENG-2","ENG-3"]}`, []string{"ENG-2", "ENG-3"}},
		{"both deduped", `{"issue_key":"ENG-2","issue_keys":["eng-2","ENG-3"]}`, []string{"ENG-2", "ENG-3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractIssueKeys(tt.metadata)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
					break
				}
			}
		})
	}
}

func TestMaterialize_CommitBeforeTicket(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	// Sync 1: only a commit referencing PROJ-1 exists.
	commitID := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "repo:aaa111", Type: models.TypeCommit,
		Title: "PROJ-1: start auth fix", Metadata: `{"issue_keys":["PROJ-1"]}`, Timestamp: base,
	})

	if _, err := Materialize(db); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	item, _ := db.GetWorkItemByKey("PROJ-1")
	if item == nil {
		t.Fatal("work item not created from commit")
	}
	if item.Title != "PROJ-1: start auth fix" {
		t.Errorf("commit-derived title = %q", item.Title)
	}
	firstID := item.ID

	// Sync 2: the Jira ticket arrives with real title + transition to Done.
	insert(t, db, models.Activity{
		Source: models.SourceJira, SourceID: "jira:PROJ-1", Type: models.TypeTicket,
		Title: "Fix auth token refresh",
		Metadata: `{"issue_key":"PROJ-1","issue_summary":"Fix auth token refresh","status":"In Progress","url":"https://jira/PROJ-1"}`,
		Timestamp: base.Add(time.Hour),
	})
	insert(t, db, models.Activity{
		Source: models.SourceJira, SourceID: "jira:PROJ-1:changelog:9", Type: models.TypeTicket,
		Title: "PROJ-1 moved to Done",
		Metadata: `{"issue_key":"PROJ-1","from_status":"In Progress","to_status":"Done"}`,
		Timestamp: base.Add(2 * time.Hour),
	})

	if _, err := Materialize(db); err != nil {
		t.Fatalf("Materialize (2nd): %v", err)
	}
	item, _ = db.GetWorkItemByKey("PROJ-1")
	if item == nil {
		t.Fatal("work item vanished")
	}
	if item.ID != firstID {
		t.Errorf("work item ID changed on re-materialize: %d -> %d", firstID, item.ID)
	}
	if item.Title != "Fix auth token refresh" {
		t.Errorf("title = %q, want ticket summary to win over commit subject", item.Title)
	}
	if item.Status != "Done" {
		t.Errorf("status = %q, want Done from latest transition", item.Status)
	}
	if item.URL != "https://jira/PROJ-1" {
		t.Errorf("url = %q", item.URL)
	}
	if !item.StatusChangedAt.Equal(base.Add(2 * time.Hour)) {
		t.Errorf("status_changed_at = %v, want transition timestamp", item.StatusChangedAt)
	}

	timeline, _ := db.ListActivitiesByWorkItem(item.ID, 0)
	if len(timeline) != 3 {
		t.Fatalf("timeline has %d activities, want 3 (commit + ticket + transition)", len(timeline))
	}
	if timeline[0].ID != commitID {
		t.Errorf("timeline should start with the earliest activity (commit)")
	}
}

func TestMaterialize_PRWithKeysLinksCommits(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	commitID := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "myrepo:abc123", Type: models.TypeCommit,
		Title: "implement retry", Timestamp: base, // no issue key in the commit itself
	})
	prID := insert(t, db, models.Activity{
		Source: models.SourceGitHub, SourceID: "github:o/r:pr:7", Type: models.TypePullRequest,
		Title: "Add retry to token refresh",
		Metadata: `{"issue_keys":["PROJ-1"],"commit_shas":["abc123"],"url":"https://github/pr/7"}`,
		Timestamp: base.Add(time.Hour),
	})

	if _, err := Materialize(db); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	item, _ := db.GetWorkItemByKey("PROJ-1")
	if item == nil {
		t.Fatal("PROJ-1 not created")
	}
	if item.Kind != KindTicket {
		t.Errorf("kind = %q, want ticket", item.Kind)
	}
	if item.Title != "Add retry to token refresh" {
		t.Errorf("title = %q, want PR title (no ticket activity exists)", item.Title)
	}

	refs, _ := db.ListActivityWorkItems([]int64{commitID, prID})
	if len(refs[commitID]) != 1 || refs[commitID][0].Key != "PROJ-1" {
		t.Errorf("commit not linked to PROJ-1 via PR SHAs: %v", refs[commitID])
	}
	if len(refs[prID]) != 1 {
		t.Errorf("PR not linked: %v", refs[prID])
	}
}

func TestMaterialize_PRWithoutKeysSynthesized(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	commitID := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "myrepo:def456", Type: models.TypeCommit,
		Title: "bump deps", Timestamp: base,
	})
	prID := insert(t, db, models.Activity{
		Source: models.SourceGitHub, SourceID: "github:o/r:pr:8", Type: models.TypePullRequest,
		Title: "Bump dependencies",
		Metadata: `{"commit_shas":["def456"],"url":"https://github/pr/8"}`,
		Timestamp: base.Add(time.Hour),
	})

	if _, err := Materialize(db); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	key := "pr:github:github:o/r:pr:8"
	item, _ := db.GetWorkItemByKey(key)
	if item == nil {
		t.Fatalf("synthesized PR work item %q not created", key)
	}
	if item.Kind != KindPR {
		t.Errorf("kind = %q, want pr", item.Kind)
	}
	if item.Title != "Bump dependencies" {
		t.Errorf("title = %q", item.Title)
	}

	refs, _ := db.ListActivityWorkItems([]int64{commitID, prID})
	if len(refs[commitID]) != 1 || refs[commitID][0].Key != key {
		t.Errorf("commit not linked to synthesized PR item: %v", refs[commitID])
	}
	if len(refs[prID]) != 1 {
		t.Errorf("PR not self-linked: %v", refs[prID])
	}
}

func TestMaterialize_IdempotentAndCleansStale(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	id := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "repo:aaa", Type: models.TypeCommit,
		Title: "work on PROJ-9", Metadata: `{"issue_keys":["PROJ-9"]}`, Timestamp: base,
	})

	s1, err := Materialize(db)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	s2, err := Materialize(db)
	if err != nil {
		t.Fatalf("Materialize (2nd): %v", err)
	}
	if s1 != s2 {
		t.Errorf("not idempotent: first %+v, second %+v", s1, s2)
	}

	// Metadata correction removes the key — the stale item disappears.
	insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "repo:aaa", Type: models.TypeCommit,
		Title: "work on PROJ-9", Metadata: `{}`, Timestamp: base,
	})
	_ = id
	if _, err := Materialize(db); err != nil {
		t.Fatalf("Materialize (3rd): %v", err)
	}
	item, _ := db.GetWorkItemByKey("PROJ-9")
	if item != nil {
		t.Error("stale work item should be removed after metadata correction")
	}
}
