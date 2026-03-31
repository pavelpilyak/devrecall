package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

const standupSystemPrompt = `You are a developer standup report generator. Given a list of work activities (commits, Slack messages, etc.), write a concise, natural-language standup summary.

Rules:
- Group related work together (e.g., multiple commits on the same feature)
- Use past tense for completed work
- Keep each bullet point to 1-2 sentences
- Highlight key decisions from Slack threads
- Don't include commit SHAs or raw file counts — focus on what was accomplished
- If there are multiple days, separate them with date headers
- Be concise and professional — this is for a team standup

Respond ONLY with the standup text, no preamble or explanation.`

// LLMSummarizer generates standups using an LLM provider.
type LLMSummarizer struct {
	provider llm.Provider
	fallback *TemplateSummarizer
}

// NewLLMSummarizer creates an LLM-powered summarizer with template fallback.
func NewLLMSummarizer(provider llm.Provider) *LLMSummarizer {
	return &LLMSummarizer{
		provider: provider,
		fallback: NewTemplateSummarizer(),
	}
}

// Standup generates a standup report using the LLM.
// Falls back to template-based generation on LLM failure.
func (s *LLMSummarizer) Standup(activities []models.Activity) (string, error) {
	if len(activities) == 0 {
		return "No activity found for the given period.", nil
	}

	prompt := buildActivitiesPrompt(activities)

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
		return s.fallback.Standup(activities)
	}

	resp = strings.TrimSpace(resp)
	if resp == "" {
		return s.fallback.Standup(activities)
	}

	return resp, nil
}

// buildActivitiesPrompt formats activities into a structured prompt for the LLM.
func buildActivitiesPrompt(activities []models.Activity) string {
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
				var meta slackMeta
				json.Unmarshal([]byte(a.Metadata), &meta)
				if meta.Summary != nil && meta.Summary.Topic != "" {
					fmt.Fprintf(&b, "- [Slack thread] #%s: %s", meta.ChannelName, meta.Summary.Topic)
					if len(meta.Summary.Decisions) > 0 {
						fmt.Fprintf(&b, " | Decisions: %s", strings.Join(meta.Summary.Decisions, "; "))
					}
				} else {
					fmt.Fprintf(&b, "- [Slack message] #%s: %s", meta.ChannelName, a.Title)
				}
				b.WriteString("\n")

			default:
				fmt.Fprintf(&b, "- [%s] %s\n", a.Source, a.Title)
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}
