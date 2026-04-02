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

func activity(title, repo, sha string, files, ins, del int, ts time.Time) models.Activity {
	return models.Activity{
		Source:    models.SourceGit,
		Type:     models.TypeCommit,
		Title:    title,
		Metadata: meta(repo, sha, files, ins, del),
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
