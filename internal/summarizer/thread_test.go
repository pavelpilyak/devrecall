package summarizer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	slackcollector "github.com/pavelpiliak/devrecall/internal/collector/slack"
	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// mockProvider is a test double for llm.Provider.
type mockProvider struct {
	response string
	err      error
	calls    int
}

func (m *mockProvider) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOpts) (string, error) {
	m.calls++
	return m.response, m.err
}

func (m *mockProvider) Name() string { return "mock" }

func slackActivity(channelName string, threadMsgs []slackcollector.ThreadMsg, summary *slackcollector.ThreadSummary) models.Activity {
	meta := slackMsgMeta{
		ChannelName: channelName,
		ReplyCount:  len(threadMsgs) - 1,
		ThreadMsgs:  threadMsgs,
		Summary:     summary,
	}
	metaJSON, _ := json.Marshal(meta)
	return models.Activity{
		Source:    models.SourceSlack,
		SourceID:  "slack:C01:1234",
		Type:      models.TypeMessage,
		Title:     "Thread in #" + channelName,
		Metadata:  string(metaJSON),
		Timestamp: time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC),
	}
}

func TestSummarizeThreads_CallsLLM(t *testing.T) {
	provider := &mockProvider{
		response: `{"topic": "deployment strategy discussion", "decisions": ["switch to blue-green for payment service"]}`,
	}

	msgs := []slackcollector.ThreadMsg{
		{User: "U1", Text: "Should we use blue-green or canary?"},
		{User: "U2", Text: "Blue-green is safer for the payment service"},
		{User: "U1", Text: "Agreed, let's go with blue-green"},
	}

	activities := []models.Activity{
		slackActivity("backend", msgs, nil),
	}

	ts := NewThreadSummarizer(provider)
	result, count, err := ts.SummarizeThreads(context.Background(), activities)
	if err != nil {
		t.Fatalf("SummarizeThreads: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 summarized, got %d", count)
	}
	if provider.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", provider.calls)
	}

	var meta slackMsgMeta
	if err := json.Unmarshal([]byte(result[0].Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.Summary == nil {
		t.Fatal("expected summary in metadata")
	}
	if meta.Summary.Topic != "deployment strategy discussion" {
		t.Errorf("unexpected topic: %q", meta.Summary.Topic)
	}
	if len(meta.Summary.Decisions) != 1 || meta.Summary.Decisions[0] != "switch to blue-green for payment service" {
		t.Errorf("unexpected decisions: %v", meta.Summary.Decisions)
	}
}

func TestSummarizeThreads_SkipsAlreadySummarized(t *testing.T) {
	provider := &mockProvider{response: `{"topic": "should not be called"}`}

	existing := &slackcollector.ThreadSummary{Topic: "already done"}
	msgs := []slackcollector.ThreadMsg{{User: "U1", Text: "hello"}}

	activities := []models.Activity{
		slackActivity("general", msgs, existing),
	}

	ts := NewThreadSummarizer(provider)
	_, count, err := ts.SummarizeThreads(context.Background(), activities)
	if err != nil {
		t.Fatalf("SummarizeThreads: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 summarized (already done), got %d", count)
	}
	if provider.calls != 0 {
		t.Errorf("expected 0 LLM calls, got %d", provider.calls)
	}
}

func TestSummarizeThreads_SkipsNonSlack(t *testing.T) {
	provider := &mockProvider{response: `{"topic": "should not be called"}`}

	activities := []models.Activity{
		{Source: models.SourceGit, Type: models.TypeCommit, Metadata: `{"repo":"test"}`},
	}

	ts := NewThreadSummarizer(provider)
	_, count, _ := ts.SummarizeThreads(context.Background(), activities)
	if count != 0 {
		t.Errorf("expected 0 summarized for git activity, got %d", count)
	}
}

func TestSummarizeThreads_SkipsNoThreadMsgs(t *testing.T) {
	provider := &mockProvider{response: `{"topic": "should not be called"}`}

	activities := []models.Activity{
		slackActivity("general", nil, nil),
	}

	ts := NewThreadSummarizer(provider)
	_, count, _ := ts.SummarizeThreads(context.Background(), activities)
	if count != 0 {
		t.Errorf("expected 0 summarized for activity without thread msgs, got %d", count)
	}
}

func TestParseThreadSummary_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		topic string
		decs  int
	}{
		{
			name:  "plain JSON",
			input: `{"topic": "refactoring auth module", "decisions": ["use JWT", "add refresh tokens"]}`,
			topic: "refactoring auth module",
			decs:  2,
		},
		{
			name:  "with code fences",
			input: "```json\n{\"topic\": \"test topic\", \"decisions\": []}\n```",
			topic: "test topic",
			decs:  0,
		},
		{
			name:  "no decisions",
			input: `{"topic": "casual chat", "decisions": []}`,
			topic: "casual chat",
			decs:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseThreadSummary(tt.input)
			if err != nil {
				t.Fatalf("parseThreadSummary: %v", err)
			}
			if result.Topic != tt.topic {
				t.Errorf("topic = %q, want %q", result.Topic, tt.topic)
			}
			if len(result.Decisions) != tt.decs {
				t.Errorf("decisions count = %d, want %d", len(result.Decisions), tt.decs)
			}
		})
	}
}

func TestParseThreadSummary_InvalidJSON(t *testing.T) {
	_, err := parseThreadSummary("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseThreadSummary_EmptyTopic(t *testing.T) {
	_, err := parseThreadSummary(`{"topic": "", "decisions": []}`)
	if err == nil {
		t.Error("expected error for empty topic")
	}
}

func TestStandup_ThreadWithSummary(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)

	summary := &slackcollector.ThreadSummary{
		Topic:     "Deployment strategy discussion",
		Decisions: []string{"Switch to blue-green for payment service"},
	}
	metaJSON, _ := json.Marshal(slackMeta{
		ChannelName: "backend",
		ReplyCount:  3,
		Summary:     &slackThreadSummary{Topic: summary.Topic, Decisions: summary.Decisions},
	})

	out, err := s.Standup([]models.Activity{{
		Source:    models.SourceSlack,
		Type:      models.TypeMessage,
		Title:     "Thread in #backend (3 replies)",
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Deployment strategy discussion") {
		t.Errorf("expected thread topic in output:\n%s", out)
	}
	if !strings.Contains(out, "→ Switch to blue-green for payment service") {
		t.Errorf("expected decision in output:\n%s", out)
	}
	// Should NOT contain the raw "Thread in #backend" title when summary exists.
	if strings.Contains(out, "Thread in #backend") {
		t.Errorf("should show summary topic instead of raw title:\n%s", out)
	}
}

func TestStandup_ThreadWithoutSummary(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)

	metaJSON, _ := json.Marshal(slackMeta{ChannelName: "general"})
	out, err := s.Standup([]models.Activity{{
		Source:    models.SourceSlack,
		Type:      models.TypeMessage,
		Title:     "Message in #general",
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}
	if !strings.Contains(out, "Message in #general") {
		t.Errorf("expected raw title for non-summarized message:\n%s", out)
	}
}
