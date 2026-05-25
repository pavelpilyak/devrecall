package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// fixedNow returns a deterministic now func.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

// newTestDB opens an in-memory SQLite and seeds a small fixture set
// covering several sources, types, identities, and a pre-built summary.
func newTestDB(t *testing.T) (*storage.DB, map[string]int64) {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Two identities: self + a teammate.
	selfID, err := db.InsertIdentity("Pavel", "pavel@example.com", true)
	if err != nil {
		t.Fatalf("insert self: %v", err)
	}
	annaID, err := db.InsertIdentity("Anna Smith", "anna@example.com", false)
	if err != nil {
		t.Fatalf("insert anna: %v", err)
	}

	mk := func(src models.Source, srcID string, typ models.ActivityType, title, content string, ts time.Time, ident int64) {
		_, err := db.InsertActivity(models.Activity{
			Source:     src,
			SourceID:   srcID,
			IdentityID: ident,
			Type:       typ,
			Title:      title,
			Content:    content,
			Timestamp:  ts,
		})
		if err != nil {
			t.Fatalf("insert %s: %v", srcID, err)
		}
	}

	// Activities span 2026-04-05 → 2026-04-08.
	mk(models.SourceGit, "repo:abc1", models.TypeCommit, "Fix payment retry logic", "Address race condition in retry loop", time.Date(2026, 4, 5, 9, 0, 0, 0, time.UTC), selfID)
	mk(models.SourceGit, "repo:abc2", models.TypeCommit, "Add retry tests", "", time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC), selfID)
	mk(models.SourceSlack, "C1:1700000001.0001", models.TypeMessage, "Deploy decision", "Decided to switch to blue-green for the payment service", time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC), selfID)
	mk(models.SourceGitHub, "octo/api#423", models.TypePullRequest, "Payment retries PR", "", time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC), selfID)
	mk(models.SourceGitHub, "octo/api#424:review", models.TypeReview, "Reviewed notification PR", "", time.Date(2026, 4, 8, 9, 30, 0, 0, time.UTC), annaID)

	// Pre-built summary for the week.
	if _, err := db.InsertSummary(models.Summary{
		PeriodType:    "weekly",
		PeriodStart:   "2026-04-06",
		PeriodEnd:     "2026-04-13",
		SummaryText:   "Worked on payment retries and blue-green deploy.",
		ActivityCount: 4,
	}); err != nil {
		t.Fatalf("insert summary: %v", err)
	}

	return db, map[string]int64{"self": selfID, "anna": annaID}
}

func newTestRegistry(t *testing.T) (*Registry, *storage.DB, map[string]int64) {
	db, ids := newTestDB(t)
	reg := NewRegistry(Deps{
		DB:  db,
		Now: fixedNow(time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)),
	})
	return reg, db, ids
}

func decodeResult(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode result: %v\nraw=%s", err, string(raw))
	}
}

// ─── registry plumbing ────────────────────────────────────────────────────────

func TestRegistry_Names(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	want := []string{
		"current_time",
		"list_activities",
		"count_activities",
		"search_activities",
		"semantic_search_activities",
		"get_activity",
		"get_related_activities",
		"who_worked_on",
		"recent_decisions",
		"prep_meeting",
		"log_event",
		"list_summaries",
		"get_summary",
		"list_identities",
		"resolve_person",
	}
	got := reg.Names()
	if len(got) != len(want) {
		t.Fatalf("registered %d tools, want %d (%v)", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("[%d] = %q, want %q", i, got[i], n)
		}
	}
}

func TestRegistry_LLMTools_AllValidSchemas(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	for _, t2 := range reg.LLMTools() {
		var probe map[string]any
		if err := json.Unmarshal(t2.Schema, &probe); err != nil {
			t.Errorf("%s: schema not valid JSON: %v", t2.Name, err)
		}
		if probe["type"] != "object" {
			t.Errorf("%s: schema type = %v, want object", t2.Name, probe["type"])
		}
		if t2.Description == "" {
			t.Errorf("%s: empty description", t2.Name)
		}
	}
}

func TestRegistry_Execute_UnknownTool(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── current_time ─────────────────────────────────────────────────────────────

func TestCurrentTime(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "current_time", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Now      string `json:"now"`
		Timezone string `json:"timezone"`
	}
	decodeResult(t, out, &got)
	if got.Now != "2026-04-08T12:00:00Z" {
		t.Errorf("now = %q", got.Now)
	}
	if got.Timezone != "UTC" {
		t.Errorf("tz = %q", got.Timezone)
	}
}

// ─── list_activities ──────────────────────────────────────────────────────────

func TestListActivities_DateRangeAndSourceFilter(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	args := json.RawMessage(`{"start":"2026-04-07","end":"2026-04-09","source":"github"}`)
	out, err := reg.Execute(context.Background(), "list_activities", args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Activities []activitySummary `json:"activities"`
		Count      int               `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count != 2 {
		t.Fatalf("count = %d, want 2 (PR + review)", got.Count)
	}
	for _, a := range got.Activities {
		if a.Source != "github" {
			t.Errorf("source = %q", a.Source)
		}
	}
}

func TestListActivities_LimitAndOffset(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "list_activities", json.RawMessage(`{"limit":2}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Activities []activitySummary `json:"activities"`
		Count      int               `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count != 2 {
		t.Errorf("limit count = %d, want 2", got.Count)
	}
	// Offset by 2 — skip the first two newest.
	out, err = reg.Execute(context.Background(), "list_activities", json.RawMessage(`{"limit":2,"offset":2}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got2 struct {
		Activities []activitySummary `json:"activities"`
	}
	decodeResult(t, out, &got2)
	if len(got2.Activities) == 0 {
		t.Fatal("offset returned nothing")
	}
	if got.Activities[0].ID == got2.Activities[0].ID {
		t.Errorf("offset did not advance: both started at id %d", got.Activities[0].ID)
	}
}

func TestListActivities_BadDate(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "list_activities", json.RawMessage(`{"start":"not-a-date"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── count_activities ─────────────────────────────────────────────────────────

func TestCountActivities_TotalAndGroups(t *testing.T) {
	reg, _, _ := newTestRegistry(t)

	cases := []struct {
		name        string
		args        string
		wantTotal   int
		wantGroupBy string
		wantBuckets map[string]int
	}{
		{
			name:      "no group",
			args:      `{}`,
			wantTotal: 5,
		},
		{
			name:        "by source",
			args:        `{"group_by":"source"}`,
			wantTotal:   5,
			wantGroupBy: "source",
			wantBuckets: map[string]int{"git": 2, "slack": 1, "github": 2},
		},
		{
			name:        "by type",
			args:        `{"group_by":"type"}`,
			wantTotal:   5,
			wantGroupBy: "type",
			wantBuckets: map[string]int{"commit": 2, "message": 1, "pull_request": 1, "review": 1},
		},
		{
			name:        "by day in window",
			args:        `{"group_by":"day","start":"2026-04-07","end":"2026-04-09"}`,
			wantTotal:   3,
			wantGroupBy: "day",
			wantBuckets: map[string]int{"2026-04-07": 2, "2026-04-08": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := reg.Execute(context.Background(), "count_activities", json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			var got struct {
				Total     int            `json:"total"`
				GroupBy   string         `json:"group_by"`
				Breakdown map[string]int `json:"breakdown"`
			}
			decodeResult(t, out, &got)
			if got.Total != tc.wantTotal {
				t.Errorf("total = %d, want %d", got.Total, tc.wantTotal)
			}
			if got.GroupBy != tc.wantGroupBy {
				t.Errorf("group_by = %q, want %q", got.GroupBy, tc.wantGroupBy)
			}
			for k, v := range tc.wantBuckets {
				if got.Breakdown[k] != v {
					t.Errorf("breakdown[%q] = %d, want %d (full=%v)", k, got.Breakdown[k], v, got.Breakdown)
				}
			}
		})
	}
}

func TestCountActivities_BadGroup(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "count_activities", json.RawMessage(`{"group_by":"galaxy"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── search_activities ────────────────────────────────────────────────────────

func TestSearchActivities_FTS(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "search_activities", json.RawMessage(`{"query":"deploy"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Activities []activitySummary `json:"activities"`
		Count      int               `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count == 0 {
		t.Fatal("expected at least one match for 'deploy'")
	}
	found := false
	for _, a := range got.Activities {
		if strings.Contains(strings.ToLower(a.Title), "deploy") {
			found = true
		}
	}
	if !found {
		t.Errorf("no result with 'deploy' in title: %+v", got.Activities)
	}
}

func TestSearchActivities_RequiresQuery(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "search_activities", json.RawMessage(`{"query":"  "}`))
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

// ─── semantic_search_activities ──────────────────────────────────────────────

// stubEmbedder returns a fixed vector — enough to exercise the wiring even
// though the in-memory DB has no embeddings stored.
type stubEmbedder struct {
	dims int
	err  error
}

func (s *stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	v := make([]float32, s.dims)
	for i := range v {
		v[i] = 0.1
	}
	return v, nil
}
func (s *stubEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil
}
func (s *stubEmbedder) Dimensions() int { return s.dims }
func (s *stubEmbedder) Name() string    { return "stub" }

func TestSemanticSearch_NoEmbedder(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "semantic_search_activities", json.RawMessage(`{"query":"latency"}`))
	if err == nil {
		t.Fatal("expected error when no embedder configured")
	}
}

func TestSemanticSearch_WithEmbedder(t *testing.T) {
	db, _ := newTestDB(t)
	reg := NewRegistry(Deps{
		DB:       db,
		Embedder: &stubEmbedder{dims: 384},
	})
	out, err := reg.Execute(context.Background(), "semantic_search_activities", json.RawMessage(`{"query":"latency"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// vec_activities is empty, so result is an empty list — but the call
	// must succeed and produce a count field.
	var got struct {
		Activities []any `json:"activities"`
		Count      int   `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count != 0 {
		t.Errorf("count = %d, want 0 (no embeddings stored)", got.Count)
	}
}

// ─── get_activity ─────────────────────────────────────────────────────────────

func TestGetActivity(t *testing.T) {
	reg, db, _ := newTestRegistry(t)

	rows, err := db.ListActivities(storage.ActivityFilter{Source: models.SourceGit})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no git activities in fixture")
	}
	id := rows[0].ID

	args, _ := json.Marshal(map[string]any{"id": id})
	out, err := reg.Execute(context.Background(), "get_activity", args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Activity *models.Activity `json:"activity"`
	}
	decodeResult(t, out, &got)
	if got.Activity == nil || got.Activity.ID != id {
		t.Fatalf("activity = %+v, want id %d", got.Activity, id)
	}
	if got.Activity.Content == "" && rows[0].Content != "" {
		t.Errorf("get_activity should include content; got %q", got.Activity.Content)
	}
}

func TestGetActivity_Missing(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "get_activity", json.RawMessage(`{"id":99999}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Activity *models.Activity `json:"activity"`
	}
	decodeResult(t, out, &got)
	if got.Activity != nil {
		t.Errorf("expected nil, got %+v", got.Activity)
	}
}

func TestGetActivity_RequiresID(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "get_activity", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── list_summaries / get_summary ────────────────────────────────────────────

func TestListSummaries(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "list_summaries", json.RawMessage(`{"period_type":"weekly"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Summaries []models.Summary `json:"summaries"`
		Count     int              `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count != 1 {
		t.Errorf("count = %d, want 1", got.Count)
	}
}

func TestGetSummary(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "get_summary", json.RawMessage(`{"period_type":"weekly","period_start":"2026-04-06"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Summary *models.Summary `json:"summary"`
	}
	decodeResult(t, out, &got)
	if got.Summary == nil || got.Summary.PeriodStart != "2026-04-06" {
		t.Fatalf("summary = %+v", got.Summary)
	}
}

func TestGetSummary_Missing(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "get_summary", json.RawMessage(`{"period_type":"daily","period_start":"1999-01-01"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Summary *models.Summary `json:"summary"`
	}
	decodeResult(t, out, &got)
	if got.Summary != nil {
		t.Errorf("expected nil, got %+v", got.Summary)
	}
}

// ─── list_identities / resolve_person ────────────────────────────────────────

func TestListIdentities(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	out, err := reg.Execute(context.Background(), "list_identities", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Identities []models.Identity `json:"identities"`
		Count      int               `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count != 2 {
		t.Errorf("count = %d, want 2", got.Count)
	}

	out, err = reg.Execute(context.Background(), "list_identities", json.RawMessage(`{"query":"anna"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	decodeResult(t, out, &got)
	if got.Count != 1 || !strings.Contains(strings.ToLower(got.Identities[0].Name), "anna") {
		t.Errorf("anna filter = %+v", got)
	}
}

func TestResolvePerson(t *testing.T) {
	reg, _, ids := newTestRegistry(t)

	cases := []struct {
		name   string
		needle string
		wantID int64
	}{
		{"by email", "anna@example.com", ids["anna"]},
		{"by name substring", "Anna", ids["anna"]},
		{"by self email", "pavel@example.com", ids["self"]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"name_or_email": tc.needle})
			out, err := reg.Execute(context.Background(), "resolve_person", args)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			var got struct {
				Identity *models.Identity `json:"identity"`
			}
			decodeResult(t, out, &got)
			if got.Identity == nil || got.Identity.ID != tc.wantID {
				t.Fatalf("identity = %+v, want id %d", got.Identity, tc.wantID)
			}
		})
	}

	// Unknown → null.
	out, err := reg.Execute(context.Background(), "resolve_person", json.RawMessage(`{"name_or_email":"ghost"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Identity *models.Identity `json:"identity"`
	}
	decodeResult(t, out, &got)
	if got.Identity != nil {
		t.Errorf("expected nil for unknown person, got %+v", got.Identity)
	}
}

func TestGetRelatedActivities_LinksAcrossSourcesByIssueKey(t *testing.T) {
	reg, db, _ := newTestRegistry(t)

	// Source activity: a Jira ticket carrying metadata.issue_key.
	jiraID, err := db.InsertActivity(models.Activity{
		Source:    models.SourceJira,
		SourceID:  "jira:PROJ-1",
		Type:      models.TypeTicket,
		Title:     "PROJ-1: auth retry",
		Metadata:  `{"issue_key":"PROJ-1","url":"https://x/PROJ-1"}`,
		Timestamp: time.Date(2026, 4, 5, 8, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("insert jira: %v", err)
	}
	// Git commit carrying the same key in metadata.issue_keys (array).
	if _, err := db.InsertActivity(models.Activity{
		Source:    models.SourceGit,
		SourceID:  "/r:sha1",
		Type:      models.TypeCommit,
		Title:     "Fix retry",
		Metadata:  `{"sha":"sha1","issue_keys":["PROJ-1"]}`,
		Timestamp: time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("insert git: %v", err)
	}
	// GitHub PR also referencing PROJ-1.
	if _, err := db.InsertActivity(models.Activity{
		Source:    models.SourceGitHub,
		SourceID:  "octo/api#1",
		Type:      models.TypePullRequest,
		Title:     "PR: PROJ-1 retry fix",
		Metadata:  `{"pr_number":1,"issue_keys":["PROJ-1"]}`,
		Timestamp: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("insert pr: %v", err)
	}
	// Unrelated commit on a different ticket — should not appear in results.
	if _, err := db.InsertActivity(models.Activity{
		Source:    models.SourceGit,
		SourceID:  "/r:sha2",
		Type:      models.TypeCommit,
		Title:     "Unrelated",
		Metadata:  `{"sha":"sha2","issue_keys":["OTHER-9"]}`,
		Timestamp: time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("insert unrelated: %v", err)
	}

	args := json.RawMessage(`{"id":` + strconv.FormatInt(jiraID, 10) + `}`)
	out, err := reg.Execute(context.Background(), "get_related_activities", args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var got struct {
		Keys    []string          `json:"keys"`
		Related []activitySummary `json:"related"`
	}
	decodeResult(t, out, &got)

	if len(got.Keys) != 1 || got.Keys[0] != "PROJ-1" {
		t.Errorf("keys = %v, want [PROJ-1]", got.Keys)
	}
	if len(got.Related) != 2 {
		t.Fatalf("related = %d, want 2 (git + github), got %+v", len(got.Related), got.Related)
	}
	seen := map[string]bool{}
	for _, r := range got.Related {
		seen[r.Source] = true
		if r.ID == jiraID {
			t.Errorf("source activity should be excluded from related")
		}
	}
	if !seen["git"] || !seen["github"] {
		t.Errorf("expected both git and github in related; got %+v", seen)
	}
}

func TestListActivities_HasMoreAndHint(t *testing.T) {
	reg, db, ids := newTestRegistry(t)
	selfID := ids["self"]

	// Add enough rows to exceed limit=2.
	for i := 0; i < 5; i++ {
		_, err := db.InsertActivity(models.Activity{
			Source:     models.SourceGit,
			SourceID:   "/r:bulk" + strconv.Itoa(i),
			IdentityID: selfID,
			Type:       models.TypeCommit,
			Title:      "bulk " + strconv.Itoa(i),
			Timestamp:  time.Date(2026, 4, 8, i, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("insert bulk %d: %v", i, err)
		}
	}

	out, err := reg.Execute(context.Background(), "list_activities", json.RawMessage(`{"limit":2}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Activities []activitySummary `json:"activities"`
		Count      int               `json:"count"`
		HasMore    bool              `json:"has_more"`
		Hint       string            `json:"hint"`
	}
	decodeResult(t, out, &got)

	if got.Count != 2 {
		t.Errorf("count = %d, want 2", got.Count)
	}
	if !got.HasMore {
		t.Errorf("expected has_more=true with 2-of-many")
	}
	if !strings.Contains(got.Hint, "offset=2") {
		t.Errorf("hint missing offset suggestion: %q", got.Hint)
	}
}

func TestClampLimit_RespectsHardCap(t *testing.T) {
	if got := clampLimit(0, 50); got != 50 {
		t.Errorf("zero → default: got %d", got)
	}
	if got := clampLimit(-3, 50); got != 50 {
		t.Errorf("negative → default: got %d", got)
	}
	if got := clampLimit(10000, 50); got != maxToolLimit {
		t.Errorf("over cap → maxToolLimit: got %d", got)
	}
	if got := clampLimit(20, 50); got != 20 {
		t.Errorf("under cap → user value: got %d", got)
	}
}

func TestResolvePerson_Empty(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "resolve_person", json.RawMessage(`{"name_or_email":"   "}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLogEvent_InsertsManualNote(t *testing.T) {
	reg, db, _ := newTestRegistry(t)

	out, err := reg.Execute(context.Background(), "log_event", json.RawMessage(`{
		"text":"Decided to switch to ULIDs",
		"tags":["arch","decision"],
		"people":["anna"]
	}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got logEventResult
	decodeResult(t, out, &got)
	if got.ActivityID <= 0 {
		t.Errorf("expected positive id, got %d", got.ActivityID)
	}
	if got.Title != "Decided to switch to ULIDs" {
		t.Errorf("title = %q", got.Title)
	}

	// Confirm the row landed in the DB.
	rows, err := db.GetActivitiesByIDs([]int64{got.ActivityID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("readback: %v / rows=%d", err, len(rows))
	}
	if rows[0].Source != models.SourceManual || rows[0].Type != models.TypeNote {
		t.Errorf("source/type = %s/%s, want manual/note", rows[0].Source, rows[0].Type)
	}
	if !strings.Contains(rows[0].Metadata, `"tags":["arch","decision"]`) {
		t.Errorf("metadata missing tags: %q", rows[0].Metadata)
	}
}

func TestLogEvent_EmptyTextRejected(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "log_event", json.RawMessage(`{"text":"   "}`))
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestWhoWorkedOn_FiltersByIdentity(t *testing.T) {
	reg, _, ids := newTestRegistry(t)
	selfID := ids["self"]
	annaID := ids["anna"]

	// Fixture inserts mix self/anna activities; who_worked_on(anna) should
	// return only anna's rows.
	out, err := reg.Execute(context.Background(), "who_worked_on", json.RawMessage(
		`{"identity_id":`+strconv.FormatInt(annaID, 10)+`}`,
	))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		IdentityID int64             `json:"identity_id"`
		Activities []activitySummary `json:"activities"`
		Count      int               `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count == 0 {
		t.Fatalf("expected anna activities, got 0")
	}
	for _, a := range got.Activities {
		if a.IdentityID != annaID {
			t.Errorf("returned activity %d has identity %d, want %d (anna)", a.ID, a.IdentityID, annaID)
		}
	}
	if got.IdentityID != annaID {
		t.Errorf("identity_id = %d, want %d", got.IdentityID, annaID)
	}
	_ = selfID
}

func TestWhoWorkedOn_ResolvesByName(t *testing.T) {
	reg, _, _ := newTestRegistry(t)

	out, err := reg.Execute(context.Background(), "who_worked_on", json.RawMessage(`{"name_or_email":"anna"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		IdentityID int64 `json:"identity_id"`
		Count      int   `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.IdentityID == 0 {
		t.Fatal("expected name resolution to populate identity_id")
	}
}

func TestWhoWorkedOn_MissingIdentifier(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "who_worked_on", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when neither identity_id nor name_or_email given")
	}
}

func TestRecentDecisions_MergesNotesAndFTSMatches(t *testing.T) {
	reg, db, _ := newTestRegistry(t)

	// Seed: one type=note manual entry and one commit whose title contains "decision".
	if _, err := db.InsertActivity(models.Activity{
		Source:    models.SourceManual,
		SourceID:  "manual:n1",
		Type:      models.TypeNote,
		Title:     "Decided to switch to gRPC",
		Content:   "Decided to switch to gRPC after benchmarks.",
		Timestamp: time.Date(2026, 4, 7, 14, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("insert note: %v", err)
	}
	if _, err := db.InsertActivity(models.Activity{
		Source:    models.SourceGit,
		SourceID:  "repo:dec1",
		Type:      models.TypeCommit,
		Title:     "ADR-014: cache layer",
		Content:   "ADR-014 introduces a Redis cache.",
		Timestamp: time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("insert commit: %v", err)
	}

	out, err := reg.Execute(context.Background(), "recent_decisions", json.RawMessage(`{"limit":20}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Activities []activitySummary `json:"activities"`
		Count      int               `json:"count"`
	}
	decodeResult(t, out, &got)
	if got.Count < 2 {
		t.Fatalf("expected at least 2 decisions, got %d", got.Count)
	}
	seen := map[string]bool{}
	for _, a := range got.Activities {
		seen[a.Title] = true
	}
	if !seen["Decided to switch to gRPC"] {
		t.Errorf("expected manual note in results")
	}
	if !seen["ADR-014: cache layer"] {
		t.Errorf("expected ADR-shaped commit in results")
	}
}

func TestPrepMeeting_ByActivityID(t *testing.T) {
	reg, db, _ := newTestRegistry(t)

	// Seed a calendar event whose metadata names anna as an attendee.
	annaEmail := "anna@example.com"
	calMeta := `{"attendees":[{"email":"` + annaEmail + `","display_name":"Anna"}],"url":"https://meet.example.com/abc"}`
	mid, err := db.InsertActivity(models.Activity{
		Source:    models.SourceCalendar,
		SourceID:  "cal:evt1",
		Type:      models.TypeMeeting,
		Title:     "1:1 with Anna",
		Metadata:  calMeta,
		Timestamp: time.Date(2026, 4, 8, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("insert meeting: %v", err)
	}

	out, err := reg.Execute(context.Background(), "prep_meeting", json.RawMessage(
		`{"activity_id":`+strconv.FormatInt(mid, 10)+`}`,
	))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got prepMeetingResult
	decodeResult(t, out, &got)
	if got.MeetingID != mid {
		t.Errorf("meeting_id = %d, want %d", got.MeetingID, mid)
	}
	if got.URL != "https://meet.example.com/abc" {
		t.Errorf("url = %q", got.URL)
	}
	if len(got.Attendees) != 1 || got.Attendees[0].Email != annaEmail {
		t.Fatalf("attendees = %+v", got.Attendees)
	}
	// Anna's identity is in the fixture, so recent activity should populate.
	if got.Attendees[0].IdentityID == 0 {
		t.Errorf("expected anna's identity_id populated")
	}
}

func TestPrepMeeting_MissingIdentifier(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	_, err := reg.Execute(context.Background(), "prep_meeting", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when neither activity_id nor date given")
	}
}
