package summarizer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func meta(repo, sha string, files, ins, del int) string {
	b, _ := json.Marshal(commitMeta{
		Repo: repo, SHA: sha, FilesChanged: files, Insertions: ins, Deletions: del,
	})
	return string(b)
}

func metaWithKeys(repo, sha string, files, ins, del int, issueKeys []string) string {
	b, _ := json.Marshal(commitMeta{
		Repo: repo, SHA: sha, FilesChanged: files, Insertions: ins, Deletions: del, IssueKeys: issueKeys,
	})
	return string(b)
}

func activity(title, repo, sha string, files, ins, del int, ts time.Time) models.Activity {
	return models.Activity{
		Source:    models.SourceGit,
		Type:     models.TypeCommit,
		Title:    title,
		Metadata: meta(repo, sha, files, ins, del),
		Timestamp: ts,
	}
}

func activityWithKeys(title, repo, sha string, files, ins, del int, issueKeys []string, ts time.Time) models.Activity {
	return models.Activity{
		Source:    models.SourceGit,
		Type:     models.TypeCommit,
		Title:    title,
		Metadata: metaWithKeys(repo, sha, files, ins, del, issueKeys),
		Timestamp: ts,
	}
}

func ticketActivity(source models.Source, title, issueKey, toStatus string, ts time.Time) models.Activity {
	m := ticketActivityMeta{IssueKey: issueKey, ToStatus: toStatus}
	b, _ := json.Marshal(m)
	return models.Activity{
		Source:    source,
		SourceID:  fmt.Sprintf("%s:%s", source, issueKey),
		Type:      models.TypeTicket,
		Title:     title,
		Metadata:  string(b),
		Timestamp: ts,
	}
}

func ticketActivityWithSprint(source models.Source, title, issueKey, toStatus, sprint string, ts time.Time) models.Activity {
	m := ticketActivityMeta{IssueKey: issueKey, ToStatus: toStatus, Sprint: sprint}
	b, _ := json.Marshal(m)
	return models.Activity{
		Source:    source,
		SourceID:  fmt.Sprintf("%s:%s", source, issueKey),
		Type:      models.TypeTicket,
		Title:     title,
		Metadata:  string(b),
		Timestamp: ts,
	}
}

func ticketActivityWithCycle(source models.Source, title, identifier, toStatus, cycle string, ts time.Time) models.Activity {
	m := ticketActivityMeta{Identifier: identifier, ToStatus: toStatus, Cycle: cycle}
	b, _ := json.Marshal(m)
	return models.Activity{
		Source:    source,
		SourceID:  fmt.Sprintf("%s:%s", source, identifier),
		Type:      models.TypeTicket,
		Title:     title,
		Metadata:  string(b),
		Timestamp: ts,
	}
}

func TestStandup_Empty(t *testing.T) {
	s := NewTemplateSummarizer()
	out, err := s.Standup(nil)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}
	if !strings.Contains(out, "No activity") {
		t.Errorf("expected no-activity message, got %q", out)
	}
}

func TestStandup_SingleCommit(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 14, 30, 0, 0, time.UTC) // Friday

	out, err := s.Standup([]models.Activity{
		activity("Fix auth refresh", "backend-api", "abc123def456", 3, 47, 12, ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Friday (2026-03-27):") {
		t.Errorf("missing date header in:\n%s", out)
	}
	if !strings.Contains(out, "- backend-api: Fix auth refresh (abc123d) — 3 files, +47/-12") {
		t.Errorf("unexpected bullet format in:\n%s", out)
	}
}

func TestStandup_GroupsByRepo(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Fix auth", "backend-api", "aaa1111", 3, 47, 12, ts),
		activity("Add tests", "backend-api", "bbb2222", 1, 10, 0, ts.Add(time.Hour)),
		activity("Update login form", "frontend-app", "ccc3333", 2, 20, 5, ts.Add(2*time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	lines := strings.Split(out, "\n")
	// Should have: header + 3 bullets = 4 lines.
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), out)
	}
	// backend-api commits should come before frontend-app (alphabetical).
	if !strings.HasPrefix(lines[1], "- backend-api:") {
		t.Errorf("first bullet should be backend-api: %q", lines[1])
	}
	if !strings.HasPrefix(lines[3], "- frontend-app:") {
		t.Errorf("third bullet should be frontend-app: %q", lines[3])
	}
}

func TestStandup_MultipleDays(t *testing.T) {
	s := NewTemplateSummarizer()
	day1 := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Day 1 work", "repo", "aaa", 1, 5, 0, day1),
		activity("Day 2 work", "repo", "bbb", 2, 10, 3, day2),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Friday (2026-03-27):") {
		t.Errorf("missing day 1 header:\n%s", out)
	}
	if !strings.Contains(out, "Saturday (2026-03-28):") {
		t.Errorf("missing day 2 header:\n%s", out)
	}
}

func TestStandup_NoStats(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Empty commit", "repo", "abc", 0, 0, 0, ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Should not have the " — " stats separator.
	if strings.Contains(out, " — ") {
		t.Errorf("should not show stats for zero-stat commit:\n%s", out)
	}
}

func TestStandup_ShortSHA(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	// Short SHA (<=7) should be used as-is.
	out, _ := s.Standup([]models.Activity{
		activity("Short sha", "repo", "abc", 1, 1, 0, ts),
	})
	if !strings.Contains(out, "(abc)") {
		t.Errorf("short SHA should be used as-is:\n%s", out)
	}
}

func calendarActivity(title string, durationMin int, meetingType, responseStatus string, attendeeCount int, ts time.Time) models.Activity {
	var attendees []attendee
	attendees = append(attendees, attendee{Email: "me@example.com", Self: true})
	for i := 0; i < attendeeCount-1; i++ {
		attendees = append(attendees, attendee{Email: fmt.Sprintf("person%d@example.com", i+1)})
	}
	metaJSON, _ := json.Marshal(calendarMeta{
		DurationMin:    durationMin,
		MeetingType:    meetingType,
		ResponseStatus: responseStatus,
		Attendees:      attendees,
	})
	return models.Activity{
		Source:    models.SourceCalendar,
		Type:      models.TypeMeeting,
		Title:     title,
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}
}

func TestStandup_CalendarMeeting(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		calendarActivity("1:1 with Sarah", 30, "1:1", "accepted", 2, ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Meetings: 1:1 with Sarah") {
		t.Errorf("expected meeting bullet, got:\n%s", out)
	}
	if !strings.Contains(out, "30min") {
		t.Errorf("expected duration in output:\n%s", out)
	}
	if !strings.Contains(out, "1:1") {
		t.Errorf("expected meeting type:\n%s", out)
	}
}

func TestStandup_CalendarMeetingTimeSummary(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 9, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		calendarActivity("Standup", 15, "standup", "accepted", 5, ts),
		calendarActivity("Sprint Planning", 60, "ceremony", "accepted", 8, ts.Add(time.Hour)),
		calendarActivity("1:1 with Manager", 30, "1:1", "accepted", 2, ts.Add(3*time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Total: 15 + 60 + 30 = 105 minutes = 1h45min
	if !strings.Contains(out, "1h45min in meetings") {
		t.Errorf("expected meeting time summary, got:\n%s", out)
	}
}

func TestStandup_CalendarSkipsFocusTime(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		calendarActivity("Focus Time", 120, "focus", "accepted", 1, ts),
		calendarActivity("1:1 with Sarah", 30, "1:1", "accepted", 2, ts.Add(3*time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if strings.Contains(out, "Focus Time") {
		t.Errorf("focus time should be skipped:\n%s", out)
	}
	if !strings.Contains(out, "1:1 with Sarah") {
		t.Errorf("regular meeting should be present:\n%s", out)
	}
}

func TestStandup_CalendarSkipsDeclined(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		calendarActivity("Declined Meeting", 60, "group", "declined", 5, ts),
		calendarActivity("Attended Meeting", 30, "group", "accepted", 3, ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if strings.Contains(out, "Declined Meeting") {
		t.Errorf("declined meeting should be skipped:\n%s", out)
	}
	if !strings.Contains(out, "Attended Meeting") {
		t.Errorf("attended meeting should be present:\n%s", out)
	}
}

func TestStandup_CalendarMixedWithGitAndSlack(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Fix auth bug", "backend-api", "abc123def", 3, 47, 12, ts),
		calendarActivity("Sprint Planning", 60, "ceremony", "accepted", 8, ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "backend-api: Fix auth bug") {
		t.Errorf("missing git commit:\n%s", out)
	}
	if !strings.Contains(out, "Meetings: Sprint Planning") {
		t.Errorf("missing meeting:\n%s", out)
	}
	if !strings.Contains(out, "1h in meetings") {
		t.Errorf("missing meeting time summary:\n%s", out)
	}
}

func TestStandup_CalendarHourDuration(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, _ := s.Standup([]models.Activity{
		calendarActivity("Long Meeting", 120, "group", "accepted", 5, ts),
	})

	if !strings.Contains(out, "2h") {
		t.Errorf("expected 2h format:\n%s", out)
	}
}

func TestStandup_TodaySectionUpcomingMeetings(t *testing.T) {
	s := NewTemplateSummarizer()
	// Yesterday's activity.
	yesterday := time.Now().AddDate(0, 0, -1)
	yesterday = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 14, 0, 0, 0, time.UTC)

	// Today's meetings.
	today := time.Now()
	meeting1 := time.Date(today.Year(), today.Month(), today.Day(), 10, 0, 0, 0, time.UTC)
	meeting2 := time.Date(today.Year(), today.Month(), today.Day(), 14, 0, 0, 0, time.UTC)

	todayMeta1, _ := json.Marshal(calendarMeta{
		Start:          meeting1.Format(time.RFC3339),
		DurationMin:    60,
		MeetingType:    "ceremony",
		ResponseStatus: "accepted",
	})
	todayMeta2, _ := json.Marshal(calendarMeta{
		Start:          meeting2.Format(time.RFC3339),
		DurationMin:    30,
		MeetingType:    "1:1",
		ResponseStatus: "accepted",
	})

	activities := []models.Activity{
		activity("Fix auth bug", "backend-api", "abc123def", 3, 47, 12, yesterday),
		{
			Source:    models.SourceCalendar,
			Type:      models.TypeMeeting,
			Title:     "Design review",
			Metadata:  string(todayMeta1),
			Timestamp: meeting1,
		},
		{
			Source:    models.SourceCalendar,
			Type:      models.TypeMeeting,
			Title:     "1:1 with manager",
			Metadata:  string(todayMeta2),
			Timestamp: meeting2,
		},
	}

	out, err := s.Standup(activities)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Should have yesterday's activity.
	if !strings.Contains(out, "backend-api: Fix auth bug") {
		t.Errorf("missing yesterday's activity:\n%s", out)
	}

	// Should have "Today" header.
	todayDate := today.Format("2006-01-02")
	if !strings.Contains(out, "Today ("+todayDate+"):") {
		t.Errorf("missing Today header:\n%s", out)
	}

	// Should show meeting times.
	if !strings.Contains(out, "Design review") {
		t.Errorf("missing today's meeting:\n%s", out)
	}
	if !strings.Contains(out, "1:1 with manager") {
		t.Errorf("missing today's 1:1:\n%s", out)
	}
	// Duration should be shown.
	if !strings.Contains(out, "1h") {
		t.Errorf("missing duration:\n%s", out)
	}
	if !strings.Contains(out, "30min") {
		t.Errorf("missing 30min duration:\n%s", out)
	}
}

func TestStandup_TodaySkipsFocusAndDeclined(t *testing.T) {
	s := NewTemplateSummarizer()
	today := time.Now()
	meeting := time.Date(today.Year(), today.Month(), today.Day(), 10, 0, 0, 0, time.UTC)

	focusMeta, _ := json.Marshal(calendarMeta{
		Start:          meeting.Format(time.RFC3339),
		DurationMin:    120,
		MeetingType:    "focus",
		ResponseStatus: "accepted",
	})
	declinedMeta, _ := json.Marshal(calendarMeta{
		Start:          meeting.Add(3 * time.Hour).Format(time.RFC3339),
		DurationMin:    60,
		MeetingType:    "group",
		ResponseStatus: "declined",
	})
	acceptedMeta, _ := json.Marshal(calendarMeta{
		Start:          meeting.Add(5 * time.Hour).Format(time.RFC3339),
		DurationMin:    30,
		MeetingType:    "1:1",
		ResponseStatus: "accepted",
	})

	activities := []models.Activity{
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Focus Time", Metadata: string(focusMeta), Timestamp: meeting},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Skipped Meeting", Metadata: string(declinedMeta), Timestamp: meeting.Add(3 * time.Hour)},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Real Meeting", Metadata: string(acceptedMeta), Timestamp: meeting.Add(5 * time.Hour)},
	}

	out, err := s.Standup(activities)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if strings.Contains(out, "Focus Time") {
		t.Errorf("focus time should be skipped:\n%s", out)
	}
	if strings.Contains(out, "Skipped Meeting") {
		t.Errorf("declined meeting should be skipped:\n%s", out)
	}
	if !strings.Contains(out, "Real Meeting") {
		t.Errorf("accepted meeting should be present:\n%s", out)
	}
}

func TestStandup_TodayOnlyMeetings(t *testing.T) {
	s := NewTemplateSummarizer()
	today := time.Now()
	meeting := time.Date(today.Year(), today.Month(), today.Day(), 9, 0, 0, 0, time.UTC)

	meetingMeta, _ := json.Marshal(calendarMeta{
		Start:          meeting.Format(time.RFC3339),
		DurationMin:    15,
		MeetingType:    "standup",
		ResponseStatus: "accepted",
	})

	activities := []models.Activity{
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Daily Standup", Metadata: string(meetingMeta), Timestamp: meeting},
	}

	out, err := s.Standup(activities)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Today") {
		t.Errorf("expected Today section:\n%s", out)
	}
	if !strings.Contains(out, "Daily Standup") {
		t.Errorf("expected meeting:\n%s", out)
	}
}

func TestWeeklySummary_Empty(t *testing.T) {
	s := NewTemplateSummarizer()
	out, err := s.WeeklySummary(nil)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}
	if !strings.Contains(out, "No activity") {
		t.Errorf("expected no-activity message, got %q", out)
	}
}

func TestWeeklySummary_CommitsOnly(t *testing.T) {
	s := NewTemplateSummarizer()
	mon := time.Date(2026, 3, 30, 10, 0, 0, 0, time.UTC) // Monday
	tue := time.Date(2026, 3, 31, 14, 0, 0, 0, time.UTC) // Tuesday

	activities := []models.Activity{
		activity("Fix auth", "backend-api", "abc123", 3, 47, 12, mon),
		activity("Add tests", "backend-api", "def456", 1, 10, 0, mon.Add(time.Hour)),
		activity("Update UI", "frontend", "ghi789", 2, 20, 5, tue),
	}

	out, err := s.WeeklySummary(activities)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}

	if !strings.Contains(out, "Weekly Summary") {
		t.Errorf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "Monday (2026-03-30):") {
		t.Errorf("missing Monday:\n%s", out)
	}
	if !strings.Contains(out, "2 commits in backend-api") {
		t.Errorf("missing Monday commits:\n%s", out)
	}
	if !strings.Contains(out, "Tuesday (2026-03-31):") {
		t.Errorf("missing Tuesday:\n%s", out)
	}
	if !strings.Contains(out, "1 commits in frontend") {
		t.Errorf("missing Tuesday commits:\n%s", out)
	}
	if !strings.Contains(out, "Commits:  3 across 2 repo(s)") {
		t.Errorf("missing totals:\n%s", out)
	}
}

func TestWeeklySummary_MeetingBreakdown(t *testing.T) {
	s := NewTemplateSummarizer()
	mon := time.Date(2026, 3, 30, 10, 0, 0, 0, time.UTC)
	tue := time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC)

	standupMeta, _ := json.Marshal(calendarMeta{
		DurationMin: 15, MeetingType: "standup", ResponseStatus: "accepted",
	})
	oneOnOneMeta, _ := json.Marshal(calendarMeta{
		DurationMin: 30, MeetingType: "1:1", ResponseStatus: "accepted",
	})
	ceremonyMeta, _ := json.Marshal(calendarMeta{
		DurationMin: 60, MeetingType: "ceremony", ResponseStatus: "accepted",
	})
	focusMeta, _ := json.Marshal(calendarMeta{
		DurationMin: 120, MeetingType: "focus", ResponseStatus: "accepted",
	})
	declinedMeta, _ := json.Marshal(calendarMeta{
		DurationMin: 60, MeetingType: "group", ResponseStatus: "declined",
	})

	activities := []models.Activity{
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Daily Standup", Metadata: string(standupMeta), Timestamp: mon},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "1:1 with Sarah", Metadata: string(oneOnOneMeta), Timestamp: mon.Add(2 * time.Hour)},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Sprint Planning", Metadata: string(ceremonyMeta), Timestamp: tue},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Focus Time", Metadata: string(focusMeta), Timestamp: tue.Add(2 * time.Hour)},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Declined Meeting", Metadata: string(declinedMeta), Timestamp: tue.Add(4 * time.Hour)},
	}

	out, err := s.WeeklySummary(activities)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}

	// Monday: 2 meetings (standup 15min + 1:1 30min = 45min).
	if !strings.Contains(out, "Monday (2026-03-30): 2 meetings (45min)") {
		t.Errorf("missing Monday meetings:\n%s", out)
	}
	// Tuesday: 1 meeting (ceremony 60min — focus and declined skipped).
	if !strings.Contains(out, "Tuesday (2026-03-31): 1 meetings (1h)") {
		t.Errorf("missing Tuesday meetings:\n%s", out)
	}
	// Totals: 3 meetings, 1h45min.
	if !strings.Contains(out, "Meetings: 3 (1h45min)") {
		t.Errorf("missing meeting totals:\n%s", out)
	}
	// Breakdown by type.
	if !strings.Contains(out, "Meeting breakdown") {
		t.Errorf("missing meeting breakdown section:\n%s", out)
	}
	if !strings.Contains(out, "1:1: 30min") {
		t.Errorf("missing 1:1 breakdown:\n%s", out)
	}
	if !strings.Contains(out, "ceremony: 1h") {
		t.Errorf("missing ceremony breakdown:\n%s", out)
	}
	if !strings.Contains(out, "standup: 15min") {
		t.Errorf("missing standup breakdown:\n%s", out)
	}
	// Focus and declined should NOT appear.
	if strings.Contains(out, "focus") {
		t.Errorf("focus time should be excluded:\n%s", out)
	}
}

func TestWeeklySummary_MixedSources(t *testing.T) {
	s := NewTemplateSummarizer()
	mon := time.Date(2026, 3, 30, 10, 0, 0, 0, time.UTC)

	slackMeta, _ := json.Marshal(slackMeta{ChannelName: "backend"})
	meetingMeta, _ := json.Marshal(calendarMeta{
		DurationMin: 60, MeetingType: "ceremony", ResponseStatus: "accepted",
	})

	activities := []models.Activity{
		activity("Fix bug", "backend-api", "abc123", 1, 5, 2, mon),
		{Source: models.SourceSlack, Type: models.TypeMessage, Title: "Discussion", Metadata: string(slackMeta), Timestamp: mon.Add(time.Hour)},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Planning", Metadata: string(meetingMeta), Timestamp: mon.Add(2 * time.Hour)},
	}

	out, err := s.WeeklySummary(activities)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}

	if !strings.Contains(out, "1 commits in backend-api") {
		t.Errorf("missing commits:\n%s", out)
	}
	if !strings.Contains(out, "1 Slack messages") {
		t.Errorf("missing messages:\n%s", out)
	}
	if !strings.Contains(out, "1 meetings (1h)") {
		t.Errorf("missing meetings:\n%s", out)
	}
	if !strings.Contains(out, "Commits:  1 across 1 repo(s)") {
		t.Errorf("missing commit totals:\n%s", out)
	}
	if !strings.Contains(out, "Messages: 1") {
		t.Errorf("missing message totals:\n%s", out)
	}
}

func prActivity(title, repo string, prNum int, shas []string, ts time.Time) models.Activity {
	type prMeta struct {
		Repo       string   `json:"repo"`
		PRNumber   int      `json:"pr_number"`
		State      string   `json:"state"`
		CommitSHAs []string `json:"commit_shas,omitempty"`
		URL        string   `json:"url"`
	}
	m := prMeta{Repo: repo, PRNumber: prNum, State: "merged", CommitSHAs: shas, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, prNum)}
	b, _ := json.Marshal(m)
	return models.Activity{
		Source:    models.SourceGitHub,
		SourceID:  fmt.Sprintf("github:%s:pr:%d", repo, prNum),
		Type:      models.TypePullRequest,
		Title:     title,
		Metadata:  string(b),
		Timestamp: ts,
	}
}

func TestDeduplicateActivities_RemovesLinkedCommits(t *testing.T) {
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	activities := []models.Activity{
		activity("Fix auth bug", "backend-api", "abc123", 3, 47, 12, ts),
		activity("Add tests", "backend-api", "def456", 1, 10, 0, ts.Add(time.Hour)),
		activity("Unrelated commit", "frontend", "xyz789", 2, 20, 5, ts.Add(2*time.Hour)),
		prActivity("Fix auth bug PR", "octocat/backend-api", 42, []string{"abc123", "def456"}, ts.Add(3*time.Hour)),
	}

	result := DeduplicateActivities(activities)

	if len(result) != 2 {
		t.Fatalf("got %d activities, want 2 (2 commits should be deduped)", len(result))
	}

	// The unrelated commit and the PR should remain.
	if result[0].Title != "Unrelated commit" {
		t.Errorf("result[0].Title = %q, want %q", result[0].Title, "Unrelated commit")
	}
	if result[1].Type != models.TypePullRequest {
		t.Errorf("result[1].Type = %q, want %q", result[1].Type, models.TypePullRequest)
	}
}

func TestDeduplicateActivities_NoPRs(t *testing.T) {
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	activities := []models.Activity{
		activity("Fix auth bug", "backend-api", "abc123", 3, 47, 12, ts),
		activity("Add tests", "backend-api", "def456", 1, 10, 0, ts.Add(time.Hour)),
	}

	result := DeduplicateActivities(activities)
	if len(result) != 2 {
		t.Fatalf("got %d activities, want 2 (no dedup without PRs)", len(result))
	}
}

func TestDeduplicateActivities_PRWithoutCommitSHAs(t *testing.T) {
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	// PR with no commit_shas in metadata.
	prNoSHAs := models.Activity{
		Source:    models.SourceGitHub,
		SourceID:  "github:octocat/repo:pr:1",
		Type:      models.TypePullRequest,
		Title:     "Some PR",
		Metadata:  `{"repo":"octocat/repo","pr_number":1,"state":"open"}`,
		Timestamp: ts,
	}

	activities := []models.Activity{
		activity("Fix bug", "backend-api", "abc123", 1, 5, 2, ts),
		prNoSHAs,
	}

	result := DeduplicateActivities(activities)
	if len(result) != 2 {
		t.Fatalf("got %d activities, want 2 (PR has no commit SHAs)", len(result))
	}
}

func TestDeduplicateActivities_Empty(t *testing.T) {
	result := DeduplicateActivities(nil)
	if result != nil {
		t.Errorf("got %v, want nil", result)
	}
}

func TestDeduplicateActivities_PreservesNonCommitActivities(t *testing.T) {
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	slackMeta, _ := json.Marshal(slackMeta{ChannelName: "backend"})
	meetingMeta, _ := json.Marshal(calendarMeta{
		DurationMin: 60, MeetingType: "ceremony", ResponseStatus: "accepted",
	})

	activities := []models.Activity{
		activity("Fix auth bug", "backend-api", "abc123", 3, 47, 12, ts),
		{Source: models.SourceSlack, Type: models.TypeMessage, Title: "Discussion", Metadata: string(slackMeta), Timestamp: ts.Add(time.Hour)},
		{Source: models.SourceCalendar, Type: models.TypeMeeting, Title: "Planning", Metadata: string(meetingMeta), Timestamp: ts.Add(2 * time.Hour)},
		prActivity("Auth fix PR", "octocat/backend-api", 42, []string{"abc123"}, ts.Add(3*time.Hour)),
	}

	result := DeduplicateActivities(activities)
	if len(result) != 3 {
		t.Fatalf("got %d activities, want 3 (only the linked commit removed)", len(result))
	}

	// Verify Slack, Calendar, and PR remain.
	types := map[models.ActivityType]bool{}
	for _, a := range result {
		types[a.Type] = true
	}
	if !types[models.TypeMessage] {
		t.Error("Slack message should not be removed")
	}
	if !types[models.TypeMeeting] {
		t.Error("Calendar meeting should not be removed")
	}
	if !types[models.TypePullRequest] {
		t.Error("PR should not be removed")
	}
}

func reviewActivity(title, repo string, prNum int, state string, ts time.Time) models.Activity {
	type revMeta struct {
		Repo     string `json:"repo"`
		PRNumber int    `json:"pr_number"`
		PRTitle  string `json:"pr_title"`
		State    string `json:"state"`
		URL      string `json:"url"`
	}
	m := revMeta{Repo: repo, PRNumber: prNum, PRTitle: title, State: state, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, prNum)}
	b, _ := json.Marshal(m)
	return models.Activity{
		Source:    models.SourceGitHub,
		SourceID:  fmt.Sprintf("github:%s:review:%d", repo, prNum),
		Type:      models.TypeReview,
		Title:     title,
		Metadata:  string(b),
		Timestamp: ts,
	}
}

func issueActivity(title, repo string, num int, state string, ts time.Time) models.Activity {
	type issMeta struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		State  string `json:"state"`
		URL    string `json:"url"`
	}
	m := issMeta{Repo: repo, Number: num, State: state, URL: fmt.Sprintf("https://github.com/%s/issues/%d", repo, num)}
	b, _ := json.Marshal(m)
	return models.Activity{
		Source:    models.SourceGitHub,
		SourceID:  fmt.Sprintf("github:%s:issue:%d", repo, num),
		Type:      models.TypeIssue,
		Title:     title,
		Metadata:  string(b),
		Timestamp: ts,
	}
}

func TestStandup_ReviewsGroupedUnderCodeReviews(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		reviewActivity("Approved PR #99: Add notifications", "octocat/backend", 99, "APPROVED", ts),
		reviewActivity("Reviewed PR #100: Fix login", "octocat/frontend", 100, "COMMENTED", ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Code Reviews:") {
		t.Errorf("expected Code Reviews group:\n%s", out)
	}
	if !strings.Contains(out, "Approved PR #99: Add notifications (octocat/backend)") {
		t.Errorf("expected review with repo:\n%s", out)
	}
	if !strings.Contains(out, "Reviewed PR #100: Fix login (octocat/frontend)") {
		t.Errorf("expected second review:\n%s", out)
	}
}

func TestStandup_PRsGroupedByRepo(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	pr := prActivity("Fix auth bug", "octocat/backend", 42, nil, ts)
	// Override metadata to include stats.
	type fullPRMeta struct {
		Repo          string `json:"repo"`
		PRNumber      int    `json:"pr_number"`
		State         string `json:"state"`
		CommentsCount int    `json:"comments_count"`
		Additions     int    `json:"additions"`
		Deletions     int    `json:"deletions"`
	}
	m, _ := json.Marshal(fullPRMeta{
		Repo: "octocat/backend", PRNumber: 42, State: "merged",
		CommentsCount: 5, Additions: 47, Deletions: 12,
	})
	pr.Metadata = string(m)

	out, err := s.Standup([]models.Activity{pr})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "octocat/backend: Fix auth bug") {
		t.Errorf("expected PR under repo group:\n%s", out)
	}
	if !strings.Contains(out, "merged") {
		t.Errorf("expected state in stats:\n%s", out)
	}
	if !strings.Contains(out, "+47/-12") {
		t.Errorf("expected additions/deletions:\n%s", out)
	}
	if !strings.Contains(out, "5 comments") {
		t.Errorf("expected comments:\n%s", out)
	}
}

func TestStandup_IssuesGroupedByRepo(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		issueActivity("Login page broken", "octocat/frontend", 10, "open", ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "octocat/frontend: Login page broken") {
		t.Errorf("expected issue under repo group:\n%s", out)
	}
	if !strings.Contains(out, "open") {
		t.Errorf("expected state:\n%s", out)
	}
}

func TestStandup_MixedGitPlatformActivities(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Fix auth bug", "backend-api", "abc123", 3, 47, 12, ts),
		prActivity("Fix auth PR", "octocat/backend-api", 42, []string{"abc123"}, ts.Add(time.Hour)),
		reviewActivity("Approved PR #99: Notifications", "octocat/backend-api", 99, "APPROVED", ts.Add(2*time.Hour)),
		issueActivity("Login broken", "octocat/frontend", 10, "open", ts.Add(3*time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// All types should be present.
	if !strings.Contains(out, "backend-api: Fix auth bug") {
		t.Errorf("missing commit:\n%s", out)
	}
	if !strings.Contains(out, "octocat/backend-api: Fix auth PR") {
		t.Errorf("missing PR:\n%s", out)
	}
	if !strings.Contains(out, "Code Reviews:") {
		t.Errorf("missing Code Reviews section:\n%s", out)
	}
	if !strings.Contains(out, "octocat/frontend: Login broken") {
		t.Errorf("missing issue:\n%s", out)
	}
}

func TestWeeklySummary_WithReviewsAndPRs(t *testing.T) {
	s := NewTemplateSummarizer()
	mon := time.Date(2026, 3, 30, 10, 0, 0, 0, time.UTC)

	activities := []models.Activity{
		activity("Fix bug", "backend-api", "abc123", 1, 5, 2, mon),
		prActivity("Fix bug PR", "octocat/backend-api", 42, nil, mon.Add(time.Hour)),
		reviewActivity("Approved PR #99", "octocat/backend-api", 99, "APPROVED", mon.Add(2*time.Hour)),
		reviewActivity("Reviewed PR #100", "octocat/frontend", 100, "COMMENTED", mon.Add(3*time.Hour)),
		issueActivity("Login broken", "octocat/frontend", 10, "open", mon.Add(4*time.Hour)),
	}

	out, err := s.WeeklySummary(activities)
	if err != nil {
		t.Fatalf("WeeklySummary: %v", err)
	}

	// Day should show counts.
	if !strings.Contains(out, "1 PRs") {
		t.Errorf("missing PR count:\n%s", out)
	}
	if !strings.Contains(out, "2 reviews") {
		t.Errorf("missing review count:\n%s", out)
	}
	if !strings.Contains(out, "1 issues") {
		t.Errorf("missing issue count:\n%s", out)
	}

	// Totals.
	if !strings.Contains(out, "PRs:      1") {
		t.Errorf("missing PR total:\n%s", out)
	}
	if !strings.Contains(out, "Reviews:  2") {
		t.Errorf("missing review total:\n%s", out)
	}
	if !strings.Contains(out, "Issues:   1") {
		t.Errorf("missing issue total:\n%s", out)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		mins int
		want string
	}{
		{15, "15min"},
		{30, "30min"},
		{59, "59min"},
		{60, "1h"},
		{90, "1h30min"},
		{120, "2h"},
		{105, "1h45min"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.mins)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.mins, got, tt.want)
		}
	}
}

func TestStandup_CommitsGroupedByTicketKey(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activityWithKeys("Fix retry logic", "backend-api", "aaa1111", 3, 47, 12, []string{"PROJ-123"}, ts),
		activityWithKeys("Add retry tests", "backend-api", "bbb2222", 1, 10, 0, []string{"PROJ-123"}, ts.Add(time.Hour)),
		activityWithKeys("Update login form", "frontend", "ccc3333", 2, 20, 5, []string{"ENG-456"}, ts.Add(2*time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Commits should be grouped by ticket key, not repo.
	if !strings.Contains(out, "PROJ-123: Fix retry logic") {
		t.Errorf("missing PROJ-123 grouped commit:\n%s", out)
	}
	if !strings.Contains(out, "PROJ-123: Add retry tests") {
		t.Errorf("missing second PROJ-123 commit:\n%s", out)
	}
	if !strings.Contains(out, "ENG-456: Update login form") {
		t.Errorf("missing ENG-456 grouped commit:\n%s", out)
	}
	// Should NOT be grouped by repo name.
	if strings.Contains(out, "backend-api:") {
		t.Errorf("should group by ticket, not repo:\n%s", out)
	}
}

func TestStandup_CommitsWithoutTicketKeyGroupByRepo(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Fix typo", "backend-api", "aaa1111", 1, 1, 1, ts),
		activityWithKeys("PROJ-123: Fix retry", "backend-api", "bbb2222", 3, 47, 12, []string{"PROJ-123"}, ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Commit without ticket key should group by repo.
	if !strings.Contains(out, "backend-api: Fix typo") {
		t.Errorf("commit without ticket should group by repo:\n%s", out)
	}
	// Commit with ticket key should group by ticket.
	if !strings.Contains(out, "PROJ-123: PROJ-123: Fix retry") {
		t.Errorf("commit with ticket should group by ticket key:\n%s", out)
	}
}

func TestStandup_TicketActivitiesGroupedByIssueKey(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		ticketActivity(models.SourceJira, "PROJ-123: Fix payment retry → moved to In Review", "PROJ-123", "In Review", ts),
		activityWithKeys("Fix retry logic", "backend-api", "aaa1111", 3, 47, 12, []string{"PROJ-123"}, ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Both the ticket transition and commit should be under PROJ-123.
	if !strings.Contains(out, "PROJ-123:") {
		t.Errorf("expected PROJ-123 group:\n%s", out)
	}
	if !strings.Contains(out, "→ In Review") {
		t.Errorf("expected status transition:\n%s", out)
	}
	if !strings.Contains(out, "Fix retry logic") {
		t.Errorf("expected commit under ticket group:\n%s", out)
	}
}

func TestStandup_TicketActivityWithoutIssueKey(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	// A ticket activity with empty issue_key falls back to source name.
	act := models.Activity{
		Source:    models.SourceJira,
		SourceID:  "jira:unknown",
		Type:      models.TypeTicket,
		Title:     "Some ticket activity",
		Metadata:  `{}`,
		Timestamp: ts,
	}

	out, err := s.Standup([]models.Activity{act})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "jira: Some ticket activity") {
		t.Errorf("ticket without key should fall back to source:\n%s", out)
	}
}

func TestStandup_LinearTicketGroupedByIdentifier(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	linearMeta, _ := json.Marshal(ticketActivityMeta{Identifier: "ENG-42", ToStatus: "Done"})
	act := models.Activity{
		Source:    models.SourceLinear,
		SourceID:  "linear:eng-42",
		Type:      models.TypeTicket,
		Title:     "ENG-42: Fix auth refresh",
		Metadata:  string(linearMeta),
		Timestamp: ts,
	}

	out, err := s.Standup([]models.Activity{act})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "ENG-42:") {
		t.Errorf("expected ENG-42 group:\n%s", out)
	}
	if !strings.Contains(out, "→ Done") {
		t.Errorf("expected status transition:\n%s", out)
	}
}

func TestStandup_MixedTicketsAndPRs(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		ticketActivity(models.SourceJira, "PROJ-123: Fix payment retry → moved to In Review", "PROJ-123", "In Review", ts),
		activityWithKeys("Fix retry logic", "backend-api", "aaa1111", 3, 47, 12, []string{"PROJ-123"}, ts.Add(time.Hour)),
		prActivity("Fix payment retry PR", "octocat/backend-api", 42, []string{"aaa1111"}, ts.Add(2*time.Hour)),
		activity("Fix typo in README", "docs", "zzz9999", 1, 1, 0, ts.Add(3*time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Ticket + linked commit under PROJ-123.
	if !strings.Contains(out, "PROJ-123:") {
		t.Errorf("expected PROJ-123 group:\n%s", out)
	}
	// PR grouped by repo.
	if !strings.Contains(out, "octocat/backend-api:") {
		t.Errorf("expected PR grouped by repo:\n%s", out)
	}
	// Unlinked commit grouped by repo.
	if !strings.Contains(out, "docs: Fix typo in README") {
		t.Errorf("expected unlinked commit grouped by repo:\n%s", out)
	}
}

func TestStandup_SprintContextFromJira(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		ticketActivityWithSprint(models.SourceJira, "PROJ-123: Fix payment retry → In Review", "PROJ-123", "In Review", "Sprint 14", ts),
		activityWithKeys("Fix retry logic", "backend-api", "aaa1111", 3, 47, 12, []string{"PROJ-123"}, ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.HasPrefix(out, "Sprint: Sprint 14") {
		t.Errorf("expected sprint context at top:\n%s", out)
	}
	if !strings.Contains(out, "PROJ-123:") {
		t.Errorf("expected ticket group:\n%s", out)
	}
}

func TestStandup_CycleContextFromLinear(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		ticketActivityWithCycle(models.SourceLinear, "ENG-42: Fix auth refresh → Done", "ENG-42", "Done", "Cycle 7", ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.HasPrefix(out, "Sprint: Cycle 7") {
		t.Errorf("expected cycle context at top:\n%s", out)
	}
}

func TestStandup_MultipleSprintsAndCycles(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		ticketActivityWithSprint(models.SourceJira, "PROJ-123: Fix retry → In Review", "PROJ-123", "In Review", "Sprint 14", ts),
		ticketActivityWithCycle(models.SourceLinear, "ENG-42: Fix auth → Done", "ENG-42", "Done", "Cycle 7", ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Both should appear, sorted alphabetically.
	if !strings.Contains(out, "Sprint: Cycle 7, Sprint 14") {
		t.Errorf("expected both sprint and cycle:\n%s", out)
	}
}

func TestStandup_DuplicateSprintDeduped(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		ticketActivityWithSprint(models.SourceJira, "PROJ-123: Fix retry → In Review", "PROJ-123", "In Review", "Sprint 14", ts),
		ticketActivityWithSprint(models.SourceJira, "PROJ-456: Update docs → Done", "PROJ-456", "Done", "Sprint 14", ts.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Sprint 14 should appear only once.
	if !strings.HasPrefix(out, "Sprint: Sprint 14\n") {
		t.Errorf("expected single Sprint 14:\n%s", out)
	}
}

func TestStandup_NoSprintContextWithoutTickets(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Fix typo", "backend-api", "aaa1111", 1, 1, 1, ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if strings.HasPrefix(out, "Sprint:") {
		t.Errorf("should not show sprint context without ticket activities:\n%s", out)
	}
}

func TestStandup_NoSprintContextWhenSprintEmpty(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	// Ticket activity without sprint/cycle metadata.
	out, err := s.Standup([]models.Activity{
		ticketActivity(models.SourceJira, "PROJ-123: Fix retry → Done", "PROJ-123", "Done", ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if strings.HasPrefix(out, "Sprint:") {
		t.Errorf("should not show sprint context when no sprint set:\n%s", out)
	}
}
