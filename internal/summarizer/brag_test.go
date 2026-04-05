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
