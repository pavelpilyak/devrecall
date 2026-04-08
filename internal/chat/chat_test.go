package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/internal/agent"
	"github.com/pavelpiliak/devrecall/internal/agent/tools"
	"github.com/pavelpiliak/devrecall/internal/chat/freshness"
	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/internal/storage"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// scriptedProvider is a fake llm.ToolCallingProvider that replays a queued
// list of ChatResponses, recording the messages it received per call.
type scriptedProvider struct {
	responses []llm.ChatResponse
	err       error
	calls     int
	messages  [][]llm.Message
}

func (s *scriptedProvider) Name() string { return "scripted" }
func (s *scriptedProvider) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOpts) (string, error) {
	return "", errors.New("Chat not used")
}
func (s *scriptedProvider) SupportsTools(_ context.Context) bool { return true }
func (s *scriptedProvider) SupportsStreaming() bool              { return false }
func (s *scriptedProvider) ChatWithToolsStream(_ context.Context, _ []llm.Message, _ []llm.Tool, _ llm.ChatOpts) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("streaming not used")
}
func (s *scriptedProvider) ChatWithTools(_ context.Context, msgs []llm.Message, _ []llm.Tool, _ llm.ChatOpts) (llm.ChatResponse, error) {
	if s.err != nil {
		return llm.ChatResponse{}, s.err
	}
	s.messages = append(s.messages, append([]llm.Message(nil), msgs...))
	if s.calls >= len(s.responses) {
		return llm.ChatResponse{}, fmt.Errorf("scriptedProvider: ran out of responses (call %d)", s.calls+1)
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func mustOpenDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestSession wires a chat session against an in-memory DB plus a
// scripted provider.
func newTestSession(t *testing.T, in string, prov *scriptedProvider, db *storage.DB) (*Session, *bytes.Buffer) {
	t.Helper()
	if db == nil {
		db = mustOpenDB(t)
	}
	reg := tools.NewRegistry(tools.Deps{
		DB:  db,
		Now: func() time.Time { return time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC) },
	})
	loop := agent.NewLoop(prov, reg, agent.LoopOptions{})
	out := &bytes.Buffer{}
	return NewSession(strings.NewReader(in), out, loop, db), out
}

// finalAnswer is shorthand for a "no more tools" agent response.
func finalAnswer(text string) llm.ChatResponse {
	return llm.ChatResponse{Content: text}
}

func TestSession_BasicQA(t *testing.T) {
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{finalAnswer("You fixed the auth token refresh.")},
	}
	session, out := newTestSession(t, "tell me about auth changes\n/quit\n", prov, nil)

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if prov.calls != 1 {
		t.Errorf("LLM calls = %d, want 1", prov.calls)
	}
	if !strings.Contains(out.String(), "You fixed the auth token refresh.") {
		t.Errorf("output missing LLM response:\n%s", out.String())
	}
}

func TestSession_ConversationHistory(t *testing.T) {
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			finalAnswer("You worked on auth."),
			finalAnswer("The auth work was in the backend-api repo."),
		},
	}
	session, _ := newTestSession(t, "what did I work on?\ntell me more about that\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	if prov.calls != 2 {
		t.Fatalf("LLM calls = %d, want 2", prov.calls)
	}

	// Second call should include conversation history from the first turn:
	// system + user1 + assistant1 + user2 = 4 messages.
	second := prov.messages[1]
	if len(second) != 4 {
		t.Fatalf("second-call messages = %d, want 4", len(second))
	}
	if second[0].Role != "system" {
		t.Errorf("msg[0].role = %q", second[0].Role)
	}
	if second[1].Role != "user" || second[1].Content != "what did I work on?" {
		t.Errorf("history user msg = %+v", second[1])
	}
	if second[2].Role != "assistant" || second[2].Content != "You worked on auth." {
		t.Errorf("history assistant msg = %+v", second[2])
	}
}

func TestSession_QuitCommand(t *testing.T) {
	prov := &scriptedProvider{}
	session, out := newTestSession(t, "/quit\n", prov, nil)

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.calls != 0 {
		t.Errorf("LLM should not be called on /quit")
	}
	if !strings.Contains(out.String(), "Bye!") {
		t.Errorf("output missing Bye! message")
	}
}

func TestSession_ClearCommand(t *testing.T) {
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			finalAnswer("first answer"),
			finalAnswer("second answer"),
		},
	}
	session, _ := newTestSession(t, "question one\n/clear\nquestion two\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	if prov.calls != 2 {
		t.Fatalf("LLM calls = %d, want 2", prov.calls)
	}
	// After /clear the second call should only carry system + user.
	second := prov.messages[1]
	if len(second) != 2 {
		t.Errorf("after /clear, message count = %d, want 2", len(second))
	}
}

func TestSession_AgentError(t *testing.T) {
	prov := &scriptedProvider{err: errors.New("upstream blew up")}
	session, out := newTestSession(t, "test query\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	if !strings.Contains(out.String(), "Error") {
		t.Errorf("output should contain error message:\n%s", out.String())
	}
}

func TestSession_EmptyInputSkipped(t *testing.T) {
	prov := &scriptedProvider{}
	session, _ := newTestSession(t, "\n\n\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	if prov.calls != 0 {
		t.Errorf("empty lines should not trigger LLM calls")
	}
}

func TestSession_HistoryTrimmed(t *testing.T) {
	responses := make([]llm.ChatResponse, maxHistory+2)
	var inputLines []string
	for i := range responses {
		responses[i] = finalAnswer(fmt.Sprintf("answer %d", i))
		inputLines = append(inputLines, fmt.Sprintf("question %d", i))
	}
	inputLines = append(inputLines, "/quit")

	prov := &scriptedProvider{responses: responses}
	session, _ := newTestSession(t, strings.Join(inputLines, "\n")+"\n", prov, nil)
	_ = session.Run(context.Background())

	if len(session.history) > maxHistory*2 {
		t.Errorf("history length = %d, want <= %d", len(session.history), maxHistory*2)
	}
}

// ─── streaming render ─────────────────────────────────────────────────────────

func TestSession_StreamingRender(t *testing.T) {
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
			}},
			finalAnswer("It is noon."),
		},
	}
	session, out := newTestSession(t, "what time is it?\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	output := out.String()
	// Tool call line should appear before the answer.
	if !strings.Contains(output, "→ current_time(") {
		t.Errorf("expected tool-call line, got:\n%s", output)
	}
	if !strings.Contains(output, "← ") {
		t.Errorf("expected tool-result line, got:\n%s", output)
	}
	if !strings.Contains(output, "It is noon.") {
		t.Errorf("expected final answer, got:\n%s", output)
	}

	// /trace after the streamed answer should still see the tool call.
	session2, out2 := newTestSession(t,
		"what time is it?\n/trace\n/quit\n",
		&scriptedProvider{
			responses: []llm.ChatResponse{
				{ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
				}},
				finalAnswer("noon"),
			},
		}, nil)
	_ = session2.Run(context.Background())
	if !strings.Contains(out2.String(), "Last answer used 1 tool call") {
		t.Errorf("trace missing after stream, got:\n%s", out2.String())
	}
}

// ─── /trace ───────────────────────────────────────────────────────────────────

func TestSession_TraceCommand(t *testing.T) {
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
			}},
			finalAnswer("It's noon."),
		},
	}
	session, out := newTestSession(t, "what time is it?\n/trace\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	output := out.String()
	if !strings.Contains(output, "Last answer used 1 tool call") {
		t.Errorf("expected trace header, got:\n%s", output)
	}
	if !strings.Contains(output, "current_time") {
		t.Errorf("trace should mention current_time, got:\n%s", output)
	}
}

func TestSession_TraceCommand_NoQuery(t *testing.T) {
	prov := &scriptedProvider{}
	session, out := newTestSession(t, "/trace\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	if !strings.Contains(out.String(), "No trace available") {
		t.Errorf("expected 'no trace' message, got:\n%s", out.String())
	}
}

// ─── /search ──────────────────────────────────────────────────────────────────

func TestSession_SearchCommand(t *testing.T) {
	db := mustOpenDB(t)
	now := time.Now().UTC()
	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Fix auth token refresh", Content: "Handle expired tokens",
		Timestamp: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a2", Type: models.TypeCommit,
		Title: "Update README", Content: "Add badges",
		Timestamp: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	prov := &scriptedProvider{}
	session, out := newTestSession(t, "/search auth\n/quit\n", prov, db)
	_ = session.Run(context.Background())

	output := out.String()
	if !strings.Contains(output, "Fix auth token refresh") {
		t.Errorf("search should find auth activity, got:\n%s", output)
	}
	if strings.Contains(output, "Update README") {
		t.Error("search should not return unrelated results")
	}
	if prov.calls != 0 {
		t.Error("/search should not call LLM")
	}
}

func TestSession_SearchCommand_NoQuery(t *testing.T) {
	prov := &scriptedProvider{}
	session, out := newTestSession(t, "/search\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	if !strings.Contains(out.String(), "Usage") {
		t.Error("expected usage hint for /search without query")
	}
}

// ─── /stats ───────────────────────────────────────────────────────────────────

func TestSession_StatsCommand(t *testing.T) {
	db := mustOpenDB(t)
	now := time.Now().UTC()
	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Commit 1", Timestamp: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceSlack, SourceID: "s:m1", Type: models.TypeMessage,
		Title: "Message 1", Timestamp: now.Add(-24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	prov := &scriptedProvider{}
	session, out := newTestSession(t, "/stats\n/quit\n", prov, db)
	_ = session.Run(context.Background())

	output := out.String()
	if !strings.Contains(output, "Activities: 2 total") {
		t.Errorf("stats should show total count, got:\n%s", output)
	}
	if !strings.Contains(output, "git") || !strings.Contains(output, "slack") {
		t.Errorf("stats should show both sources, got:\n%s", output)
	}
}

// ─── pre-agent freshness sync ────────────────────────────────────────────────

// TestSession_FreshnessRunsBeforeQuery confirms a stale source is synced
// (with its lifecycle lines rendered) before the agent loop sees the query.
func TestSession_FreshnessRunsBeforeQuery(t *testing.T) {
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{finalAnswer("ok")},
	}
	db := mustOpenDB(t)
	session, out := newTestSession(t, "what's new?\n/quit\n", prov, db)

	calls := 0
	syncers := map[string]freshness.Syncer{
		"git": func(_ context.Context) (int, error) {
			calls++
			return 7, nil
		},
	}
	checker := freshness.New(db, freshness.Options{
		Enabled:    true,
		DefaultTTL: time.Hour,
	})
	session.WithFreshness(checker, syncers)

	_ = session.Run(context.Background())

	if calls != 1 {
		t.Errorf("syncer should run exactly once before query, calls=%d", calls)
	}
	output := out.String()
	if !strings.Contains(output, "Syncing git") {
		t.Errorf("expected 'Syncing git' line, got:\n%s", output)
	}
	if !strings.Contains(output, "git synced (7 new)") {
		t.Errorf("expected 'git synced (7 new)' line, got:\n%s", output)
	}
}

// TestSession_SyncCommandForces verifies /sync runs even when the
// freshness step is disabled and bypasses TTLs.
func TestSession_SyncCommandForces(t *testing.T) {
	prov := &scriptedProvider{}
	db := mustOpenDB(t)
	// Pretend git was synced just now — under the 1h default TTL it would
	// be considered fresh on a normal Run, but /sync forces it.
	if err := db.SetSyncState("git", ""); err != nil {
		t.Fatal(err)
	}

	session, out := newTestSession(t, "/sync\n/quit\n", prov, db)

	calls := 0
	syncers := map[string]freshness.Syncer{
		"git": func(_ context.Context) (int, error) {
			calls++
			return 0, nil
		},
	}
	// Enabled=false to prove /sync forces past it.
	checker := freshness.New(db, freshness.Options{Enabled: false, DefaultTTL: time.Hour})
	session.WithFreshness(checker, syncers)

	_ = session.Run(context.Background())

	if calls != 1 {
		t.Errorf("/sync should force the syncer once, calls=%d", calls)
	}
	if prov.calls != 0 {
		t.Errorf("/sync should not call LLM, calls=%d", prov.calls)
	}
	if !strings.Contains(out.String(), "git synced") {
		t.Errorf("expected synced line, got:\n%s", out.String())
	}
}

// TestSession_FreshnessSilentWhenFresh verifies fresh sources do not emit
// any noise during a normal query.
func TestSession_FreshnessSilentWhenFresh(t *testing.T) {
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{finalAnswer("ok")},
	}
	db := mustOpenDB(t)
	if err := db.SetSyncState("git", ""); err != nil {
		t.Fatal(err)
	}

	session, out := newTestSession(t, "what's new?\n/quit\n", prov, db)

	calls := 0
	syncers := map[string]freshness.Syncer{
		"git": func(_ context.Context) (int, error) {
			calls++
			return 0, nil
		},
	}
	checker := freshness.New(db, freshness.Options{
		Enabled:    true,
		DefaultTTL: time.Hour,
		Now:        func() time.Time { return time.Now().Add(time.Minute) },
	})
	session.WithFreshness(checker, syncers)

	_ = session.Run(context.Background())

	if calls != 0 {
		t.Errorf("fresh source should not invoke syncer, calls=%d", calls)
	}
	if strings.Contains(out.String(), "Syncing git") {
		t.Errorf("fresh source should not emit Syncing line, got:\n%s", out.String())
	}
}

// ─── /help ────────────────────────────────────────────────────────────────────

func TestSession_HelpShowsAllCommands(t *testing.T) {
	prov := &scriptedProvider{}
	session, out := newTestSession(t, "/help\n/quit\n", prov, nil)
	_ = session.Run(context.Background())

	output := out.String()
	for _, cmd := range []string{"/help", "/quit", "/clear", "/search", "/trace", "/stats", "/sync"} {
		if !strings.Contains(output, cmd) {
			t.Errorf("/help output missing %s", cmd)
		}
	}
	// /context and /date should be gone after the rewrite.
	for _, gone := range []string{"/context", "/date"} {
		if strings.Contains(output, gone) {
			t.Errorf("/help should no longer mention %s, got:\n%s", gone, output)
		}
	}
}
