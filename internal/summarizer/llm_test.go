package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// capturingProvider records the prompt sent to the LLM.
type capturingProvider struct {
	response string
	err      error
	messages []llm.Message
}

func (p *capturingProvider) Chat(_ context.Context, msgs []llm.Message, _ llm.ChatOpts) (string, error) {
	p.messages = msgs
	return p.response, p.err
}

func (p *capturingProvider) Name() string { return "mock" }

func TestLLMSummarizer_Empty(t *testing.T) {
	provider := &capturingProvider{response: "should not be called"}
	s := NewLLMSummarizer(provider)

	out, err := s.Standup(nil)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}
	if !strings.Contains(out, "No activity") {
		t.Errorf("expected no-activity message, got %q", out)
	}
}

func TestLLMSummarizer_UsesLLMResponse(t *testing.T) {
	llmOutput := "- Fixed auth token refresh in backend-api\n- Discussed deployment strategy in #backend"
	provider := &capturingProvider{response: llmOutput}
	s := NewLLMSummarizer(provider)

	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		activity("Fix auth refresh", "backend-api", "abc123def", 3, 47, 12, ts),
	}

	out, err := s.Standup(activities)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}
	if out != llmOutput {
		t.Errorf("expected LLM output, got %q", out)
	}
}

func TestLLMSummarizer_FallsBackOnError(t *testing.T) {
	provider := &capturingProvider{err: fmt.Errorf("LLM unavailable")}
	s := NewLLMSummarizer(provider)

	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		activity("Fix auth refresh", "backend-api", "abc123def", 3, 47, 12, ts),
	}

	out, err := s.Standup(activities)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}
	// Should fall back to template output.
	if !strings.Contains(out, "backend-api: Fix auth refresh") {
		t.Errorf("expected template fallback output, got %q", out)
	}
}

func TestLLMSummarizer_FallsBackOnEmptyResponse(t *testing.T) {
	provider := &capturingProvider{response: "   "}
	s := NewLLMSummarizer(provider)

	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		activity("Fix auth refresh", "backend-api", "abc123def", 3, 47, 12, ts),
	}

	out, err := s.Standup(activities)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}
	if !strings.Contains(out, "backend-api: Fix auth refresh") {
		t.Errorf("expected template fallback output, got %q", out)
	}
}

func TestBuildActivitiesPrompt_GitCommits(t *testing.T) {
	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		activity("Fix auth refresh", "backend-api", "abc123def", 3, 47, 12, ts),
		activity("Add retry tests", "backend-api", "def456abc", 1, 10, 0, ts.Add(time.Hour)),
	}

	prompt := buildActivitiesPrompt(activities, nil)

	if !strings.Contains(prompt, "Friday (2026-03-27)") {
		t.Errorf("expected date header in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[Git commit] backend-api: Fix auth refresh (3 files, +47/-12)") {
		t.Errorf("expected git commit details in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[Git commit] backend-api: Add retry tests (1 files, +10/-0)") {
		t.Errorf("expected second commit in prompt:\n%s", prompt)
	}
}

func TestBuildActivitiesPrompt_SlackThread(t *testing.T) {
	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)

	metaJSON, _ := json.Marshal(slackFullMeta{
		ChannelName: "backend",
		ThreadMsgs: []struct {
			User string `json:"user"`
			Text string `json:"text"`
		}{
			{User: "U001", Text: "Should we use blue-green?"},
			{User: "U002", Text: "Yes, let's do it"},
		},
	})

	activities := []models.Activity{{
		Source:    models.SourceSlack,
		Type:      models.TypeMessage,
		Title:     "Thread in #backend",
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}}

	// Without self UIDs — raw user IDs shown.
	prompt := buildActivitiesPrompt(activities, nil)
	if !strings.Contains(prompt, "[Slack thread] #backend (2 messages)") {
		t.Errorf("expected thread header in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "U001: Should we use blue-green?") {
		t.Errorf("expected first message with raw UID in prompt:\n%s", prompt)
	}

	// With self UIDs — "You" label applied.
	selfUIDs := map[string]bool{"U001": true}
	prompt = buildActivitiesPrompt(activities, selfUIDs)
	if !strings.Contains(prompt, "You: Should we use blue-green?") {
		t.Errorf("expected 'You' label for self message:\n%s", prompt)
	}
	if !strings.Contains(prompt, "U002: Yes, let's do it") {
		t.Errorf("expected raw UID for other user:\n%s", prompt)
	}
}

func TestBuildActivitiesPrompt_SlackWithoutSummary(t *testing.T) {
	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)

	metaJSON, _ := json.Marshal(slackMeta{ChannelName: "general"})
	activities := []models.Activity{{
		Source:    models.SourceSlack,
		Type:      models.TypeMessage,
		Title:     "Message in #general",
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}}

	prompt := buildActivitiesPrompt(activities, nil)

	if !strings.Contains(prompt, "[Slack message] #general: Message in #general") {
		t.Errorf("expected raw message in prompt:\n%s", prompt)
	}
}

func TestBuildActivitiesPrompt_MultipleDays(t *testing.T) {
	day1 := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC)

	activities := []models.Activity{
		activity("Day 1 work", "repo", "aaa", 1, 5, 0, day1),
		activity("Day 2 work", "repo", "bbb", 2, 10, 3, day2),
	}

	prompt := buildActivitiesPrompt(activities, nil)

	if !strings.Contains(prompt, "Friday (2026-03-27)") {
		t.Errorf("expected day 1 header:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Saturday (2026-03-28)") {
		t.Errorf("expected day 2 header:\n%s", prompt)
	}
}

func TestLLMSummarizer_PromptIncludesSystemAndUser(t *testing.T) {
	provider := &capturingProvider{response: "standup output"}
	s := NewLLMSummarizer(provider)

	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		activity("Fix bug", "repo", "abc", 1, 1, 0, ts),
	}

	s.Standup(activities)

	if len(provider.messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(provider.messages))
	}
	if provider.messages[0].Role != "system" {
		t.Errorf("expected system message first, got %q", provider.messages[0].Role)
	}
	if !strings.Contains(provider.messages[0].Content, "standup") {
		t.Error("system prompt should mention standup")
	}
	if provider.messages[1].Role != "user" {
		t.Errorf("expected user message second, got %q", provider.messages[1].Role)
	}
	if !strings.Contains(provider.messages[1].Content, "Fix bug") {
		t.Error("user prompt should contain activity details")
	}
}

func TestBuildActivitiesPrompt_CalendarMeeting(t *testing.T) {
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	metaJSON, _ := json.Marshal(calendarMeta{
		DurationMin:    60,
		MeetingType:    "ceremony",
		ResponseStatus: "accepted",
		Attendees: []attendee{
			{Email: "me@example.com", Self: true},
			{Email: "alice@example.com"},
			{Email: "bob@example.com"},
		},
	})

	activities := []models.Activity{{
		Source:    models.SourceCalendar,
		Type:      models.TypeMeeting,
		Title:     "Sprint Planning",
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}}

	prompt := buildActivitiesPrompt(activities, nil)

	if !strings.Contains(prompt, "[Calendar meeting] Sprint Planning") {
		t.Errorf("expected calendar meeting in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "1h") {
		t.Errorf("expected duration in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "ceremony") {
		t.Errorf("expected meeting type in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "2 attendees") {
		t.Errorf("expected attendee count in prompt:\n%s", prompt)
	}
}

func TestBuildActivitiesPrompt_MixedSources(t *testing.T) {
	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)

	threadMeta, _ := json.Marshal(slackFullMeta{
		ChannelName: "backend",
		ThreadMsgs: []struct {
			User string `json:"user"`
			Text string `json:"text"`
		}{
			{User: "U001", Text: "Deploy discussion"},
			{User: "U002", Text: "Use blue-green"},
		},
	})

	activities := []models.Activity{
		activity("Fix auth", "backend-api", "abc123", 3, 47, 12, ts),
		{
			Source:    models.SourceSlack,
			Type:      models.TypeMessage,
			Title:     "Thread in #backend",
			Metadata:  string(threadMeta),
			Timestamp: ts.Add(time.Hour),
		},
	}

	prompt := buildActivitiesPrompt(activities, nil)

	if !strings.Contains(prompt, "[Git commit]") {
		t.Error("should contain git commit")
	}
	if !strings.Contains(prompt, "[Slack thread]") {
		t.Error("should contain slack thread")
	}
}

func TestLLMWeeklySummary_UsesLLMResponse(t *testing.T) {
	llmOutput := "## Weekly Summary\n- Focused on auth system improvements\n\nMeetings: 3h total"
	provider := &capturingProvider{response: llmOutput}
	s := NewLLMSummarizer(provider)

	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		activity("Fix auth", "backend-api", "abc123", 3, 47, 12, ts),
	}

	out, err := s.WeeklySummary(activities)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}
	if out != llmOutput {
		t.Errorf("expected LLM output, got %q", out)
	}

	// Should use weekly system prompt.
	if len(provider.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(provider.messages))
	}
	if !strings.Contains(provider.messages[0].Content, "weekly summary") {
		t.Error("system prompt should mention weekly summary")
	}
}

func TestLLMWeeklySummary_FallsBackOnError(t *testing.T) {
	provider := &capturingProvider{err: fmt.Errorf("LLM unavailable")}
	s := NewLLMSummarizer(provider)

	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)
	activities := []models.Activity{
		activity("Fix auth", "backend-api", "abc123", 3, 47, 12, ts),
	}

	out, err := s.WeeklySummary(activities)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}
	if !strings.Contains(out, "Weekly Summary") {
		t.Errorf("expected template fallback, got %q", out)
	}
}

func TestLLMWeeklySummary_Empty(t *testing.T) {
	provider := &capturingProvider{response: "should not be called"}
	s := NewLLMSummarizer(provider)

	out, err := s.WeeklySummary(nil)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}
	if !strings.Contains(out, "No activity") {
		t.Errorf("expected no-activity message, got %q", out)
	}
}

func TestExtractURL(t *testing.T) {
	tests := []struct {
		name     string
		activity models.Activity
		want     string
	}{
		{
			name: "github PR with url field",
			activity: models.Activity{
				Source:   models.SourceGitHub,
				Metadata: `{"repo":"org/repo","pr_number":42,"url":"https://github.com/org/repo/pull/42"}`,
			},
			want: "https://github.com/org/repo/pull/42",
		},
		{
			name: "slack with permalink",
			activity: models.Activity{
				Source:   models.SourceSlack,
				Metadata: `{"channel_name":"backend","permalink":"https://team.slack.com/archives/C01/p123"}`,
			},
			want: "https://team.slack.com/archives/C01/p123",
		},
		{
			name: "calendar with event_id",
			activity: models.Activity{
				Source:   models.SourceCalendar,
				Metadata: `{"event_id":"abc123","calendar_id":"me@gmail.com"}`,
			},
			want: "https://calendar.google.com/calendar/event?eid=abc123",
		},
		{
			name: "jira with url",
			activity: models.Activity{
				Source:   models.SourceJira,
				Metadata: `{"issue_key":"PROJ-123","url":"https://jira.example.com/browse/PROJ-123"}`,
			},
			want: "https://jira.example.com/browse/PROJ-123",
		},
		{
			name: "git commit without url",
			activity: models.Activity{
				Source:   models.SourceGit,
				Metadata: `{"repo":"my-repo","sha":"abc123"}`,
			},
			want: "",
		},
		{
			name: "empty metadata",
			activity: models.Activity{
				Source:   models.SourceGit,
				Metadata: "",
			},
			want: "",
		},
		{
			name: "invalid JSON",
			activity: models.Activity{
				Source:   models.SourceGit,
				Metadata: "not json",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractURL(tt.activity)
			if got != tt.want {
				t.Errorf("extractURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatLink(t *testing.T) {
	if got := formatLink(""); got != "" {
		t.Errorf("formatLink(\"\") = %q, want empty", got)
	}
	if got := formatLink("https://example.com"); got != " [link](https://example.com)" {
		t.Errorf("formatLink() = %q, want ' [link](https://example.com)'", got)
	}
}

func TestBuildActivitiesPrompt_IncludesLinks(t *testing.T) {
	ts := time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC)

	// GitHub PR with URL.
	prMeta, _ := json.Marshal(map[string]any{
		"repo": "org/repo", "pr_number": 42,
		"url": "https://github.com/org/repo/pull/42",
	})

	// Slack thread with permalink.
	slackMeta, _ := json.Marshal(slackFullMeta{
		ChannelName: "backend",
		Permalink:   "https://team.slack.com/archives/C01/p123",
		ThreadMsgs: []struct {
			User string `json:"user"`
			Text string `json:"text"`
		}{
			{User: "U001", Text: "hello"},
			{User: "U002", Text: "hi"},
		},
	})

	// Jira ticket with URL.
	jiraMeta, _ := json.Marshal(map[string]any{
		"issue_key": "PROJ-42",
		"url":       "https://jira.example.com/browse/PROJ-42",
	})

	activities := []models.Activity{
		{Source: models.SourceGitHub, Type: models.TypePullRequest, Title: "Fix auth", Metadata: string(prMeta), Timestamp: ts},
		{Source: models.SourceSlack, Type: models.TypeMessage, Title: "Thread", Metadata: string(slackMeta), Timestamp: ts},
		{Source: models.SourceJira, Type: models.TypeTicket, Title: "PROJ-42 Auth bug", Metadata: string(jiraMeta), Timestamp: ts},
	}

	prompt := buildActivitiesPrompt(activities, nil)

	if !strings.Contains(prompt, "[link](https://github.com/org/repo/pull/42)") {
		t.Errorf("expected GitHub link in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[link](https://team.slack.com/archives/C01/p123)") {
		t.Errorf("expected Slack permalink in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[link](https://jira.example.com/browse/PROJ-42)") {
		t.Errorf("expected Jira link in prompt:\n%s", prompt)
	}
}

