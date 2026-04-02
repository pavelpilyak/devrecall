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

const standupSystemPrompt = `You are a developer standup report generator. Given a list of work activities (commits, Slack messages, etc.), write a concise, natural-language standup summary.

Rules:
- Group related work together (e.g., multiple commits on the same feature)
- This is a FIRST-PERSON standup: only report what "You" did, asked, or decided — not what others said
- In Slack threads, messages from "You" are the standup author; other participants are teammates
- Accurately reflect the tense: if something was planned or will be done, use future tense; if completed, use past tense
- Keep each bullet point to 1-2 sentences
- Highlight key decisions from Slack threads, but attribute them correctly
- Don't include commit SHAs or raw file counts — focus on what was accomplished
- If there are multiple days, separate them with date headers
- Be concise and professional — this is for a team standup

Respond ONLY with the standup text, no preamble or explanation.`

// LLMSummarizer generates standups using an LLM provider.
type LLMSummarizer struct {
	provider llm.Provider
	fallback *TemplateSummarizer
	selfUIDs []string // user IDs across sources (e.g., Slack UID) to label "You" in prompts
}

// NewLLMSummarizer creates an LLM-powered summarizer with template fallback.
func NewLLMSummarizer(provider llm.Provider) *LLMSummarizer {
	return &LLMSummarizer{
		provider: provider,
		fallback: NewTemplateSummarizer(),
	}
}

// WithSelfUIDs sets user IDs that identify the standup author across sources.
func (s *LLMSummarizer) WithSelfUIDs(uids ...string) *LLMSummarizer {
	s.selfUIDs = uids
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
		{Role: "system", Content: standupSystemPrompt},
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

// slackFullMeta extends slackMeta with thread messages for the LLM prompt.
type slackFullMeta struct {
	ChannelName string             `json:"channel_name"`
	ReplyCount  int                `json:"reply_count,omitempty"`
	Summary     *slackThreadSummary `json:"summary,omitempty"`
	ThreadMsgs  []struct {
		User string `json:"user"`
		Text string `json:"text"`
	} `json:"thread_msgs,omitempty"`
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

	for _, d := range dates {
		t, _ := time.Parse("2006-01-02", d.date)
		fmt.Fprintf(&b, "## %s (%s)\n", t.Weekday(), d.date)

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

			case models.SourceSlack:
				var meta slackFullMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				if len(meta.ThreadMsgs) > 1 {
					// Thread with messages — give the LLM the full conversation.
					fmt.Fprintf(&b, "- [Slack thread] #%s (%d messages):\n", meta.ChannelName, len(meta.ThreadMsgs))
					for _, m := range meta.ThreadMsgs {
						label := m.User
						if selfUIDs[m.User] {
							label = "You"
						}
						fmt.Fprintf(&b, "    %s: %s\n", label, m.Text)
					}
				} else if a.Content != "" {
					fmt.Fprintf(&b, "- [Slack message] #%s: %s\n", meta.ChannelName, a.Content)
				} else {
					fmt.Fprintf(&b, "- [Slack message] #%s: %s\n", meta.ChannelName, a.Title)
				}

			default:
				fmt.Fprintf(&b, "- [%s] %s\n", a.Source, a.Title)
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}
