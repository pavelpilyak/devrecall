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

// entry holds a formatted activity for grouping.
type entry struct {
	title     string
	stats     string
	decisions []string
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

	for _, a := range activities {
		dateStr := a.Timestamp.Format("2006-01-02")
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
				if !strings.HasPrefix(group, "#") {
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
