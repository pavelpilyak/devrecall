package chat

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/internal/rag"
	"github.com/pavelpiliak/devrecall/internal/storage"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// mockRetriever returns canned results.
type mockRetriever struct {
	results []rag.Result
	err     error
	queries []string // records queries received
}

func (m *mockRetriever) Retrieve(_ context.Context, query string, _ int) ([]rag.Result, error) {
	m.queries = append(m.queries, query)
	return m.results, m.err
}

// mockLLM returns canned responses in order.
type mockLLM struct {
	responses []string
	calls     int
	messages  [][]llm.Message // records messages for each call
}

func (m *mockLLM) Chat(_ context.Context, msgs []llm.Message, _ llm.ChatOpts) (string, error) {
	m.messages = append(m.messages, msgs)
	if m.calls >= len(m.responses) {
		return "no more responses", nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *mockLLM) Name() string { return "mock" }

func mustOpenDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSession_BasicQA(t *testing.T) {
	ret := &mockRetriever{
		results: []rag.Result{
			{
				Activity: models.Activity{
					ID: 1, Source: models.SourceGit, Type: models.TypeCommit,
					Title: "Fix auth token refresh", Timestamp: time.Now(),
				},
				Score: 0.9,
			},
		},
	}
	provider := &mockLLM{responses: []string{"You fixed the auth token refresh."}}

	in := strings.NewReader("tell me about auth changes\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Retriever should have been called with the user query.
	if len(ret.queries) != 1 || ret.queries[0] != "tell me about auth changes" {
		t.Errorf("retriever queries = %v", ret.queries)
	}

	// LLM should have been called once.
	if provider.calls != 1 {
		t.Errorf("LLM calls = %d, want 1", provider.calls)
	}

	// Output should contain the LLM response.
	output := out.String()
	if !strings.Contains(output, "You fixed the auth token refresh.") {
		t.Errorf("output missing LLM response:\n%s", output)
	}
}

func TestSession_ConversationHistory(t *testing.T) {
	ret := &mockRetriever{results: nil}
	provider := &mockLLM{responses: []string{
		"You worked on auth.",
		"The auth work was in the backend-api repo.",
	}}

	in := strings.NewReader("what did I work on?\ntell me more about that\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if provider.calls != 2 {
		t.Fatalf("LLM calls = %d, want 2", provider.calls)
	}

	// Second call should include conversation history from the first exchange.
	secondMsgs := provider.messages[1]
	// Expect: system + user1 + assistant1 + user2 = 4 messages
	if len(secondMsgs) != 4 {
		t.Fatalf("second call message count = %d, want 4", len(secondMsgs))
	}
	if secondMsgs[0].Role != "system" {
		t.Errorf("msg[0] role = %q, want system", secondMsgs[0].Role)
	}
	// History: raw user query (not context-injected)
	if secondMsgs[1].Role != "user" || secondMsgs[1].Content != "what did I work on?" {
		t.Errorf("history user msg = %q", secondMsgs[1].Content)
	}
	if secondMsgs[2].Role != "assistant" || secondMsgs[2].Content != "You worked on auth." {
		t.Errorf("history assistant msg = %q", secondMsgs[2].Content)
	}
}

func TestSession_QuitCommand(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if provider.calls != 0 {
		t.Errorf("LLM should not be called on /quit")
	}
	if !strings.Contains(out.String(), "Bye!") {
		t.Errorf("output missing Bye! message")
	}
}

func TestSession_ClearCommand(t *testing.T) {
	ret := &mockRetriever{results: nil}
	provider := &mockLLM{responses: []string{"first answer", "second answer"}}

	in := strings.NewReader("question one\n/clear\nquestion two\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if provider.calls != 2 {
		t.Fatalf("LLM calls = %d, want 2", provider.calls)
	}

	// After /clear, the second call should NOT contain history from the first.
	secondMsgs := provider.messages[1]
	// Expect: system + user2 = 2 messages (no history from before /clear).
	if len(secondMsgs) != 2 {
		t.Errorf("after /clear, message count = %d, want 2 (system + user)", len(secondMsgs))
	}
}

func TestSession_RetrievalError(t *testing.T) {
	ret := &mockRetriever{err: fmt.Errorf("connection refused")}
	provider := &mockLLM{}

	in := strings.NewReader("test query\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if provider.calls != 0 {
		t.Errorf("LLM should not be called when retrieval fails")
	}
	if !strings.Contains(out.String(), "Error") {
		t.Errorf("output should contain error message")
	}
}

func TestSession_EmptyInputSkipped(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("\n\n\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if provider.calls != 0 {
		t.Errorf("empty lines should not trigger LLM calls")
	}
}

func TestSession_HistoryTrimmed(t *testing.T) {
	ret := &mockRetriever{results: nil}

	// Generate more than maxHistory exchanges.
	responses := make([]string, maxHistory+2)
	var inputLines []string
	for i := range responses {
		responses[i] = fmt.Sprintf("answer %d", i)
		inputLines = append(inputLines, fmt.Sprintf("question %d", i))
	}
	inputLines = append(inputLines, "/quit")
	provider := &mockLLM{responses: responses}

	in := strings.NewReader(strings.Join(inputLines, "\n") + "\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	// History should be capped at maxHistory*2 messages.
	if len(session.history) > maxHistory*2 {
		t.Errorf("history length = %d, want <= %d", len(session.history), maxHistory*2)
	}
}

// --- New command tests ---

func TestSession_ContextCommand(t *testing.T) {
	activity := models.Activity{
		ID: 1, Source: models.SourceGit, Type: models.TypeCommit,
		Title: "Fix auth bug", Timestamp: time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC),
	}
	ret := &mockRetriever{results: []rag.Result{{Activity: activity, Score: 0.85}}}
	provider := &mockLLM{responses: []string{"You fixed auth."}}

	in := strings.NewReader("what did I do?\n/context\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	output := out.String()
	if !strings.Contains(output, "Retrieved 1 activities") {
		t.Errorf("expected context output, got:\n%s", output)
	}
	if !strings.Contains(output, "Fix auth bug") {
		t.Errorf("context should show activity title")
	}
	if !strings.Contains(output, "0.8500") {
		t.Errorf("context should show score")
	}
}

func TestSession_ContextCommand_NoQuery(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/context\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if !strings.Contains(out.String(), "No context available") {
		t.Error("expected 'no context' message before any query")
	}
}

func TestSession_SearchCommand(t *testing.T) {
	db := mustOpenDB(t)
	now := time.Now().UTC()

	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Fix auth token refresh", Content: "Handle expired tokens",
		Timestamp: now,
	})
	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a2", Type: models.TypeCommit,
		Title: "Update README", Content: "Add badges",
		Timestamp: now.Add(-time.Hour),
	})

	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/search auth\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, db)
	_ = session.Run(context.Background())

	output := out.String()
	if !strings.Contains(output, "Fix auth token refresh") {
		t.Errorf("search should find auth activity, got:\n%s", output)
	}
	if strings.Contains(output, "Update README") {
		t.Error("search should not return unrelated results")
	}
	if provider.calls != 0 {
		t.Error("/search should not call LLM")
	}
}

func TestSession_SearchCommand_NoQuery(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/search\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if !strings.Contains(out.String(), "Usage") {
		t.Error("expected usage hint for /search without query")
	}
}

func TestSession_StatsCommand(t *testing.T) {
	db := mustOpenDB(t)
	now := time.Now().UTC()

	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Commit 1", Timestamp: now,
	})
	db.InsertActivity(models.Activity{
		Source: models.SourceSlack, SourceID: "s:m1", Type: models.TypeMessage,
		Title: "Message 1", Timestamp: now.Add(-24 * time.Hour),
	})

	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/stats\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, db)
	_ = session.Run(context.Background())

	output := out.String()
	if !strings.Contains(output, "Activities: 2 total") {
		t.Errorf("stats should show total count, got:\n%s", output)
	}
	if !strings.Contains(output, "git") {
		t.Error("stats should show git source")
	}
	if !strings.Contains(output, "slack") {
		t.Error("stats should show slack source")
	}
}

func TestSession_StatsCommand_NoDB(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/stats\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if !strings.Contains(out.String(), "not available") {
		t.Error("expected 'not available' when db is nil")
	}
}

func TestSession_DateCommand(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/date last week\n/date\n/date clear\n/date\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	output := out.String()
	// After setting, should show the range.
	if !strings.Contains(output, "Date filter set:") {
		t.Errorf("expected date filter confirmation, got:\n%s", output)
	}
	// After clearing, should show "no filter".
	if !strings.Contains(output, "No date filter set") {
		t.Errorf("expected 'no filter' after clear, got:\n%s", output)
	}
}

func TestSession_DateCommand_InvalidFormat(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/date banana\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	if !strings.Contains(out.String(), "Could not parse") {
		t.Error("expected parse error for invalid date")
	}
}

func TestSession_HelpShowsAllCommands(t *testing.T) {
	ret := &mockRetriever{}
	provider := &mockLLM{}

	in := strings.NewReader("/help\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider, nil)
	_ = session.Run(context.Background())

	output := out.String()
	for _, cmd := range []string{"/help", "/quit", "/clear", "/search", "/context", "/date", "/stats"} {
		if !strings.Contains(output, cmd) {
			t.Errorf("/help output missing %s", cmd)
		}
	}
}

// --- formatContext tests ---

func TestFormatContext(t *testing.T) {
	now := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	results := []rag.Result{
		{
			Activity: models.Activity{
				Source: models.SourceGit, Type: models.TypeCommit,
				Title: "Fix auth bug", Timestamp: now,
			},
		},
		{
			Activity: models.Activity{
				Source: models.SourceSlack, Type: models.TypeMessage,
				Title: "Auth discussion", Content: "We should fix the token",
				Timestamp: now.Add(-time.Hour),
			},
		},
	}

	out := formatContext(results)
	if !strings.Contains(out, "[1]") || !strings.Contains(out, "[2]") {
		t.Error("formatted context should number results")
	}
	if !strings.Contains(out, "2026-03-27") {
		t.Error("formatted context should include date")
	}
	if !strings.Contains(out, "Fix auth bug") {
		t.Error("formatted context should include title")
	}
	if !strings.Contains(out, "We should fix the token") {
		t.Error("formatted context should include content")
	}
}

func TestFormatContext_TruncatesLongContent(t *testing.T) {
	long := strings.Repeat("x", 500)
	results := []rag.Result{
		{
			Activity: models.Activity{
				Source: models.SourceGit, Type: models.TypeCommit,
				Title: "Big commit", Content: long, Timestamp: time.Now(),
			},
		},
	}

	out := formatContext(results)
	if !strings.Contains(out, "...") {
		t.Error("long content should be truncated with ...")
	}
	if strings.Contains(out, long) {
		t.Error("full 500-char content should not appear")
	}
}

func TestFormatContext_Empty(t *testing.T) {
	out := formatContext(nil)
	if out != "" {
		t.Errorf("empty results should return empty string, got %q", out)
	}
}

// --- parseDateRange tests ---

func TestParseDateRange(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"today", false},
		{"yesterday", false},
		{"last week", false},
		{"this week", false},
		{"last month", false},
		{"this month", false},
		{"2026-03", false},
		{"2026-03-15", false},
		{"2026-03-01..2026-03-31", false},
		{"banana", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			after, before, err := parseDateRange(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDateRange(%q) = no error, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDateRange(%q) error: %v", tt.input, err)
			}
			if after.IsZero() || before.IsZero() {
				t.Errorf("parseDateRange(%q) returned zero times", tt.input)
			}
			if !before.After(after) {
				t.Errorf("parseDateRange(%q): before (%v) should be after (%v)", tt.input, before, after)
			}
		})
	}
}

func TestParseDateRange_MonthRange(t *testing.T) {
	after, before, err := parseDateRange("2026-03")
	if err != nil {
		t.Fatal(err)
	}
	if after != time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("after = %v, want 2026-03-01", after)
	}
	if before != time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("before = %v, want 2026-04-01", before)
	}
}

func TestParseDateRange_DotDotRange(t *testing.T) {
	after, before, err := parseDateRange("2026-03-01..2026-03-15")
	if err != nil {
		t.Fatal(err)
	}
	if after != time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("after = %v, want 2026-03-01", after)
	}
	// before should be day after end (inclusive end date)
	if before != time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC) {
		t.Errorf("before = %v, want 2026-03-16", before)
	}
}

func TestExtractDateHint(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tests := []struct {
		query     string
		wantOk    bool
		wantAfter time.Time
	}{
		{"what did I do yesterday?", true, today.AddDate(0, 0, -1)},
		{"show me today's work", true, today},
		{"what happened two days ago", true, today.AddDate(0, 0, -2)},
		{"work from 2 days ago", true, today.AddDate(0, 0, -2)},
		{"what about 3 days ago?", true, today.AddDate(0, 0, -3)},
		{"summarize last week", true, time.Time{}}, // just check ok
		{"this month's PRs", true, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)},
		{"what is the meaning of life", false, time.Time{}},
		{"tell me about auth", false, time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			after, _, ok := extractDateHint(tt.query)
			if ok != tt.wantOk {
				t.Errorf("extractDateHint(%q) ok = %v, want %v", tt.query, ok, tt.wantOk)
				return
			}
			if !tt.wantOk || tt.wantAfter.IsZero() {
				return
			}
			if !after.Equal(tt.wantAfter) {
				t.Errorf("extractDateHint(%q) after = %v, want %v", tt.query, after, tt.wantAfter)
			}
		})
	}
}
