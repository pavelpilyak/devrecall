package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	slackcollector "github.com/pavelpilyak/devrecall/internal/collector/slack"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

const threadPrompt = `You are analyzing a Slack thread. Extract the following from the conversation:

1. **Topic**: A concise one-line summary of what the thread is about.
2. **Decisions**: A list of decisions or conclusions reached in the thread. If none, return an empty list.

Respond ONLY with valid JSON in this exact format (no markdown, no explanation):
{"topic": "string", "decisions": ["string", ...]}

Thread messages:`

// slackMsgMeta mirrors the messageMeta from the Slack collector, including thread data.
type slackMsgMeta struct {
	ChannelID     string                      `json:"channel_id"`
	ChannelName   string                      `json:"channel_name"`
	ThreadTS      string                      `json:"thread_ts,omitempty"`
	IsThreadReply bool                        `json:"is_thread_reply,omitempty"`
	ReplyCount    int                         `json:"reply_count,omitempty"`
	Permalink     string                      `json:"permalink,omitempty"`
	Participants  []string                    `json:"participants,omitempty"`
	ThreadMsgs    []slackcollector.ThreadMsg  `json:"thread_msgs,omitempty"`
	Summary       *slackcollector.ThreadSummary `json:"summary,omitempty"`
}

// ThreadSummarizer uses an LLM to summarize Slack threads.
type ThreadSummarizer struct {
	provider llm.Provider
}

// NewThreadSummarizer creates a thread summarizer with the given LLM provider.
func NewThreadSummarizer(provider llm.Provider) *ThreadSummarizer {
	return &ThreadSummarizer{provider: provider}
}

// SummarizeThreads processes a slice of activities and summarizes any Slack threads
// that have thread messages but no existing summary. It returns the activities with
// updated metadata (summaries filled in) and the count of threads summarized.
func (ts *ThreadSummarizer) SummarizeThreads(ctx context.Context, activities []models.Activity) ([]models.Activity, int, error) {
	summarized := 0
	for i, a := range activities {
		if a.Source != models.SourceSlack {
			continue
		}

		var meta slackMsgMeta
		if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
			continue
		}

		// Skip if no thread messages or already summarized.
		if len(meta.ThreadMsgs) == 0 || meta.Summary != nil {
			continue
		}

		summary, err := ts.summarizeThread(ctx, meta.ThreadMsgs)
		if err != nil {
			// Non-fatal: keep going with other threads.
			continue
		}

		meta.Summary = summary
		// Update participants from metadata if available.
		if len(meta.Participants) > 0 {
			meta.Summary.Participants = meta.Participants
		}

		metaJSON, err := json.Marshal(meta)
		if err != nil {
			continue
		}
		activities[i].Metadata = string(metaJSON)
		summarized++
	}
	return activities, summarized, nil
}

// summarizeThread calls the LLM to extract topic and decisions from thread messages.
func (ts *ThreadSummarizer) summarizeThread(ctx context.Context, msgs []slackcollector.ThreadMsg) (*slackcollector.ThreadSummary, error) {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s]: %s\n", m.User, m.Text)
	}

	resp, err := ts.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: threadPrompt},
		{Role: "user", Content: b.String()},
	}, llm.ChatOpts{
		Temperature: 0.2,
		MaxTokens:   512,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM chat: %w", err)
	}

	return parseThreadSummary(resp)
}

// parseThreadSummary extracts a ThreadSummary from the LLM response JSON.
func parseThreadSummary(resp string) (*slackcollector.ThreadSummary, error) {
	// Strip markdown code fences if present.
	resp = strings.TrimSpace(resp)
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	var result slackcollector.ThreadSummary
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parsing LLM response: %w", err)
	}

	if result.Topic == "" {
		return nil, fmt.Errorf("LLM returned empty topic")
	}

	return &result, nil
}
