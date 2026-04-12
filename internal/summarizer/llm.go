package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

const standupSystemPrompt = `You are a developer standup report generator. Given a list of work activities (commits, Slack messages, meetings, etc.), write a concise, natural-language standup summary.

Rules:
- Group related work together (e.g., multiple commits on the same feature)
- This is a FIRST-PERSON standup: only report what "You" did, asked, or decided — not what others said
- In Slack threads, messages from "You" are the standup author; other participants are teammates
- Accurately reflect the tense: if something was planned or will be done, use future tense; if completed, use past tense
- Keep each bullet point to 1-2 sentences
- Highlight key decisions from Slack threads, but attribute them correctly
- Don't include commit SHAs or raw file counts — focus on what was accomplished
- When activities include [link](url) references, preserve them inline so the reader can click through to the source (PR, Slack thread, calendar event, ticket, etc.)
- For meetings: include the meeting name, duration, and type (1:1, standup, ceremony, etc.) when useful
- Skip focus time blocks — they're not relevant for standup
- Skip declined meetings — they weren't attended
- If there are multiple days, separate them with date headers
- At the end of each day, include total time spent in meetings if there were any
- If there's a "Today" section with upcoming meetings, format them as a schedule with times
- Be concise and professional — this is for a team standup

Respond ONLY with the standup text, no preamble or explanation.`

const weeklySystemPrompt = `You are a developer weekly summary generator. Given a list of work activities (commits, Slack messages, meetings, etc.), write a concise weekly summary.

Rules:
- Start with a brief 2-3 sentence overview of the week
- Group work by themes/projects, not by day
- Include key accomplishments and decisions made
- When activities include [link](url) references, preserve them inline so the reader can click through to the source
- Include a meeting time breakdown at the end: total hours in meetings, broken down by meeting type (1:1, standup, ceremony, etc.)
- Skip focus time blocks and declined meetings
- Mention collaboration highlights (who you worked with most)
- Be concise and professional
- Format meeting stats clearly at the end

Respond ONLY with the weekly summary text, no preamble or explanation.`

// LLMSummarizer generates standups using an LLM provider.
type LLMSummarizer struct {
	provider llm.Provider
	fallback *TemplateSummarizer
	selfUIDs []string       // user IDs across sources (e.g., Slack UID) to label "You" in prompts
	prompts  *PromptLoader  // optional custom prompt loader
}

// NewLLMSummarizer creates an LLM-powered summarizer with template fallback.
func NewLLMSummarizer(provider llm.Provider) *LLMSummarizer {
	return &LLMSummarizer{
		provider: provider,
		fallback: NewTemplateSummarizer(),
		prompts:  NewPromptLoader(""), // built-in only by default
	}
}

// WithSelfUIDs sets user IDs that identify the standup author across sources.
func (s *LLMSummarizer) WithSelfUIDs(uids ...string) *LLMSummarizer {
	s.selfUIDs = uids
	return s
}

// WithPromptLoader sets a custom prompt loader for user-customizable templates.
func (s *LLMSummarizer) WithPromptLoader(loader *PromptLoader) *LLMSummarizer {
	s.prompts = loader
	return s
}

// Standup generates a standup report using the LLM.
// Falls back to template-based generation on LLM failure.
func (s *LLMSummarizer) Standup(activities []models.Activity) (string, error) {
	if len(activities) == 0 {
		return "No activity found for the given period.", nil
	}

	selfSet := make(map[string]bool, len(s.selfUIDs))
	for _, uid := range s.selfUIDs {
		selfSet[uid] = true
	}
	prompt := buildActivitiesPrompt(activities, selfSet)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := s.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: s.prompts.Load(PromptStandup)},
		{Role: "user", Content: prompt},
	}, llm.ChatOpts{
		Temperature: 0.3,
		MaxTokens:   1024,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM standup generation failed: %v (falling back to template)\n", err)
		return s.fallback.Standup(activities)
	}

	resp = strings.TrimSpace(resp)
	if resp == "" {
		return s.fallback.Standup(activities)
	}

	return resp, nil
}

// WeeklySummary generates a weekly summary using the LLM.
// Falls back to template-based generation on LLM failure.
func (s *LLMSummarizer) WeeklySummary(activities []models.Activity) (string, error) {
	if len(activities) == 0 {
		return "No activity found for the given period.", nil
	}

	selfSet := make(map[string]bool, len(s.selfUIDs))
	for _, uid := range s.selfUIDs {
		selfSet[uid] = true
	}
	prompt := buildActivitiesPrompt(activities, selfSet)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := s.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: s.prompts.Load(PromptWeekly)},
		{Role: "user", Content: prompt},
	}, llm.ChatOpts{
		Temperature: 0.3,
		MaxTokens:   2048,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM weekly summary failed: %v (falling back to template)\n", err)
		return s.fallback.WeeklySummary(activities)
	}

	resp = strings.TrimSpace(resp)
	if resp == "" {
		return s.fallback.WeeklySummary(activities)
	}

	return resp, nil
}

const bragDocSystemPrompt = `You are a developer brag document generator. Given work activities and/or period summaries, write a comprehensive brag document highlighting key accomplishments.

Format the document in Markdown with these sections:
## Key Deliverables
- List major features, projects, or initiatives completed. Include ticket IDs and repo names when available.
- Emphasize outcomes and impact, not just tasks completed.

## Collaboration & Mentorship
- PRs reviewed for teammates
- Design reviews led or participated in
- Cross-team discussions and decisions driven

## Metrics
- Commits, PRs merged, PRs reviewed
- Meetings attended (with time breakdown)
- Channels/threads participated in

## Notable Decisions & Technical Leadership
- Key technical decisions made or influenced
- Architecture changes proposed or implemented

Rules:
- Be specific: cite dates, repo names, ticket IDs, and people when available.
- When activities include [link](url) references, preserve them inline so the reader can click through to PRs, tickets, Slack threads, etc.
- Focus on impact and outcomes, not just activity volume.
- Use professional language suitable for sharing with management.
- If data is limited, work with what's available — don't pad or fabricate.

Respond ONLY with the brag document text in Markdown, no preamble or explanation.`

// BragDoc generates a brag document from activities and child summaries.
// Falls back to template-based generation on LLM failure.
func (s *LLMSummarizer) BragDoc(activities []models.Activity, childSummaries []models.Summary) (string, error) {
	prompt := buildBragPrompt(activities, childSummaries)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := s.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: s.prompts.Load(PromptBragDoc)},
		{Role: "user", Content: prompt},
	}, llm.ChatOpts{
		Temperature: 0.3,
		MaxTokens:   4096,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM brag doc generation failed: %v (falling back to template)\n", err)
		return s.fallback.BragDoc(activities, childSummaries)
	}

	resp = strings.TrimSpace(resp)
	if resp == "" {
		return s.fallback.BragDoc(activities, childSummaries)
	}

	return resp, nil
}

func buildBragPrompt(activities []models.Activity, childSummaries []models.Summary) string {
	var b strings.Builder

	if len(childSummaries) > 0 {
		b.WriteString("Period summaries:\n\n")
		for _, s := range childSummaries {
			fmt.Fprintf(&b, "=== %s (%s to %s) ===\n%s\n\n",
				s.PeriodType, s.PeriodStart, s.PeriodEnd, s.SummaryText)
		}
	}

	if len(activities) > 0 {
		b.WriteString("Raw activities:\n\n")
		selfSet := make(map[string]bool)
		b.WriteString(buildActivitiesPrompt(activities, selfSet))
	}

	if b.Len() == 0 {
		return "No activities or summaries available for this period."
	}

	return b.String()
}

const perfReviewSystemPrompt = `You are a developer performance review document generator. Given work activities and/or period summaries, write a structured performance review document.

Format the document in Markdown with these sections:

## Key Contributions
- Major deliverables with measurable impact where possible
- Cite ticket IDs, repo names, and specific outcomes

## Technical Leadership
- Architecture decisions made or influenced
- Technical debt addressed
- Quality improvements (testing, CI/CD, monitoring)

## Collaboration & Mentorship
- Code reviews: volume and quality signals (PRs reviewed, turnaround)
- Knowledge sharing: design reviews led, documentation written
- Team support: unblocking teammates, onboarding help

## Evidence & Metrics
- Quantitative: commits, PRs merged, PRs reviewed, meetings
- Qualitative: key decisions, process improvements
- Cross-team impact: channels participated in, external discussions

## Growth Areas
- New technologies or domains explored
- Skills demonstrated for the first time this period

Rules:
- Be specific and evidence-based — cite dates, repos, tickets, and people.
- When activities include [link](url) references, preserve them inline so the reader can click through to PRs, tickets, Slack threads, etc.
- Frame contributions in terms of impact, not just output.
- Use professional language suitable for manager review or self-assessment.
- Distinguish between individual contributions and team outcomes you influenced.
- If data is limited, work with what's available — don't fabricate.

Respond ONLY with the performance review document in Markdown, no preamble or explanation.`

// PerfReview generates a structured performance review document.
// Falls back to template-based generation on LLM failure.
func (s *LLMSummarizer) PerfReview(activities []models.Activity, childSummaries []models.Summary) (string, error) {
	prompt := buildBragPrompt(activities, childSummaries) // reuse the same prompt builder

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := s.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: s.prompts.Load(PromptPerfReview)},
		{Role: "user", Content: prompt},
	}, llm.ChatOpts{
		Temperature: 0.3,
		MaxTokens:   4096,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM perf review generation failed: %v (falling back to template)\n", err)
		return s.fallback.PerfReview(activities, childSummaries)
	}

	resp = strings.TrimSpace(resp)
	if resp == "" {
		return s.fallback.PerfReview(activities, childSummaries)
	}

	return resp, nil
}

// slackFullMeta extends slackMeta with thread messages for the LLM prompt.
type slackFullMeta struct {
	ChannelName string             `json:"channel_name"`
	ReplyCount  int                `json:"reply_count,omitempty"`
	Summary     *slackThreadSummary `json:"summary,omitempty"`
	Permalink   string             `json:"permalink,omitempty"`
	ThreadMsgs  []struct {
		User string `json:"user"`
		Text string `json:"text"`
	} `json:"thread_msgs,omitempty"`
}

// extractURL pulls a source link from an activity's metadata JSON.
// Most sources store a "url" field; Slack uses "permalink"; Calendar URLs are
// constructed from event_id + calendar_id.
func extractURL(a models.Activity) string {
	if a.Metadata == "" {
		return ""
	}

	// Fast path: most sources use a "url" field.
	var generic struct {
		URL       string `json:"url"`
		Permalink string `json:"permalink"`
		EventID   string `json:"event_id"`
	}
	if err := json.Unmarshal([]byte(a.Metadata), &generic); err != nil {
		return ""
	}

	if generic.URL != "" {
		return generic.URL
	}
	if generic.Permalink != "" {
		return generic.Permalink
	}
	// Google Calendar: construct URL from event ID.
	if generic.EventID != "" {
		return "https://calendar.google.com/calendar/event?eid=" + generic.EventID
	}
	return ""
}

// formatLink returns " [link](url)" if url is non-empty, otherwise "".
func formatLink(url string) string {
	if url == "" {
		return ""
	}
	return fmt.Sprintf(" [link](%s)", url)
}

// buildActivitiesPrompt formats activities into a structured prompt for the LLM.
// selfUIDs maps user IDs that belong to the standup author (labeled "You" in output).
func buildActivitiesPrompt(activities []models.Activity, selfUIDs map[string]bool) string {
	var b strings.Builder
	b.WriteString("Here are my work activities:\n\n")

	// Group by date for clarity.
	type dateActivities struct {
		date       string
		activities []models.Activity
	}
	var dates []dateActivities
	dateIndex := make(map[string]int)

	for _, a := range activities {
		dateStr := a.Timestamp.Format("2006-01-02")
		if idx, ok := dateIndex[dateStr]; ok {
			dates[idx].activities = append(dates[idx].activities, a)
		} else {
			dateIndex[dateStr] = len(dates)
			dates = append(dates, dateActivities{date: dateStr, activities: []models.Activity{a}})
		}
	}

	todayStr := time.Now().UTC().Format("2006-01-02")

	for _, d := range dates {
		t, _ := time.Parse("2006-01-02", d.date)
		if d.date == todayStr {
			fmt.Fprintf(&b, "## Today (%s)\n", d.date)
		} else {
			fmt.Fprintf(&b, "## %s (%s)\n", t.Weekday(), d.date)
		}

		for _, a := range d.activities {
			switch a.Source {
			case models.SourceGit:
				var meta commitMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				fmt.Fprintf(&b, "- [Git commit] %s: %s", meta.Repo, a.Title)
				if meta.FilesChanged > 0 {
					fmt.Fprintf(&b, " (%d files, +%d/-%d)", meta.FilesChanged, meta.Insertions, meta.Deletions)
				}
				b.WriteString("\n")

			case models.SourceCalendar:
				var meta calendarMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				link := extractURL(a)
				details := formatDuration(meta.DurationMin)
				if meta.MeetingType != "" {
					details += ", " + meta.MeetingType
				}
				nonSelfCount := 0
				for _, att := range meta.Attendees {
					if !att.Self {
						nonSelfCount++
					}
				}
				if nonSelfCount > 0 {
					details += fmt.Sprintf(", %d attendees", nonSelfCount)
				}
				if meta.ResponseStatus == "declined" {
					details += ", declined"
				}
				// Show scheduled time for today's meetings.
				if d.date == todayStr && meta.Start != "" && !meta.IsAllDay {
					if st, err := time.Parse(time.RFC3339, meta.Start); err == nil {
						fmt.Fprintf(&b, "- [Calendar meeting] %s — %s (%s)%s\n", st.Local().Format("15:04"), a.Title, details, formatLink(link))
						break
					}
				}
				fmt.Fprintf(&b, "- [Calendar meeting] %s (%s)%s\n", a.Title, details, formatLink(link))

			case models.SourceSlack:
				var meta slackFullMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				link := formatLink(meta.Permalink)
				if len(meta.ThreadMsgs) > 1 {
					// Thread with messages — give the LLM the full conversation.
					fmt.Fprintf(&b, "- [Slack thread] #%s (%d messages):%s\n", meta.ChannelName, len(meta.ThreadMsgs), link)
					for _, m := range meta.ThreadMsgs {
						label := m.User
						if selfUIDs[m.User] {
							label = "You"
						}
						fmt.Fprintf(&b, "    %s: %s\n", label, m.Text)
					}
				} else if a.Content != "" {
					fmt.Fprintf(&b, "- [Slack message] #%s: %s%s\n", meta.ChannelName, a.Content, link)
				} else {
					fmt.Fprintf(&b, "- [Slack message] #%s: %s%s\n", meta.ChannelName, a.Title, link)
				}

			default:
				link := formatLink(extractURL(a))
				fmt.Fprintf(&b, "- [%s] %s%s\n", a.Source, a.Title, link)
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}
