package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/chat/freshness"
)

func TestHandleSyncStream_StreamsFreshnessFrames(t *testing.T) {
	srv, db := setupTestServer(t)

	srv.syncStreamFactory = func() (*freshness.Checker, map[string]freshness.Syncer) {
		checker := freshness.New(db, freshness.Options{Enabled: true, Wait: 5 * time.Second})
		return checker, map[string]freshness.Syncer{
			"git": func(_ context.Context) (int, error) { return 3, nil },
			"slack": func(_ context.Context) (int, error) {
				time.Sleep(5 * time.Millisecond)
				return 7, nil
			},
		}
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/sync/stream", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}

	frames := readSSE(t, w.Body.Bytes())
	if len(frames) == 0 {
		t.Fatalf("no frames decoded:\n%s", w.Body.String())
	}

	statusBySource := map[string][]string{}
	var doneFrame *sseFrame
	for i := range frames {
		f := frames[i]
		switch f.Event {
		case "freshness":
			var ev freshness.Event
			if err := json.Unmarshal([]byte(f.Data), &ev); err != nil {
				t.Fatalf("freshness payload: %v", err)
			}
			statusBySource[ev.Source] = append(statusBySource[ev.Source], string(ev.Status))
		case "done":
			doneFrame = &frames[i]
		}
	}

	for _, src := range []string{"git", "slack"} {
		seq := statusBySource[src]
		if len(seq) < 2 || seq[0] != string(freshness.StatusSyncing) || seq[len(seq)-1] != string(freshness.StatusSynced) {
			t.Errorf("%s: want syncing→…→synced, got %v", src, seq)
		}
	}

	if doneFrame == nil {
		t.Fatalf("missing done frame")
	}
	var done struct {
		TotalAdded int `json:"total_added"`
	}
	if err := json.Unmarshal([]byte(doneFrame.Data), &done); err != nil {
		t.Fatalf("done payload: %v", err)
	}
	if done.TotalAdded != 10 {
		t.Errorf("total_added = %d, want 10", done.TotalAdded)
	}

	if frames[len(frames)-1].Event != "done" {
		t.Errorf("done must be last frame, got %s", frames[len(frames)-1].Event)
	}
}

func TestHandleSyncStream_NoSyncers(t *testing.T) {
	srv, db := setupTestServer(t)
	srv.syncStreamFactory = func() (*freshness.Checker, map[string]freshness.Syncer) {
		return freshness.New(db, freshness.Options{Enabled: true, Wait: time.Second}),
			map[string]freshness.Syncer{}
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/sync/stream", nil))

	frames := readSSE(t, w.Body.Bytes())
	if len(frames) != 1 || frames[0].Event != "done" {
		t.Fatalf("want single done frame, got %#v", frames)
	}
	if !strings.Contains(frames[0].Data, `"total_added":0`) {
		t.Errorf("done payload should report 0 total, got %s", frames[0].Data)
	}
}

func TestHandleSyncStream_PropagatesSyncerError(t *testing.T) {
	srv, db := setupTestServer(t)
	srv.syncStreamFactory = func() (*freshness.Checker, map[string]freshness.Syncer) {
		return freshness.New(db, freshness.Options{Enabled: true, Wait: time.Second}),
			map[string]freshness.Syncer{
				"jira": func(_ context.Context) (int, error) {
					return 0, errors.New("token expired")
				},
				"git": func(_ context.Context) (int, error) { return 2, nil },
			}
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/sync/stream", nil))

	frames := readSSE(t, w.Body.Bytes())

	var sawError bool
	for _, f := range frames {
		if f.Event != "freshness" {
			continue
		}
		var ev freshness.Event
		if err := json.Unmarshal([]byte(f.Data), &ev); err != nil {
			t.Fatalf("freshness payload: %v", err)
		}
		if ev.Source == "jira" && ev.Status == freshness.StatusError {
			sawError = true
			if !strings.Contains(ev.Err, "token expired") {
				t.Errorf("error frame missing message: %s", ev.Err)
			}
		}
	}
	if !sawError {
		t.Errorf("expected error frame for jira:\n%s", w.Body.String())
	}

	// `done` still emits with the count from the source(s) that succeeded.
	last := frames[len(frames)-1]
	if last.Event != "done" {
		t.Fatalf("last frame = %s, want done", last.Event)
	}
	if !strings.Contains(last.Data, `"total_added":2`) {
		t.Errorf("total_added should be 2 (git only): %s", last.Data)
	}
}

// Sanity check that the production wiring is at least constructible —
// catches signature drift between BuildAllSyncers and the handler.
func TestSyncStreamPlan_DefaultUsesBuildAllSyncers(t *testing.T) {
	srv, _ := setupTestServer(t)
	checker, syncers := srv.syncStreamPlan()
	if checker == nil {
		t.Fatal("checker is nil")
	}
	// setupTestServer enables Git but no remote sources, so we expect
	// exactly the git syncer.
	if _, ok := syncers["git"]; !ok {
		t.Errorf("git syncer missing from default plan: %v", keys(syncers))
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
