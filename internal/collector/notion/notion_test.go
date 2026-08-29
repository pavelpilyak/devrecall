package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

const me = "user-me"
const other = "user-other"

// page builds a Notion search result. editedBy/createdBy are user ids.
func page(id, title, createdBy, editedBy, edited string) map[string]any {
	return map[string]any{
		"object":           "page",
		"id":               id,
		"created_time":     "2026-08-01T09:00:00.000Z",
		"last_edited_time": edited,
		"created_by":       map[string]any{"id": createdBy},
		"last_edited_by":   map[string]any{"id": editedBy},
		"url":              "https://notion.so/" + id,
		"parent":           map[string]any{"type": "workspace", "workspace": true},
		"properties": map[string]any{
			"title": map[string]any{
				"type":  "title",
				"title": []map[string]any{{"plain_text": title}},
			},
		},
	}
}

// server returns pages in the given order, one page of results per call.
// blockResults is what the fake returns for a page-body request; tests override
// it when they care about content.
var blockResults = []map[string]any{}
var blockCalls int

func server(t *testing.T, pages ...[]map[string]any) (*Collector, *int) {
	t.Helper()
	blockResults = []map[string]any{}
	blockCalls = 0
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block fetches for page bodies; tests that care assert on them.
		if strings.HasPrefix(r.URL.Path, "/v1/blocks/") {
			blockCalls++
			json.NewEncoder(w).Encode(map[string]any{"results": blockResults})
			return
		}
		if r.URL.Path != "/v1/search" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Notion-Version") == "" {
			t.Error("Notion-Version header missing; Notion rejects requests without it")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		idx := calls
		calls++
		if idx >= len(pages) {
			json.NewEncoder(w).Encode(map[string]any{"results": []any{}, "has_more": false})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results":     pages[idx],
			"has_more":    idx < len(pages)-1,
			"next_cursor": fmt.Sprintf("cursor-%d", idx+1),
		})
	}))
	t.Cleanup(srv.Close)
	return NewWithClient("secret", me, srv.URL, srv.Client()), &calls
}

func collect(t *testing.T, c *Collector, since time.Time) []models.Activity {
	t.Helper()
	acts, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	return acts
}

// Notion's search API cannot filter by author, so "only mine" is enforced here.
// Without it the collector would index the whole shared workspace.
func TestCollect_KeepsOnlyYourPages(t *testing.T) {
	c, _ := server(t, []map[string]any{
		page("p1", "My design doc", me, me, "2026-08-10T10:00:00.000Z"),
		page("p2", "Someone else's page", other, other, "2026-08-10T09:00:00.000Z"),
		page("p3", "Shared doc I edited", other, me, "2026-08-10T08:00:00.000Z"),
	})
	acts := collect(t, c, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))

	if len(acts) != 2 {
		t.Fatalf("got %d activities, want 2 (mine + one I edited)", len(acts))
	}
	titles := map[string]bool{}
	for _, a := range acts {
		titles[a.Title] = true
	}
	if titles["Someone else's page"] {
		t.Error("indexed a page that is not yours")
	}
	if !titles["My design doc"] || !titles["Shared doc I edited"] {
		t.Errorf("missing one of your pages: %v", titles)
	}
}

// Results come back newest-first, so the first page older than the window means
// nothing after it can qualify — stop rather than paging the whole workspace.
func TestCollectSince_StopsAtWindowEdge(t *testing.T) {
	c, calls := server(t,
		[]map[string]any{
			page("p1", "Recent", me, me, "2026-08-10T10:00:00.000Z"),
			page("p2", "Too old", me, me, "2026-01-01T10:00:00.000Z"),
		},
		[]map[string]any{page("p3", "Older still", me, me, "2025-12-01T10:00:00.000Z")},
	)
	acts := collect(t, c, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))

	if len(acts) != 1 || acts[0].Title != "Recent" {
		t.Fatalf("got %v, want just the recent page", acts)
	}
	if *calls != 1 {
		t.Errorf("made %d requests; should have stopped after the first page", *calls)
	}
}

// A database row's title property is named by the user ("Name", "Task", …), so
// matching on the property key would miss it. Match on type instead.
func TestCollect_TitleFromDatabaseRow(t *testing.T) {
	p := page("p1", "unused", me, me, "2026-08-10T10:00:00.000Z")
	p["properties"] = map[string]any{
		"Status": map[string]any{"type": "select"},
		"Task name": map[string]any{
			"type":  "title",
			"title": []map[string]any{{"plain_text": "Ship the "}, {"plain_text": "Notion collector"}},
		},
	}
	c, _ := server(t, []map[string]any{p})

	acts := collect(t, c, time.Time{})
	if len(acts) != 1 || acts[0].Title != "Ship the Notion collector" {
		t.Fatalf("title = %q, want the joined rich-text of the title property", acts[0].Title)
	}
}

func TestCollect_SkipsArchivedAndTrashed(t *testing.T) {
	arch := page("p1", "Archived", me, me, "2026-08-10T10:00:00.000Z")
	arch["archived"] = true
	trash := page("p2", "Trashed", me, me, "2026-08-10T09:00:00.000Z")
	trash["in_trash"] = true
	c, _ := server(t, []map[string]any{arch, trash,
		page("p3", "Live", me, me, "2026-08-10T08:00:00.000Z")})

	acts := collect(t, c, time.Time{})
	if len(acts) != 1 || acts[0].Title != "Live" {
		t.Fatalf("got %v, want only the live page", acts)
	}
}

func TestCollect_UntitledPageStillIndexed(t *testing.T) {
	p := page("p1", "", me, me, "2026-08-10T10:00:00.000Z")
	p["properties"] = map[string]any{}
	c, _ := server(t, []map[string]any{p})

	acts := collect(t, c, time.Time{})
	if len(acts) != 1 || acts[0].Title != "Untitled Notion page" {
		t.Fatalf("got %v, want a placeholder title rather than a blank row", acts)
	}
}

func TestCollect_ActivityShape(t *testing.T) {
	c, _ := server(t, []map[string]any{page("abc", "RFC: retries", me, me, "2026-08-10T10:30:00.000Z")})
	a := collect(t, c, time.Time{})[0]

	if a.Source != models.SourceNotion || a.Type != models.TypeDocument {
		t.Errorf("source/type = %s/%s", a.Source, a.Type)
	}
	if a.SourceID != "abc" {
		t.Errorf("source_id = %q, want the page id so re-syncs upsert", a.SourceID)
	}
	if !a.Timestamp.Equal(time.Date(2026, 8, 10, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v, want last_edited_time", a.Timestamp)
	}
	var m pageMeta
	if err := json.Unmarshal([]byte(a.Metadata), &m); err != nil {
		t.Fatal(err)
	}
	if m.URL != "https://notion.so/abc" {
		t.Errorf("url = %q; without it Timeline and Search rows can't link out", m.URL)
	}
}

// textBlock builds a Notion block of the given type carrying rich text.
func textBlock(kind, text string) map[string]any {
	return map[string]any{
		"type": kind,
		kind:   map[string]any{"rich_text": []map[string]any{{"plain_text": text}}},
	}
}

// Titles alone make search near-useless and produce a digest that only restates
// the title, so the body is what gives a Notion row any retrieval value.
func TestCollect_FetchesPageBody(t *testing.T) {
	c, _ := server(t, []map[string]any{page("p1", "Retry RFC", me, me, "2026-08-10T10:00:00.000Z")})
	blockResults = []map[string]any{
		textBlock("heading_1", "Retry strategy"),
		textBlock("paragraph", "We back off exponentially and cap at 30s."),
		{"type": "image", "image": map[string]any{}}, // no rich_text: must not break parsing
		textBlock("bulleted_list_item", "Idempotency keys required"),
	}
	a := collect(t, c, time.Time{})[0]

	for _, want := range []string{"Retry strategy", "exponentially", "Idempotency keys"} {
		if !strings.Contains(a.Content, want) {
			t.Errorf("content missing %q; got %q", want, a.Content)
		}
	}
}

// A page body is one extra request each against a ~3 req/s limit, so the run
// must stay bounded no matter how many pages match.
func TestCollect_BodyFetchesAreBounded(t *testing.T) {
	var many []map[string]any
	for i := 0; i < maxContentFetches+25; i++ {
		many = append(many, page(fmt.Sprintf("p%d", i), fmt.Sprintf("Page %d", i), me, me, "2026-08-10T10:00:00.000Z"))
	}
	c, _ := server(t, many)
	collect(t, c, time.Time{})

	if blockCalls > maxContentFetches {
		t.Errorf("made %d body requests, over the %d cap", blockCalls, maxContentFetches)
	}
}

// A page you created years ago and edited yesterday is an *edit* yesterday.
// Labelling it "created" misdates authorship and reads wrong in a brag doc.
func TestCollect_OldPageEditedNowIsAnEdit(t *testing.T) {
	p := page("p1", "Recipes", me, me, "2026-08-28T23:16:00.000Z")
	p["created_time"] = "2022-12-11T11:42:00.000Z"
	c, _ := server(t, []map[string]any{p})

	a := collect(t, c, time.Time{})[0]
	var m pageMeta
	json.Unmarshal([]byte(a.Metadata), &m)

	if m.Action != "edited" {
		t.Errorf("action = %q, want edited — the page was created in 2022, not at this timestamp", m.Action)
	}
	if !a.Timestamp.Equal(time.Date(2026, 8, 28, 23, 16, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v, want the edit time", a.Timestamp)
	}
}

// A page written in one sitting is genuinely authorship.
func TestCollect_NewlyWrittenPageIsCreated(t *testing.T) {
	p := page("p1", "Fresh spec", me, me, "2026-08-10T10:03:00.000Z")
	p["created_time"] = "2026-08-10T10:00:00.000Z"
	c, _ := server(t, []map[string]any{p})

	var m pageMeta
	json.Unmarshal([]byte(collect(t, c, time.Time{})[0].Metadata), &m)
	if m.Action != "created" {
		t.Errorf("action = %q, want created for a page written in one sitting", m.Action)
	}
}

// If someone else made the last edit, that edit isn't yours — the newest thing
// you did is creating the page, so the activity belongs at creation time.
func TestCollect_CreatorButNotLastEditorDatesToCreation(t *testing.T) {
	p := page("p1", "My doc, their edit", me, other, "2026-08-28T23:16:00.000Z")
	p["created_time"] = "2026-02-01T09:00:00.000Z"
	c, _ := server(t, []map[string]any{p})

	acts := collect(t, c, time.Time{})
	if len(acts) != 1 {
		t.Fatalf("got %d activities, want 1", len(acts))
	}
	var m pageMeta
	json.Unmarshal([]byte(acts[0].Metadata), &m)
	if m.Action != "created" {
		t.Errorf("action = %q, want created", m.Action)
	}
	if !acts[0].Timestamp.Equal(time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v, want creation time — the later edit was someone else's", acts[0].Timestamp)
	}
}
