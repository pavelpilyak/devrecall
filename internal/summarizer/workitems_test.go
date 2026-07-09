package summarizer

import (
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// fakeWorkItemStore returns a fixed refs map.
type fakeWorkItemStore struct {
	refs map[int64][]models.WorkItemRef
}

func (f *fakeWorkItemStore) ListActivityWorkItems(ids []int64) (map[int64][]models.WorkItemRef, error) {
	return f.refs, nil
}

func workItemFixture(t *testing.T) ([]models.Activity, map[int64][]models.WorkItemRef) {
	t.Helper()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	ticket := models.Activity{
		ID: 1, Source: models.SourceJira, Type: models.TypeTicket,
		Title:     "Fix payment retry",
		Metadata:  `{"issue_key":"PROJ-123","issue_summary":"Fix payment retry","status":"In Review","url":"https://jira/PROJ-123"}`,
		Timestamp: ts,
	}
	commit := models.Activity{
		ID: 2, Source: models.SourceGit, Type: models.TypeCommit,
		Title:     "PROJ-123: fix retry logic",
		Metadata:  `{"repo":"backend-api","issue_keys":["PROJ-123"],"files_changed":3,"insertions":47,"deletions":12}`,
		Timestamp: ts.Add(time.Hour),
	}
	transition := models.Activity{
		ID: 3, Source: models.SourceJira, Type: models.TypeTicket,
		Title:     "PROJ-123 moved to In Review",
		Metadata:  `{"issue_key":"PROJ-123","from_status":"In Progress","to_status":"In Review"}`,
		Timestamp: ts.Add(2 * time.Hour),
	}
	meeting := models.Activity{
		ID: 4, Source: models.SourceCalendar, Type: models.TypeMeeting,
		Title:     "Sprint planning",
		Metadata:  `{"duration_min":60,"meeting_type":"ceremony"}`,
		Timestamp: ts.Add(3 * time.Hour),
	}

	ref := models.WorkItemRef{
		ID: 1, Key: "PROJ-123", Kind: "ticket", Title: "Fix payment retry",
		Status: "In Review", StatusChangedAt: ts.Add(2 * time.Hour),
	}
	refs := map[int64][]models.WorkItemRef{
		1: {ref},
		2: {ref},
		3: {ref},
	}
	return []models.Activity{ticket, commit, transition, meeting}, refs
}

func TestBuildActivitiesPrompt_WorkItemGrouping(t *testing.T) {
	activities, refs := workItemFixture(t)
	prompt := buildActivitiesPrompt(activities, nil, refs)

	if !strings.Contains(prompt, "### PROJ-123 — Fix payment retry") {
		t.Errorf("expected work-item header:\n%s", prompt)
	}
	if !strings.Contains(prompt, "status: In Review, moved to In Review on 2026-03-27") {
		t.Errorf("expected status fact in header:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[Git commit] backend-api: PROJ-123: fix retry logic") {
		t.Errorf("expected commit bullet under work item:\n%s", prompt)
	}
	// The transition must not appear as its own bullet.
	if strings.Contains(prompt, "PROJ-123 moved to In Review") {
		t.Errorf("status transition rendered as bullet:\n%s", prompt)
	}
	// Unlinked meeting lands in the residual section.
	if !strings.Contains(prompt, "### Other activity") {
		t.Errorf("expected residual section:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[Calendar meeting] Sprint planning") {
		t.Errorf("expected meeting in residual section:\n%s", prompt)
	}
	// Residual comes after the work-item block.
	if strings.Index(prompt, "### Other activity") < strings.Index(prompt, "### PROJ-123") {
		t.Errorf("residual section should follow work-item blocks:\n%s", prompt)
	}
}

func TestBuildActivitiesPrompt_StatusOnlyWorkItem(t *testing.T) {
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	transition := models.Activity{
		ID: 1, Source: models.SourceJira, Type: models.TypeTicket,
		Title:     "PROJ-9 moved to Done",
		Metadata:  `{"issue_key":"PROJ-9","to_status":"Done"}`,
		Timestamp: ts,
	}
	refs := map[int64][]models.WorkItemRef{
		1: {{ID: 1, Key: "PROJ-9", Kind: "ticket", Title: "Cleanup task", Status: "Done", StatusChangedAt: ts}},
	}

	prompt := buildActivitiesPrompt([]models.Activity{transition}, nil, refs)

	if !strings.Contains(prompt, "### PROJ-9 — Cleanup task") {
		t.Errorf("expected header:\n%s", prompt)
	}
	if !strings.Contains(prompt, "(status update only — no other activity)") {
		t.Errorf("expected status-only marker:\n%s", prompt)
	}
	if strings.Contains(prompt, "- [jira] PROJ-9 moved to Done") {
		t.Errorf("transition rendered as bullet:\n%s", prompt)
	}
}

func TestBuildActivitiesPrompt_MultiKeyActivityRendersOnce(t *testing.T) {
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	commit := models.Activity{
		ID: 1, Source: models.SourceGit, Type: models.TypeCommit,
		Title:     "shared fix",
		Metadata:  `{"repo":"backend-api","issue_keys":["PROJ-1","PROJ-2"]}`,
		Timestamp: ts,
	}
	refs := map[int64][]models.WorkItemRef{
		1: {
			{ID: 1, Key: "PROJ-1", Kind: "ticket"},
			{ID: 2, Key: "PROJ-2", Kind: "ticket"},
		},
	}

	prompt := buildActivitiesPrompt([]models.Activity{commit}, nil, refs)

	if strings.Count(prompt, "shared fix") != 1 {
		t.Errorf("multi-key activity should render exactly once:\n%s", prompt)
	}
	if !strings.Contains(prompt, "(also PROJ-2)") {
		t.Errorf("expected secondary key annotation:\n%s", prompt)
	}
}

func TestBuildActivitiesPrompt_NilWorkItemsUnchanged(t *testing.T) {
	// Regression guard: without a work-item map the output must be exactly
	// the flat date-grouped format (no ### headers, transitions intact).
	activities, _ := workItemFixture(t)
	prompt := buildActivitiesPrompt(activities, nil, nil)

	if strings.Contains(prompt, "###") {
		t.Errorf("nil work items must not produce block headers:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- [jira] PROJ-123 moved to In Review") {
		t.Errorf("flat mode should keep the transition line:\n%s", prompt)
	}
	if !strings.Contains(prompt, "## Friday (2026-03-27)") {
		t.Errorf("expected date header:\n%s", prompt)
	}
}

func TestLLMSummarizer_WithWorkItemsGroupsPrompt(t *testing.T) {
	activities, refs := workItemFixture(t)
	provider := &capturingProvider{response: "standup"}
	s := NewLLMSummarizer(provider).WithWorkItems(&fakeWorkItemStore{refs: refs})

	if _, err := s.Standup(activities); err != nil {
		t.Fatalf("Standup: %v", err)
	}
	if len(provider.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(provider.messages))
	}
	if !strings.Contains(provider.messages[1].Content, "### PROJ-123") {
		t.Errorf("user prompt should contain work-item block:\n%s", provider.messages[1].Content)
	}
	if !strings.Contains(provider.messages[0].Content, "work item") {
		t.Errorf("system prompt should explain work-item headers")
	}
}
