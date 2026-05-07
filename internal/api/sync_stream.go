package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pavelpilyak/devrecall/internal/chat/freshness"
)

// syncStreamWait is how long handleSyncStream waits for in-flight
// collectors to finish before cancelling stragglers. Significantly
// longer than freshness.DefaultWait because the user explicitly asked
// for a full sync — Slack/Jira/etc. can take tens of seconds on a cold
// fetch and we'd rather block the SSE connection than truncate them.
const syncStreamWait = 5 * time.Minute

// handleSyncStream serves the full sync as Server-Sent Events.
//
// Unlike POST /api/sync (which only runs git and returns one JSON blob
// at the end), this endpoint streams a `freshness` event for every
// lifecycle transition of every enabled source so the desktop app can
// show "Syncing slack…" / "slack synced (12 new)" in its tooltip.
//
// Event names mirror chat_stream's freshness frames:
//
//	freshness   {"source":"slack","status":"syncing"}
//	freshness   {"source":"slack","status":"synced","added":12}
//	done        {"total_added":N}
//	error       {"error":"..."}
//
// The connection closes after a terminal `done` event.
func (s *Server) handleSyncStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported by this server")
		return
	}

	checker, syncers := s.syncStreamPlan()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if len(syncers) == 0 {
		writeSyncEvent(w, flusher, "done", map[string]any{"total_added": 0})
		return
	}

	totalAdded := 0
	for ev := range checker.Run(r.Context(), syncers, true) {
		if ev.Status == freshness.StatusSynced {
			totalAdded += ev.Added
		}
		writeSyncEvent(w, flusher, "freshness", ev)
	}
	writeSyncEvent(w, flusher, "done", map[string]any{"total_added": totalAdded})
}

// syncStreamPlan returns the freshness checker + syncers used by
// handleSyncStream. Tests inject deterministic versions via
// syncStreamFactory; production builds them lazily so config changes
// (a fresh OAuth token, a newly enabled source) are picked up on the
// next click without needing to restart the server.
func (s *Server) syncStreamPlan() (*freshness.Checker, map[string]freshness.Syncer) {
	if s.syncStreamFactory != nil {
		return s.syncStreamFactory()
	}
	cfg := s.Cfg()
	checker := freshness.New(s.db, freshness.Options{
		Enabled: true,
		Wait:    syncStreamWait,
	})
	return checker, BuildAllSyncers(cfg, s.db, s.tokenStore)
}

func writeSyncEvent(w http.ResponseWriter, flusher http.Flusher, name string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body)
	flusher.Flush()
}
