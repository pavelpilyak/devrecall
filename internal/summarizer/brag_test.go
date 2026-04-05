package summarizer

import (
	"strings"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func TestTemplateSummarizer_BragDoc(t *testing.T) {
	ts := NewTemplateSummarizer()

	activities := []models.Activity{
		{Source: models.SourceGit, Type: models.TypeCommit, Title: "Fix auth bug", Timestamp: time.Now()},
		{Source: models.SourceGit, Type: models.TypeCommit, Title: "Add retry logic", Timestamp: time.Now()},
		{Source: models.SourceGit, Type: models.TypePullRequest, Title: "PR #42", Timestamp: time.Now()},
		{Source: models.SourceSlack, Type: models.TypeMessage, Title: "Discussion", Timestamp: time.Now()},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Sprint planning", Timestamp: time.Now()},
	}

	summaries := []models.Summary{
		{PeriodType: "weekly", PeriodStart: "2026-03-23", PeriodEnd: "2026-03-30", SummaryText: "Worked on auth."},
	}

	text, err := ts.BragDoc(activities, summaries)
	if err != nil {
		t.Fatalf("BragDoc: %v", err)
	}

	if !strings.Contains(text, "## Period Summaries") {
		t.Error("should include summaries section")
	}
	if !strings.Contains(text, "## Metrics") {
		t.Error("should include metrics section")
	}
	if !strings.Contains(text, "2 commits") {
		t.Errorf("should show commit count, got:\n%s", text)
	}
	if !strings.Contains(text, "1 meeting") {
		t.Errorf("should show meeting count, got:\n%s", text)
	}
}

func TestTemplateSummarizer_BragDoc_Empty(t *testing.T) {
	ts := NewTemplateSummarizer()

	text, err := ts.BragDoc(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "No activities found") {
		t.Error("empty brag should say no activities")
	}
}

func TestBuildBragPrompt_WithSummaries(t *testing.T) {
	summaries := []models.Summary{
		{PeriodType: "weekly", PeriodStart: "2026-03-23", PeriodEnd: "2026-03-30", SummaryText: "Auth work."},
	}

	prompt := buildBragPrompt(nil, summaries)
	if !strings.Contains(prompt, "Period summaries") {
		t.Error("prompt should include summaries header")
	}
	if !strings.Contains(prompt, "Auth work.") {
		t.Error("prompt should include summary text")
	}
}

func TestBuildBragPrompt_Empty(t *testing.T) {
	prompt := buildBragPrompt(nil, nil)
	if !strings.Contains(prompt, "No activities") {
		t.Error("empty prompt should indicate no data")
	}
}

func TestTemplateSummarizer_PerfReview(t *testing.T) {
	ts := NewTemplateSummarizer()

	activities := []models.Activity{
		{Source: models.SourceGit, Type: models.TypeCommit, Title: "Fix bug", Timestamp: time.Now()},
		{Source: models.SourceGit, Type: models.TypePullRequest, Title: "PR #42", Timestamp: time.Now()},
		{Source: models.SourceGit, Type: models.TypeReview, Title: "Review PR #43", Timestamp: time.Now()},
		{Source: models.SourceSlack, Type: models.TypeMessage, Title: "Discussion", Timestamp: time.Now()},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Sprint planning", Timestamp: time.Now()},
		{Source: models.SourceJira, Type: models.TypeTicket, Title: "PROJ-123", Timestamp: time.Now()},
	}

	text, err := ts.PerfReview(activities, nil)
	if err != nil {
		t.Fatalf("PerfReview: %v", err)
	}

	if !strings.Contains(text, "## Evidence & Metrics") {
		t.Error("should include evidence section")
	}
	if !strings.Contains(text, "1 commits") {
		t.Errorf("should show commit count, got:\n%s", text)
	}
	if !strings.Contains(text, "1 code reviews") {
		t.Errorf("should show review count, got:\n%s", text)
	}
	if !strings.Contains(text, "1 tickets") {
		t.Errorf("should show ticket count, got:\n%s", text)
	}
}

func TestTemplateSummarizer_PerfReview_Empty(t *testing.T) {
	ts := NewTemplateSummarizer()

	text, err := ts.PerfReview(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "No activities found") {
		t.Error("empty perf review should say no activities")
	}
}
