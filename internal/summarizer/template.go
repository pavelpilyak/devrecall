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
	Repo         string   `json:"repo"`
	SHA          string   `json:"sha"`
	FilesChanged int      `json:"files_changed"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
	IssueKeys    []string `json:"issue_keys,omitempty"`
}

// ticketActivityMeta is a minimal struct for parsing ticket metadata from Jira/Linear.
type ticketActivityMeta struct {
	IssueKey   string `json:"issue_key"`   // Jira
	Identifier string `json:"identifier"`  // Linear
	FromStatus string `json:"from_status"` // status transitions
	ToStatus   string `json:"to_status"`
	Sprint     string `json:"sprint,omitempty"` // Jira sprint name
	Cycle      string `json:"cycle,omitempty"`  // Linear cycle name
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

// reviewActivityMeta is a unified struct for parsing review metadata across platforms.
type reviewActivityMeta struct {
	Repo     string `json:"repo"`    // GitHub, Bitbucket
	Project  string `json:"project"` // GitLab
	PRNumber int    `json:"pr_number"`
	MRNumber int    `json:"mr_number"`
	PRTitle  string `json:"pr_title"`
	MRTitle  string `json:"mr_title"`
	State    string `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED
	Approved bool   `json:"approved"`
	URL      string `json:"url"`
}

func (m reviewActivityMeta) repoName() string {
	if m.Repo != "" {
		return m.Repo
	}
	return m.Project
}

// prActivityMeta is a unified struct for parsing PR/MR metadata across platforms.
type prActivityMeta struct {
	Repo          string   `json:"repo"`    // GitHub, Bitbucket
	Project       string   `json:"project"` // GitLab
	PRNumber      int      `json:"pr_number"`
	MRNumber      int      `json:"mr_number"`
	State         string   `json:"state"`
	CommentsCount int      `json:"comments_count"`
	Additions     int      `json:"additions"`
	Deletions     int      `json:"deletions"`
	URL           string   `json:"url"`
}

func (m prActivityMeta) repoName() string {
	if m.Repo != "" {
		return m.Repo
	}
	return m.Project
}

func (m prActivityMeta) number() int {
	if m.PRNumber != 0 {
		return m.PRNumber
	}
	return m.MRNumber
}

// issueActivityMeta is a unified struct for parsing issue metadata across platforms.
type issueActivityMeta struct {
	Repo    string   `json:"repo"`    // GitHub
	Project string   `json:"project"` // GitLab
	Number  int      `json:"number"`
	State   string   `json:"state"`
	Labels  []string `json:"labels,omitempty"`
	URL     string   `json:"url"`
}

func (m issueActivityMeta) repoName() string {
	if m.Repo != "" {
		return m.Repo
	}
	return m.Project
}

// prCommitSHAs is a minimal struct to extract commit SHAs from PR/MR metadata.
type prCommitSHAs struct {
	CommitSHAs []string `json:"commit_shas"`
}

// DeduplicateActivities removes git commits that are already represented by a
// PR or MR activity. When both a local commit and a remote PR reference the
// same SHA, the commit is redundant — the PR provides richer context.
func DeduplicateActivities(activities []models.Activity) []models.Activity {
	// Collect all commit SHAs referenced by PR/MR activities.
	linkedSHAs := make(map[string]bool)
	for _, a := range activities {
		if a.Type != models.TypePullRequest && a.Type != models.TypeMergeRequest {
			continue
		}
		var meta prCommitSHAs
		if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
			continue
		}
		for _, sha := range meta.CommitSHAs {
			linkedSHAs[sha] = true
		}
	}

	if len(linkedSHAs) == 0 {
		return activities
	}

	// Filter out git commits whose SHA matches a linked PR commit.
	result := make([]models.Activity, 0, len(activities))
	for _, a := range activities {
		if a.Type == models.TypeCommit {
			var meta commitMeta
			if err := json.Unmarshal([]byte(a.Metadata), &meta); err == nil && linkedSHAs[meta.SHA] {
				continue // skip — this commit is covered by a PR/MR
			}
		}
		result = append(result, a)
	}
	return result
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

	// Track active sprints/cycles for the context line.
	sprintCycles := make(map[string]bool)

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
			switch a.Type {
			case models.TypeReview:
				var meta reviewActivityMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				group = "Code Reviews"
				repo := meta.repoName()
				if repo != "" {
					e = entry{title: fmt.Sprintf("%s (%s)", a.Title, repo)}
				} else {
					e = entry{title: a.Title}
				}
			case models.TypePullRequest, models.TypeMergeRequest:
				var meta prActivityMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				group = meta.repoName()
				if group == "" {
					group = "unknown"
				}
				e = entry{title: a.Title, stats: formatPRStats(meta)}
			case models.TypeIssue:
				var meta issueActivityMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				group = meta.repoName()
				if group == "" {
					group = "unknown"
				}
				e = entry{title: a.Title, stats: meta.State}
			case models.TypeTicket:
				var meta ticketActivityMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				key := meta.IssueKey
				if key == "" {
					key = meta.Identifier
				}
				if key != "" {
					group = key
				} else {
					group = string(a.Source)
				}
				e = entry{title: a.Title}
				if meta.ToStatus != "" {
					e.stats = "→ " + meta.ToStatus
				}
				if meta.Sprint != "" {
					sprintCycles[meta.Sprint] = true
				}
				if meta.Cycle != "" {
					sprintCycles[meta.Cycle] = true
				}
			default:
				var meta commitMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				if len(meta.IssueKeys) > 0 {
					group = meta.IssueKeys[0]
				} else {
					group = meta.Repo
				}
				if group == "" {
					group = "unknown"
				}
				e = entry{title: a.Title, stats: formatStats(meta)}
			}
		}

		if grouped[dateStr] == nil {
			grouped[dateStr] = make(map[string][]entry)
		}
		grouped[dateStr][group] = append(grouped[dateStr][group], e)
	}

	var b strings.Builder

	// Render sprint/cycle context line if any ticket activities reference one.
	if len(sprintCycles) > 0 {
		names := sortedMapKeys(sprintCycles)
		b.WriteString("Sprint: ")
		b.WriteString(strings.Join(names, ", "))
		b.WriteString("\n\n")
	}

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

func formatPRStats(m prActivityMeta) string {
	var parts []string
	if m.State != "" {
		parts = append(parts, m.State)
	}
	if m.Additions > 0 || m.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d/-%d", m.Additions, m.Deletions))
	}
	if m.CommentsCount > 0 {
		parts = append(parts, fmt.Sprintf("%d comments", m.CommentsCount))
	}
	return strings.Join(parts, ", ")
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

// meetingTypeBreakdown holds per-type meeting time.
type meetingTypeBreakdown struct {
	meetingType string
	minutes     int
}

// WeeklySummary generates a weekly summary with per-day breakdown and meeting time stats.
func (s *TemplateSummarizer) WeeklySummary(activities []models.Activity) (string, error) {
	if len(activities) == 0 {
		return "No activity found for the given period.", nil
	}

	// Group activities by date.
	type dayData struct {
		commits        int
		messages       int
		meetings       int
		reviews        int
		prs            int
		issues         int
		meetingMinutes int
		meetingByType  map[string]int
		repos          map[string]bool
	}

	days := make(map[string]*dayData)
	var dateOrder []string
	datesSeen := make(map[string]bool)

	totalCommits := 0
	totalMessages := 0
	totalMeetings := 0
	totalReviews := 0
	totalPRs := 0
	totalIssues := 0
	totalMeetingMinutes := 0
	totalMeetingByType := make(map[string]int)
	allRepos := make(map[string]bool)

	for _, a := range activities {
		dateStr := a.Timestamp.Format("2006-01-02")
		if !datesSeen[dateStr] {
			datesSeen[dateStr] = true
			dateOrder = append(dateOrder, dateStr)
			days[dateStr] = &dayData{
				meetingByType: make(map[string]int),
				repos:         make(map[string]bool),
			}
		}
		d := days[dateStr]

		switch a.Source {
		case models.SourceGit:
			d.commits++
			totalCommits++
			var meta commitMeta
			json.Unmarshal([]byte(a.Metadata), &meta)
			if meta.Repo != "" {
				d.repos[meta.Repo] = true
				allRepos[meta.Repo] = true
			}
		case models.SourceSlack:
			d.messages++
			totalMessages++
		case models.SourceCalendar:
			var meta calendarMeta
			json.Unmarshal([]byte(a.Metadata), &meta)
			if meta.MeetingType == "focus" || meta.ResponseStatus == "declined" {
				continue
			}
			d.meetings++
			totalMeetings++
			d.meetingMinutes += meta.DurationMin
			totalMeetingMinutes += meta.DurationMin
			mtype := meta.MeetingType
			if mtype == "" {
				mtype = "group"
			}
			d.meetingByType[mtype] += meta.DurationMin
			totalMeetingByType[mtype] += meta.DurationMin
		default:
			switch a.Type {
			case models.TypeReview:
				d.reviews++
				totalReviews++
			case models.TypePullRequest, models.TypeMergeRequest:
				d.prs++
				totalPRs++
			case models.TypeIssue:
				d.issues++
				totalIssues++
			}
		}
	}

	var b strings.Builder
	b.WriteString("Weekly Summary\n")
	b.WriteString("==============\n\n")

	// Per-day breakdown.
	for _, dateStr := range dateOrder {
		d := days[dateStr]
		t, _ := time.Parse("2006-01-02", dateStr)
		b.WriteString(fmt.Sprintf("%s (%s):", t.Weekday(), dateStr))

		var parts []string
		if d.commits > 0 {
			repoNames := sortedMapKeys(d.repos)
			parts = append(parts, fmt.Sprintf("%d commits in %s", d.commits, strings.Join(repoNames, ", ")))
		}
		if d.prs > 0 {
			parts = append(parts, fmt.Sprintf("%d PRs", d.prs))
		}
		if d.reviews > 0 {
			parts = append(parts, fmt.Sprintf("%d reviews", d.reviews))
		}
		if d.issues > 0 {
			parts = append(parts, fmt.Sprintf("%d issues", d.issues))
		}
		if d.messages > 0 {
			parts = append(parts, fmt.Sprintf("%d Slack messages", d.messages))
		}
		if d.meetings > 0 {
			parts = append(parts, fmt.Sprintf("%d meetings (%s)", d.meetings, formatDuration(d.meetingMinutes)))
		}

		if len(parts) > 0 {
			b.WriteString(" ")
			b.WriteString(strings.Join(parts, ", "))
		} else {
			b.WriteString(" no activity")
		}
		b.WriteString("\n")
	}

	// Totals.
	b.WriteString("\nTotals\n------\n")
	b.WriteString(fmt.Sprintf("Commits:  %d", totalCommits))
	if len(allRepos) > 0 {
		b.WriteString(fmt.Sprintf(" across %d repo(s)", len(allRepos)))
	}
	b.WriteString("\n")
	if totalPRs > 0 {
		b.WriteString(fmt.Sprintf("PRs:      %d\n", totalPRs))
	}
	if totalReviews > 0 {
		b.WriteString(fmt.Sprintf("Reviews:  %d\n", totalReviews))
	}
	if totalIssues > 0 {
		b.WriteString(fmt.Sprintf("Issues:   %d\n", totalIssues))
	}
	b.WriteString(fmt.Sprintf("Messages: %d\n", totalMessages))
	b.WriteString(fmt.Sprintf("Meetings: %d (%s)\n", totalMeetings, formatDuration(totalMeetingMinutes)))

	// Meeting time breakdown by type.
	if len(totalMeetingByType) > 0 {
		b.WriteString("\nMeeting breakdown\n-----------------\n")
		typeNames := sortedMapKeys2(totalMeetingByType)
		for _, mtype := range typeNames {
			mins := totalMeetingByType[mtype]
			b.WriteString(fmt.Sprintf("  %s: %s\n", mtype, formatDuration(mins)))
		}
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func sortedMapKeys2(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// BragDoc generates a template-based brag document (fallback when LLM unavailable).
func (t *TemplateSummarizer) BragDoc(activities []models.Activity, childSummaries []models.Summary) (string, error) {
	var b strings.Builder

	// If we have child summaries, use them.
	if len(childSummaries) > 0 {
		b.WriteString("## Period Summaries\n\n")
		for _, s := range childSummaries {
			fmt.Fprintf(&b, "### %s (%s to %s)\n%s\n\n", s.PeriodType, s.PeriodStart, s.PeriodEnd, s.SummaryText)
		}
	}

	// Count activity stats.
	if len(activities) > 0 {
		bySource := make(map[models.Source]int)
		byType := make(map[models.ActivityType]int)
		for _, a := range activities {
			bySource[a.Source]++
			byType[a.Type]++
		}

		b.WriteString("## Metrics\n\n")
		for source, count := range bySource {
			fmt.Fprintf(&b, "- %s: %d activities\n", source, count)
		}
		b.WriteString("\n")

		if commits := byType[models.TypeCommit]; commits > 0 {
			fmt.Fprintf(&b, "- %d commits\n", commits)
		}
		if prs := byType[models.TypePullRequest] + byType[models.TypeMergeRequest]; prs > 0 {
			fmt.Fprintf(&b, "- %d PRs/MRs\n", prs)
		}
		if reviews := byType[models.TypeReview]; reviews > 0 {
			fmt.Fprintf(&b, "- %d code reviews\n", reviews)
		}
		if meetings := byType[models.TypeMeeting]; meetings > 0 {
			fmt.Fprintf(&b, "- %d meetings\n", meetings)
		}
		b.WriteString("\n")
	}

	if b.Len() == 0 {
		return "No activities found for this period.", nil
	}

	return b.String(), nil
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
