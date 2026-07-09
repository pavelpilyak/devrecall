package storage

import (
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

func TestInsertEnrichments_UpsertAndFetch(t *testing.T) {
	db := mustOpen(t)
	ts := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	id := insertActivityFull(t, db, models.SourceGit, "repo:abc", models.TypeCommit, "fix bug", ts)

	n, err := db.InsertEnrichments([]Enrichment{
		{ActivityID: id, Digest: "Fixed the auth bug", Tags: []string{"bugfix"}, Model: "ollama/llama3"},
	})
	if err != nil || n != 1 {
		t.Fatalf("InsertEnrichments: n=%d err=%v", n, err)
	}

	// Upsert replaces.
	_, err = db.InsertEnrichments([]Enrichment{
		{ActivityID: id, Digest: "Fixed auth token refresh on 401", Tags: []string{"bugfix", "auth"},
			Entities: `{"systems":["auth"]}`, Model: "ollama/llama3"},
	})
	if err != nil {
		t.Fatalf("InsertEnrichments (upsert): %v", err)
	}

	got, err := db.GetEnrichmentsByActivityIDs([]int64{id})
	if err != nil {
		t.Fatalf("GetEnrichmentsByActivityIDs: %v", err)
	}
	e, ok := got[id]
	if !ok {
		t.Fatal("enrichment not found")
	}
	if e.Digest != "Fixed auth token refresh on 401" {
		t.Errorf("digest = %q", e.Digest)
	}
	if len(e.Tags) != 2 || e.Tags[0] != "bugfix" || e.Tags[1] != "auth" {
		t.Errorf("tags = %v", e.Tags)
	}
	if e.Entities != `{"systems":["auth"]}` {
		t.Errorf("entities = %q", e.Entities)
	}
}

func TestListUnenrichedActivityIDs(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	old := insertActivityFull(t, db, models.SourceGit, "repo:old", models.TypeCommit, "old", base)
	newer := insertActivityFull(t, db, models.SourceGit, "repo:new", models.TypeCommit, "new", base.Add(time.Hour))
	done := insertActivityFull(t, db, models.SourceGit, "repo:done", models.TypeCommit, "done", base.Add(2*time.Hour))

	db.InsertEnrichments([]Enrichment{{ActivityID: done, Digest: "d", Model: EnrichmentModelDeterministic}})

	ids, err := db.ListUnenrichedActivityIDs(0)
	if err != nil {
		t.Fatalf("ListUnenrichedActivityIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != newer || ids[1] != old {
		t.Errorf("ids = %v, want [%d %d] (newest first, enriched excluded)", ids, newer, old)
	}

	ids, _ = db.ListUnenrichedActivityIDs(1)
	if len(ids) != 1 || ids[0] != newer {
		t.Errorf("limited ids = %v, want [%d]", ids, newer)
	}
}

func TestActivityFilter_Tag(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	bug := insertActivityFull(t, db, models.SourceGit, "repo:bug", models.TypeCommit, "fix crash on login", base)
	feat := insertActivityFull(t, db, models.SourceGit, "repo:feat", models.TypeCommit, "add search filters", base.Add(time.Hour))
	bare := insertActivityFull(t, db, models.SourceGit, "repo:bare", models.TypeCommit, "untagged crash work", base.Add(2*time.Hour))
	_ = bare

	db.InsertEnrichments([]Enrichment{
		{ActivityID: bug, Digest: "Fixed login crash", Tags: []string{"bugfix"}, Model: "m"},
		{ActivityID: feat, Digest: "Added search filters", Tags: []string{"feature", "search"}, Model: "m"},
	})

	tests := []struct {
		name string
		tag  string
		want []int64
	}{
		{"single match", "bugfix", []int64{bug}},
		{"second tag in array", "search", []int64{feat}},
		{"uppercase input normalized", "BUGFIX", []int64{bug}},
		{"no match", "docs", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.ListActivities(ActivityFilter{Tag: tt.tag})
			if err != nil {
				t.Fatalf("ListActivities: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d activities, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i].ID != tt.want[i] {
					t.Errorf("got[%d].ID = %d, want %d", i, got[i].ID, tt.want[i])
				}
			}
		})
	}

	// Tag filter also applies to FTS search: "crash" matches two activities,
	// but only the bugfix-tagged one passes.
	matches, err := db.SearchFTS("crash", ActivityFilter{Tag: "bugfix"}, 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(matches) != 1 || matches[0].Activity.ID != bug {
		t.Errorf("FTS with tag filter = %v, want single bugfix match", matches)
	}
}
