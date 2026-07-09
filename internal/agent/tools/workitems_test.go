package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/internal/workitem"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// seedWorkItems inserts a linked ticket+commit+PR cluster and materializes
// work items, returning the registry and the activity IDs.
func seedWorkItems(t *testing.T) (*Registry, *storage.DB, map[string]int64) {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	base := time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC)
	ids := map[string]int64{}
	mk := func(name string, a models.Activity) {
		id, err := db.InsertActivity(a)
		if err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		ids[name] = id
	}

	mk("ticket", models.Activity{
		Source: models.SourceJira, SourceID: "jira:PROJ-7", Type: models.TypeTicket,
		Title:    "Fix payment retry",
		Metadata: `{"issue_key":"PROJ-7","issue_summary":"Fix payment retry","status":"In Review","url":"https://jira/PROJ-7"}`, Timestamp: base,
	})
	mk("commit", models.Activity{
		Source: models.SourceGit, SourceID: "api:abc123", Type: models.TypeCommit,
		Title: "retry loop fix", Timestamp: base.Add(time.Hour),
	})
	mk("pr", models.Activity{
		Source: models.SourceGitHub, SourceID: "github:o/api:pr:42", Type: models.TypePullRequest,
		Title:    "Fix payment retry PR",
		Metadata: `{"issue_keys":["PROJ-7"],"commit_shas":["abc123"],"url":"https://github/pr/42"}`, Timestamp: base.Add(2 * time.Hour),
	})
	mk("loose", models.Activity{
		Source: models.SourceGit, SourceID: "api:zzz999", Type: models.TypeCommit,
		Title: "unrelated typo fix", Timestamp: base.Add(3 * time.Hour),
	})

	if _, err := workitem.Materialize(db); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := db.InsertEnrichments([]storage.Enrichment{
		{ActivityID: ids["commit"], Digest: "Fixed the payment retry loop.", Tags: []string{"bugfix"}, Model: "m"},
	}); err != nil {
		t.Fatalf("InsertEnrichments: %v", err)
	}

	reg := NewRegistry(Deps{DB: db, Now: fixedNow(base.AddDate(0, 0, 2))})
	return reg, db, ids
}

func execTool(t *testing.T, reg *Registry, name, args string) json.RawMessage {
	t.Helper()
	raw, err := reg.Execute(context.Background(), name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return raw
}

func TestGetWorkItem_Timeline(t *testing.T) {
	reg, _, ids := seedWorkItems(t)

	var res struct {
		Found    bool         `json:"found"`
		WorkItem workItemView `json:"work_item"`
		Timeline []struct {
			ID     int64    `json:"id"`
			Digest string   `json:"digest"`
			Tags   []string `json:"tags"`
		} `json:"timeline"`
	}
	decodeResult(t, execTool(t, reg, "get_work_item", `{"key":"proj-7"}`), &res)

	if !res.Found {
		t.Fatal("work item not found")
	}
	if res.WorkItem.Title != "Fix payment retry" || res.WorkItem.Status != "In Review" {
		t.Errorf("work item = %+v", res.WorkItem)
	}
	if len(res.Timeline) != 3 {
		t.Fatalf("timeline has %d entries, want 3 (ticket+commit+PR)", len(res.Timeline))
	}
	// Chronological, with enrichment annotations on the commit.
	if res.Timeline[0].ID != ids["ticket"] || res.Timeline[1].ID != ids["commit"] || res.Timeline[2].ID != ids["pr"] {
		t.Errorf("timeline order = %+v", res.Timeline)
	}
	if res.Timeline[1].Digest != "Fixed the payment retry loop." {
		t.Errorf("commit digest = %q", res.Timeline[1].Digest)
	}
}

func TestGetWorkItem_NotFound(t *testing.T) {
	reg, _, _ := seedWorkItems(t)
	var res struct {
		Found bool `json:"found"`
	}
	decodeResult(t, execTool(t, reg, "get_work_item", `{"key":"NOPE-1"}`), &res)
	if res.Found {
		t.Error("expected found=false")
	}
}

func TestListWorkItems(t *testing.T) {
	reg, _, _ := seedWorkItems(t)

	var res struct {
		WorkItems []workItemView `json:"work_items"`
		Count     int            `json:"count"`
	}
	decodeResult(t, execTool(t, reg, "list_work_items", `{}`), &res)
	if res.Count != 1 || res.WorkItems[0].Key != "PROJ-7" {
		t.Errorf("work items = %+v", res.WorkItems)
	}

	decodeResult(t, execTool(t, reg, "list_work_items", `{"status":"in review"}`), &res)
	if res.Count != 1 {
		t.Errorf("status filter: count = %d, want 1", res.Count)
	}
	decodeResult(t, execTool(t, reg, "list_work_items", `{"query":"payment"}`), &res)
	if res.Count != 1 {
		t.Errorf("query filter: count = %d, want 1", res.Count)
	}
	decodeResult(t, execTool(t, reg, "list_work_items", `{"status":"done"}`), &res)
	if res.Count != 0 {
		t.Errorf("non-matching status: count = %d, want 0", res.Count)
	}
}

func TestGetRelatedActivities_UsesWorkItemLinks(t *testing.T) {
	reg, _, ids := seedWorkItems(t)

	// The commit has no issue keys in its own metadata — only the
	// PR-commit SHA link connects it to PROJ-7. The old key-extraction
	// path would return nothing here.
	var res struct {
		Keys    []string `json:"keys"`
		Related []struct {
			ID int64 `json:"id"`
		} `json:"related"`
	}
	args, _ := json.Marshal(map[string]any{"id": ids["commit"]})
	decodeResult(t, execTool(t, reg, "get_related_activities", string(args)), &res)

	if len(res.Keys) != 1 || res.Keys[0] != "PROJ-7" {
		t.Fatalf("keys = %v", res.Keys)
	}
	got := map[int64]bool{}
	for _, r := range res.Related {
		got[r.ID] = true
	}
	if !got[ids["ticket"]] || !got[ids["pr"]] {
		t.Errorf("related should include ticket and PR, got %+v", res.Related)
	}
	if got[ids["commit"]] {
		t.Errorf("related should exclude the source activity itself")
	}
	if got[ids["loose"]] {
		t.Errorf("unrelated activity leaked into results")
	}
}

func TestListActivities_TagFilter(t *testing.T) {
	reg, _, ids := seedWorkItems(t)

	var res struct {
		Activities []struct {
			ID        int64    `json:"id"`
			Tags      []string `json:"tags"`
			WorkItems []string `json:"work_items"`
		} `json:"activities"`
	}
	decodeResult(t, execTool(t, reg, "list_activities", `{"tag":"bugfix"}`), &res)

	if len(res.Activities) != 1 || res.Activities[0].ID != ids["commit"] {
		t.Fatalf("tag filter results = %+v", res.Activities)
	}
	if len(res.Activities[0].WorkItems) != 1 || res.Activities[0].WorkItems[0] != "PROJ-7" {
		t.Errorf("work_items annotation = %v", res.Activities[0].WorkItems)
	}
}
