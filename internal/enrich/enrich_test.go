package enrich

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// stubProvider returns queued responses in order, then errors.
type stubProvider struct {
	responses []string
	err       error
	calls     int
}

func (s *stubProvider) Chat(ctx context.Context, msgs []llm.Message, opts llm.ChatOpts) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	if len(s.responses) == 0 {
		return "", errors.New("stub: no responses left")
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}

func (s *stubProvider) Name() string { return "stub" }

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
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	}
	id, err := db.InsertActivity(a)
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	return id
}

func TestRun_HappyPath(t *testing.T) {
	db := mustOpen(t)
	id := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit,
		Title: "fix login crash", Content: "handle nil session on 401",
	})

	stub := &stubProvider{responses: []string{
		`[{"n":1,"digest":"Fixed a login crash caused by nil sessions on 401.","tags":["bugfix","Auth"],"entities":{"systems":["auth"]}}]`,
	}}
	stats, err := New(stub).Run(context.Background(), db, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.LLM != 1 || stats.Fallback != 0 {
		t.Errorf("stats = %+v", stats)
	}

	got, _ := db.GetEnrichmentsByActivityIDs([]int64{id})
	e := got[id]
	if e.Digest != "Fixed a login crash caused by nil sessions on 401." {
		t.Errorf("digest = %q", e.Digest)
	}
	if len(e.Tags) != 2 || e.Tags[0] != "bugfix" || e.Tags[1] != "auth" {
		t.Errorf("tags = %v (want lowercased, vocab + free-form)", e.Tags)
	}
	if e.Model != "stub" {
		t.Errorf("model = %q", e.Model)
	}
}

func TestRun_FencedJSONAccepted(t *testing.T) {
	db := mustOpen(t)
	id := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit, Title: "add tests",
	})

	stub := &stubProvider{responses: []string{
		"Here you go:\n```json\n[{\"n\":1,\"digest\":\"Added tests.\",\"tags\":[\"testing\"]}]\n```",
	}}
	if _, err := New(stub).Run(context.Background(), db, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, _ := db.GetEnrichmentsByActivityIDs([]int64{id})
	if got[id].Digest != "Added tests." {
		t.Errorf("digest = %q", got[id].Digest)
	}
}

func TestRun_MissingItemGetsFallback(t *testing.T) {
	db := mustOpen(t)
	id1 := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit, Title: "first",
		Timestamp: time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
	})
	id2 := insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "r:2", Type: models.TypeCommit, Title: "second",
		Timestamp: time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC),
	})

	// Both calls (initial + retry) only answer for one item.
	resp := `[{"n":1,"digest":"Did the second thing.","tags":["feature"]}]`
	stub := &stubProvider{responses: []string{resp, resp}}
	stats, err := New(stub).Run(context.Background(), db, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.LLM != 1 || stats.Fallback != 1 {
		t.Errorf("stats = %+v, want 1 LLM + 1 fallback", stats)
	}

	got, _ := db.GetEnrichmentsByActivityIDs([]int64{id1, id2})
	// Newest first: item n=1 is id2.
	if got[id2].Digest != "Did the second thing." {
		t.Errorf("id2 digest = %q", got[id2].Digest)
	}
	fb := got[id1]
	if fb.Model != storage.EnrichmentModelFallback || fb.Digest != "first" {
		t.Errorf("fallback row = %+v", fb)
	}
	if len(fb.Tags) != 1 || fb.Tags[0] != "commit" {
		t.Errorf("fallback tags = %v", fb.Tags)
	}
}

func TestRun_ProviderErrorLeavesRowsUnenriched(t *testing.T) {
	db := mustOpen(t)
	insert(t, db, models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit, Title: "x",
	})

	stub := &stubProvider{err: errors.New("connection refused")}
	_, err := New(stub).Run(context.Background(), db, Options{})
	if err == nil {
		t.Fatal("want error from failed batch")
	}
	if stub.calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", stub.calls)
	}

	ids, _ := db.ListUnenrichedActivityIDs(0)
	if len(ids) != 1 {
		t.Errorf("activity should remain unenriched for the next sync, got %v", ids)
	}
}

func TestRun_DeterministicPrefills(t *testing.T) {
	db := mustOpen(t)
	transition := insert(t, db, models.Activity{
		Source: models.SourceJira, SourceID: "jira:PROJ-1:changelog:1", Type: models.TypeTicket,
		Title:    "PROJ-1 moved",
		Metadata: `{"issue_key":"PROJ-1","from_status":"In Progress","to_status":"Done"}`,
	})
	note := insert(t, db, models.Activity{
		Source: models.SourceManual, SourceID: "note:1", Type: models.TypeNote,
		Title:    "Decided to keep SQLite",
		Metadata: `{"tags":["Decision","architecture"]}`,
	})

	stub := &stubProvider{} // any LLM call would error: deterministic rows must not reach it
	stats, err := New(stub).Run(context.Background(), db, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Deterministic != 2 || stats.LLM != 0 {
		t.Errorf("stats = %+v, want 2 deterministic", stats)
	}
	if stub.calls != 0 {
		t.Errorf("LLM called %d times for deterministic rows", stub.calls)
	}

	got, _ := db.GetEnrichmentsByActivityIDs([]int64{transition, note})
	tr := got[transition]
	if tr.Digest != "Moved PROJ-1 to Done" {
		t.Errorf("transition digest = %q", tr.Digest)
	}
	if len(tr.Tags) != 1 || tr.Tags[0] != "status-change" {
		t.Errorf("transition tags = %v", tr.Tags)
	}
	nt := got[note]
	if nt.Digest != "Decided to keep SQLite" {
		t.Errorf("note digest = %q", nt.Digest)
	}
	if len(nt.Tags) != 2 || nt.Tags[0] != "decision" || nt.Tags[1] != "architecture" {
		t.Errorf("note tags = %v (want manual tags copied, lowercased)", nt.Tags)
	}
}

func TestRun_MaxPerRunCap(t *testing.T) {
	db := mustOpen(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		insert(t, db, models.Activity{
			Source: models.SourceGit, SourceID: "r:" + string(rune('a'+i)), Type: models.TypeCommit,
			Title: "c", Timestamp: base.Add(time.Duration(i) * time.Hour),
		})
	}

	stub := &stubProvider{responses: []string{`[{"n":1,"digest":"Did c.","tags":["feature"]}]`}}
	stats, err := New(stub).Run(context.Background(), db, Options{MaxPerRun: 1, BatchPerCall: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.LLM != 1 {
		t.Errorf("stats = %+v", stats)
	}
	ids, _ := db.ListUnenrichedActivityIDs(0)
	if len(ids) != 2 {
		t.Errorf("%d rows remain, want 2 (cap respected)", len(ids))
	}
}

func TestClampTags(t *testing.T) {
	got := clampTags([]string{"Bugfix", "auth", "very-long-free-form-tag-over-the-limit-aaaa", "infra", "extra-free", "bugfix", ""})
	// bugfix (vocab), auth + infra (2 free-form), extra-free rejected (cap), long + dup + empty rejected.
	want := []string{"bugfix", "auth", "infra"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}
