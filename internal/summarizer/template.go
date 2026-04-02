package summarizer

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

// commitMeta mirrors the JSON metadata stored by the git collector.
type commitMeta struct {
	Repo         string `json:"repo"`
	SHA          string `json:"sha"`
	FilesChanged int    `json:"files_changed"`
	Insertions   int    `json:"insertions"`
	Deletions    int    `json:"deletions"`
}

// slackMeta mirrors the JSON metadata stored by the Slack collector.
type slackMeta struct {
	ChannelName string             `json:"channel_name"`
	ReplyCount  int                `json:"reply_count,omitempty"`
	Summary     *slackThreadSummary `json:"summary,omitempty"`
}

// slackThreadSummary mirrors the ThreadSummary from the Slack collector.
type slackThreadSummary struct {
	Topic     string   `json:"topic"`
	Decisions []string `json:"decisions,omitempty"`
}

// calendarMeta mirrors the JSON metadata stored by the calendar collector.
type calendarMeta struct {
	Start          string      `json:"start"`
	DurationMin    int         `json:"duration_minutes"`
	MeetingType    string      `json:"meeting_type"`
	Attendees      []attendee  `json:"attendees,omitempty"`
	ResponseStatus string      `json:"response_status"`
	IsAllDay       bool        `json:"is_all_day,omitempty"`
}

type attendee struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

// entry holds a formatted activity for grouping.
type entry struct {
	title     string
	stats     string
	decisions []string
}

// todayMeeting holds an upcoming meeting for the "Today" section.
type todayMeeting struct {
	time     string // "10:00" or "All day"
	title    string
	duration string // "1h", "30min"
}

// TemplateSummarizer generates standups using plain-text templates (no LLM).
type TemplateSummarizer struct{}

// NewTemplateSummarizer creates a template-based summarizer.
func NewTemplateSummarizer() *TemplateSummarizer {
	return &TemplateSummarizer{}
}

// Standup generates a standup report grouped by date and repo.
func (s *TemplateSummarizer) Standup(activities []models.Activity) (string, error) {
	if len(activities) == 0 {
		return "No activity found for the given period.", nil
	}

	// Group by date, then by repo.
	// date string -> repo -> entries
	grouped := make(map[string]map[string][]entry)
	var dateOrder []string
	datesSeen := make(map[string]bool)

	// Track meeting minutes per date for the summary line.
	meetingMinutes := make(map[string]int)

	// Separate today's calendar events for the "Today" schedule section.
	todayStr := time.Now().Format("2006-01-02")
	var todayMeetings []todayMeeting

	for _, a := range activities {
		dateStr := a.Timestamp.Format("2006-01-02")

		// Today's calendar events go into the schedule section, not the activity list.
		localDateStr := a.Timestamp.Local().Format("2006-01-02")
		if a.Source == models.SourceCalendar && localDateStr == todayStr {
			var meta calendarMeta
			json.Unmarshal([]byte(a.Metadata), &meta)

			if meta.MeetingType == "focus" || meta.ResponseStatus == "declined" {
				continue
			}

			timeStr := "All day"
			if !meta.IsAllDay && meta.Start != "" {
				if t, err := time.Parse(time.RFC3339, meta.Start); err == nil {
					timeStr = t.Local().Format("15:04")
				}
			}

			todayMeetings = append(todayMeetings, todayMeeting{
				time:     timeStr,
				title:    a.Title,
				duration: formatDuration(meta.DurationMin),
			})
			continue
		}

		if !datesSeen[dateStr] {
			datesSeen[dateStr] = true
			dateOrder = append(dateOrder, dateStr)
		}

		var group string
		var e entry

		switch a.Source {
		case models.SourceSlack:
			var meta slackMeta
			json.Unmarshal([]byte(a.Metadata), &meta)
			group = "#" + meta.ChannelName
			if group == "#" {
				group = "#slack"
			}
			if meta.Summary != nil && meta.Summary.Topic != "" {
				e = entry{title: meta.Summary.Topic}
				if len(meta.Summary.Decisions) > 0 {
					e.decisions = meta.Summary.Decisions
				}
			} else if a.Content != "" {
				e = entry{title: a.Content}
			} else {
				e = entry{title: a.Title}
			}
		case models.SourceCalendar:
			var meta calendarMeta
			json.Unmarshal([]byte(a.Metadata), &meta)

			// Skip focus time blocks — not relevant for standup.
			if meta.MeetingType == "focus" {
				continue
			}

			// Skip declined meetings.
			if meta.ResponseStatus == "declined" {
				continue
			}

			group = "Meetings"
			e = entry{
				title: a.Title,
				stats: formatMeetingStats(meta),
			}
			meetingMinutes[dateStr] += meta.DurationMin
		default:
			var meta commitMeta
			json.Unmarshal([]byte(a.Metadata), &meta)
			group = meta.Repo
			if group == "" {
				group = "unknown"
			}
			e = entry{title: a.Title, stats: formatStats(meta)}
		}

		if grouped[dateStr] == nil {
			grouped[dateStr] = make(map[string][]entry)
		}
		grouped[dateStr][group] = append(grouped[dateStr][group], e)
	}

	var b strings.Builder
	for i, dateStr := range dateOrder {
		if i > 0 {
			b.WriteString("\n")
		}
		t, _ := time.Parse("2006-01-02", dateStr)
		b.WriteString(formatDateHeader(t))
		b.WriteString("\n")

		repos := grouped[dateStr]
		// Collect repo names and sort for deterministic output.
		groupNames := sortedKeys(repos)
		for _, group := range groupNames {
			for _, e := range repos[group] {
				var shortSHA string
				if !strings.HasPrefix(group, "#") && group != "Meetings" {
					shortSHA = extractShortSHA(activities, group, e.title)
				}
				b.WriteString(formatBullet(group, e.title, shortSHA, e.stats))
				b.WriteString("\n")
				for _, d := range e.decisions {
					b.WriteString("  → ")
					b.WriteString(d)
					b.WriteString("\n")
				}
			}
		}

		if mins := meetingMinutes[dateStr]; mins > 0 {
			b.WriteString(fmt.Sprintf("⏱ %s in meetings\n", formatDuration(mins)))
		}
	}

	// Render "Today" section with upcoming meetings.
	if len(todayMeetings) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		today, _ := time.Parse("2006-01-02", todayStr)
		b.WriteString(fmt.Sprintf("Today (%s):\n", today.Format("2006-01-02")))
		for _, m := range todayMeetings {
			fmt.Fprintf(&b, "- %s — %s (%s)\n", m.time, m.title, m.duration)
		}
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

func formatDateHeader(t time.Time) string {
	weekday := t.Weekday().String()
	return fmt.Sprintf("%s (%s):", weekday, t.Format("2006-01-02"))
}

func formatStats(m commitMeta) string {
	if m.FilesChanged == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d files", m.FilesChanged)}
	if m.Insertions > 0 || m.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d/-%d", m.Insertions, m.Deletions))
	}
	return strings.Join(parts, ", ")
}

func formatBullet(repo, title, shortSHA, stats string) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(repo)
	b.WriteString(": ")
	b.WriteString(title)
	if shortSHA != "" {
		b.WriteString(" (")
		b.WriteString(shortSHA)
		b.WriteString(")")
	}
	if stats != "" {
		b.WriteString(" — ")
		b.WriteString(stats)
	}
	return b.String()
}

// extractShortSHA finds the short SHA for a commit from activities metadata.
func extractShortSHA(activities []models.Activity, repo, title string) string {
	for _, a := range activities {
		if a.Title != title {
			continue
		}
		var meta commitMeta
		json.Unmarshal([]byte(a.Metadata), &meta)
		if meta.Repo == repo && meta.SHA != "" {
			if len(meta.SHA) > 7 {
				return meta.SHA[:7]
			}
			return meta.SHA
		}
	}
	return ""
}

func formatMeetingStats(m calendarMeta) string {
	parts := []string{formatDuration(m.DurationMin)}
	if m.MeetingType != "" && m.MeetingType != "group" {
		parts = append(parts, m.MeetingType)
	}
	nonSelfCount := 0
	for _, a := range m.Attendees {
		if !a.Self {
			nonSelfCount++
		}
	}
	if nonSelfCount > 0 {
		parts = append(parts, fmt.Sprintf("%d attendees", nonSelfCount))
	}
	return strings.Join(parts, ", ")
}

func formatDuration(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%dmin", minutes)
	}
	h := minutes / 60
	m := minutes % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dmin", h, m)
}

func sortedKeys(m map[string][]entry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort — repos per day will be small.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
