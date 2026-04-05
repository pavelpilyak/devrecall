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
	provider := &mockLLM{responses: []string{"You fixed the auth token refresh yesterday."}}

	in := strings.NewReader("what did I do yesterday?\n/quit\n")
	var out bytes.Buffer

	session := NewSession(in, &out, ret, provider)
	err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Retriever should have been called with the user query.
	if len(ret.queries) != 1 || ret.queries[0] != "what did I do yesterday?" {
		t.Errorf("retriever queries = %v", ret.queries)
	}

	// LLM should have been called once.
	if provider.calls != 1 {
		t.Errorf("LLM calls = %d, want 1", provider.calls)
	}

	// Output should contain the LLM response.
	output := out.String()
	if !strings.Contains(output, "You fixed the auth token refresh yesterday.") {
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

	session := NewSession(in, &out, ret, provider)
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

	session := NewSession(in, &out, ret, provider)
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

	session := NewSession(in, &out, ret, provider)
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

	session := NewSession(in, &out, ret, provider)
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

	session := NewSession(in, &out, ret, provider)
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

	session := NewSession(in, &out, ret, provider)
	_ = session.Run(context.Background())

	// History should be capped at maxHistory*2 messages.
	if len(session.history) > maxHistory*2 {
		t.Errorf("history length = %d, want <= %d", len(session.history), maxHistory*2)
	}
}

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
