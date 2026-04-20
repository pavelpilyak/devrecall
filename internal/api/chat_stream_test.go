package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/agent"
	agenttools "github.com/pavelpilyak/devrecall/internal/agent/tools"
	"github.com/pavelpilyak/devrecall/internal/chat/freshness"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// fakeStreamProvider is a tiny llm.ToolCallingProvider used by the chat
// stream handler tests. It replays a queued list of buffered ChatResponses
// (one per turn). Streaming is intentionally off so the agent loop falls
// back to the buffered path — the SSE wire format is the same either way.
type fakeStreamProvider struct {
	responses []llm.ChatResponse
	calls     int
	err       error
}

func (f *fakeStreamProvider) Name() string { return "fake" }
func (f *fakeStreamProvider) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOpts) (string, error) {
	return "", errors.New("Chat not used")
}
func (f *fakeStreamProvider) SupportsTools(_ context.Context) bool { return true }
func (f *fakeStreamProvider) SupportsStreaming() bool              { return false }
func (f *fakeStreamProvider) ChatWithToolsStream(_ context.Context, _ []llm.Message, _ []llm.Tool, _ llm.ChatOpts) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("streaming off")
}
func (f *fakeStreamProvider) ChatWithTools(_ context.Context, _ []llm.Message, _ []llm.Tool, _ llm.ChatOpts) (llm.ChatResponse, error) {
	if f.err != nil {
		return llm.ChatResponse{}, f.err
	}
	if f.calls >= len(f.responses) {
		return llm.ChatResponse{}, fmt.Errorf("fakeStreamProvider: ran out of responses (call %d)", f.calls+1)
	}
	resp := f.responses[f.calls]
	f.calls++
	return resp, nil
}

// installFakeLoop wires a Server to use the given provider through the
// agentLoopFactory hook. Activities seeded into the DB are visible to
// any tools the model invokes.
func installFakeLoop(srv *Server, prov llm.ToolCallingProvider) {
	srv.agentLoopFactory = func() (*agent.Loop, error) {
		registry := agenttools.NewRegistry(agenttools.Deps{
			DB: srv.db,
			Now: func() time.Time {
				return time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
			},
		})
		return agent.NewLoop(prov, registry, agent.LoopOptions{}), nil
	}
}

// sseFrame is one decoded `event:`/`data:` pair from the test stream.
type sseFrame struct {
	Event string
	Data  string
}

// readSSE parses the response body produced by handleChatStream into a
// slice of frames. The handler always emits one blank line between
// frames, so this is enough for tests.
func readSSE(t *testing.T, body []byte) []sseFrame {
	t.Helper()
	var frames []sseFrame
	var cur sseFrame
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.Data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.Event != "" {
				frames = append(frames, cur)
				cur = sseFrame{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	return frames
}

func TestHandleChatStream_HappyPath(t *testing.T) {
	srv, db := setupTestServer(t)
	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit,
		Title: "Initial commit", Timestamp: time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	prov := &fakeStreamProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
			}},
			{Content: "It is noon."},
		},
	}
	installFakeLoop(srv, prov)

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/chat/stream",
		map[string]any{"message": "what time is it?"}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}

	frames := readSSE(t, w.Body.Bytes())
	if len(frames) == 0 {
		t.Fatalf("no SSE frames decoded:\n%s", w.Body.String())
	}

	// Find required event types in order.
	var saw = map[string]int{}
	for _, f := range frames {
		saw[f.Event]++
	}
	for _, want := range []string{"thinking", "tool_call", "tool_result", "done"} {
		if saw[want] == 0 {
			t.Errorf("missing %s event in stream:\n%s", want, w.Body.String())
		}
	}

	// Final frame should be `done` with the assistant text.
	last := frames[len(frames)-1]
	if last.Event != "done" {
		t.Errorf("last event = %s, want done", last.Event)
	}
	var doneEv struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(last.Data), &doneEv); err != nil {
		t.Fatalf("done payload: %v (%s)", err, last.Data)
	}
	if doneEv.Content != "It is noon." {
		t.Errorf("done.content = %q", doneEv.Content)
	}
}

// TestHandleChatStream_FreshnessEvents verifies the SSE handler runs the
// freshness checker before the agent loop starts and surfaces lifecycle
// events as `freshness` SSE frames.
func TestHandleChatStream_FreshnessEvents(t *testing.T) {
	srv, db := setupTestServer(t)

	prov := &fakeStreamProvider{
		responses: []llm.ChatResponse{{Content: "ok"}},
	}
	installFakeLoop(srv, prov)

	syncerCalls := 0
	srv.freshnessFactory = func() (*freshness.Checker, map[string]freshness.Syncer) {
		return freshness.New(db, freshness.Options{
				Enabled:    true,
				DefaultTTL: time.Hour,
			}),
			map[string]freshness.Syncer{
				"git": func(_ context.Context) (int, error) {
					syncerCalls++
					return 4, nil
				},
			}
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/chat/stream",
		map[string]any{"message": "what's new?"}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if syncerCalls != 1 {
		t.Errorf("git syncer should run exactly once, got %d", syncerCalls)
	}

	frames := readSSE(t, w.Body.Bytes())

	// We expect at least two `freshness` frames (Syncing then Synced)
	// emitted before the agent's `done` frame.
	var freshFrames []sseFrame
	doneIdx := -1
	for i, f := range frames {
		if f.Event == "freshness" {
			freshFrames = append(freshFrames, f)
		}
		if f.Event == "done" && doneIdx == -1 {
			doneIdx = i
		}
	}
	if len(freshFrames) < 2 {
		t.Fatalf("expected ≥2 freshness frames, got %d:\n%s", len(freshFrames), w.Body.String())
	}
	if doneIdx == -1 {
		t.Fatalf("missing done frame")
	}
	// Freshness must precede done.
	for i, f := range frames[:doneIdx] {
		if f.Event == "freshness" {
			break
		}
		if i == doneIdx-1 {
			t.Errorf("no freshness frame before done")
		}
	}

	// Check Syncing/Synced statuses are present in the payloads.
	var statuses []string
	for _, f := range freshFrames {
		var ev struct {
			Status string `json:"status"`
			Added  int    `json:"added,omitempty"`
		}
		if err := json.Unmarshal([]byte(f.Data), &ev); err != nil {
			t.Fatalf("freshness payload: %v (%s)", err, f.Data)
		}
		statuses = append(statuses, ev.Status)
	}
	hasSyncing, hasSynced := false, false
	for _, s := range statuses {
		if s == "syncing" {
			hasSyncing = true
		}
		if s == "synced" {
			hasSynced = true
		}
	}
	if !hasSyncing || !hasSynced {
		t.Errorf("expected syncing+synced statuses, got %v", statuses)
	}
}

// TestHandleChat_AgentLoop verifies the buffered POST /api/chat handler
// runs the agent loop (not the deleted RAG path) and returns the trace
// + freshness events alongside the assistant text.
func TestHandleChat_AgentLoop(t *testing.T) {
	srv, db := setupTestServer(t)

	prov := &fakeStreamProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
			}},
			{Content: "It is noon."},
		},
	}
	installFakeLoop(srv, prov)

	syncerCalls := 0
	srv.freshnessFactory = func() (*freshness.Checker, map[string]freshness.Syncer) {
		return freshness.New(db, freshness.Options{
				Enabled:    true,
				DefaultTTL: time.Hour,
			}),
			map[string]freshness.Syncer{
				"git": func(_ context.Context) (int, error) {
					syncerCalls++
					return 2, nil
				},
			}
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/chat",
		map[string]any{"message": "what time is it?"}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Response  string `json:"response"`
		Steps     int    `json:"steps"`
		Trace     []struct {
			ToolName string `json:"tool_name"`
		} `json:"trace"`
		Freshness []struct {
			Source string `json:"source"`
			Status string `json:"status"`
			Added  int    `json:"added,omitempty"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}

	if resp.Response != "It is noon." {
		t.Errorf("response = %q", resp.Response)
	}
	if resp.Steps < 2 {
		t.Errorf("steps = %d, want ≥2", resp.Steps)
	}
	if len(resp.Trace) != 1 || resp.Trace[0].ToolName != "current_time" {
		t.Errorf("trace = %+v, want one current_time call", resp.Trace)
	}
	if syncerCalls != 1 {
		t.Errorf("freshness syncer should run once, got %d", syncerCalls)
	}
	// Expect at least syncing+synced events from the freshness step.
	gotSyncing, gotSynced := false, false
	for _, ev := range resp.Freshness {
		if ev.Source == "git" && ev.Status == "syncing" {
			gotSyncing = true
		}
		if ev.Source == "git" && ev.Status == "synced" && ev.Added == 2 {
			gotSynced = true
		}
	}
	if !gotSyncing || !gotSynced {
		t.Errorf("freshness events missing syncing/synced: %+v", resp.Freshness)
	}
}

func TestHandleChatStream_MissingMessage(t *testing.T) {
	srv, _ := setupTestServer(t)
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/chat/stream", map[string]any{}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleChatStream_LoopFactoryError(t *testing.T) {
	srv, _ := setupTestServer(t)
	srv.agentLoopFactory = func() (*agent.Loop, error) {
		return nil, errors.New("LLM not configured")
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/chat/stream",
		map[string]any{"message": "hi"}))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "LLM not configured") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestHandleChatStream_ProviderError(t *testing.T) {
	srv, _ := setupTestServer(t)
	prov := &fakeStreamProvider{err: errors.New("upstream blew up")}
	installFakeLoop(srv, prov)

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/chat/stream",
		map[string]any{"message": "hi"}))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (errors arrive over the stream), got %d", w.Code)
	}

	frames := readSSE(t, w.Body.Bytes())
	if len(frames) == 0 {
		t.Fatalf("no frames")
	}
	last := frames[len(frames)-1]
	if last.Event != "error" {
		t.Errorf("last event = %s, want error", last.Event)
	}
	if !strings.Contains(last.Data, "upstream blew up") {
		t.Errorf("error data = %s", last.Data)
	}
}
