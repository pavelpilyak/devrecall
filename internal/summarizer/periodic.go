package summarizer

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

const monthlySystemPrompt = `You are a developer monthly summary generator. Given daily and weekly summaries for a month, write a concise monthly summary.

Rules:
- Start with a 2-3 sentence overview of the month's main themes
- Group work by projects or themes, not by week
- Highlight key accomplishments, decisions, and milestones
- Include collaboration highlights (key people you worked with)
- Include a brief meeting time summary if available
- Be concise and professional — this feeds into quarterly summaries and performance reviews

Respond ONLY with the monthly summary text, no preamble or explanation.`

const quarterlySystemPrompt = `You are a developer quarterly summary generator. Given monthly summaries for a quarter, write a comprehensive quarterly summary suitable for performance reviews.

Rules:
- Start with a high-level overview (3-4 sentences) of the quarter
- Group accomplishments by impact area or project
- Highlight measurable outcomes where possible (PRs merged, features shipped, incidents resolved)
- Note key collaborations and cross-team work
- Identify growth areas and new skills/technologies used
- End with a brief forward-looking section if context permits
- This summary will be used for brag documents and performance reviews — be thorough but concise

Respond ONLY with the quarterly summary text, no preamble or explanation.`

// PeriodType constants.
const (
	PeriodDaily     = "daily"
	PeriodWeekly    = "weekly"
	PeriodMonthly   = "monthly"
	PeriodQuarterly = "quarterly"
)

// PeriodicGenerator generates and stores periodic summaries.
type PeriodicGenerator struct {
	db          *storage.DB
	summarizer  Summarizer
	llmProvider llm.Provider
}

// NewPeriodicGenerator creates a generator that produces periodic summaries.
func NewPeriodicGenerator(db *storage.DB, summarizer Summarizer, provider llm.Provider) *PeriodicGenerator {
	return &PeriodicGenerator{
		db:          db,
		summarizer:  summarizer,
		llmProvider: provider,
	}
}

// GenerateMissing generates all missing summaries for the given period type
// between since and until. Returns the number of summaries generated.
func (g *PeriodicGenerator) GenerateMissing(ctx context.Context, periodType string, since, until time.Time) (int, error) {
	// Align since to period boundary.
	since = alignToPeriod(since, periodType)

	missing, err := g.db.MissingSummaryPeriods(periodType, since, until)
	if err != nil {
		return 0, fmt.Errorf("find missing periods: %w", err)
	}

	// Filter out current (incomplete) period first for accurate count.
	var toGenerate []time.Time
	for _, periodStart := range missing {
		periodEnd := endOfPeriod(periodStart, periodType)
		if !periodEnd.After(until) {
			toGenerate = append(toGenerate, periodStart)
		}
	}

	if len(toGenerate) > 0 {
		fmt.Fprintf(os.Stderr, "  Generating %d %s %s...\n",
			len(toGenerate), periodType, pluralSuffix(len(toGenerate), "summary", "summaries"))
	}

	generated := 0
	for i, periodStart := range toGenerate {
		periodEnd := endOfPeriod(periodStart, periodType)

		fmt.Fprintf(os.Stderr, "  [%d/%d] %s %s → calling LLM...\n",
			i+1, len(toGenerate), periodType, periodStart.Format("2006-01-02"))

		summary, err := g.generateOne(ctx, periodType, periodStart, periodEnd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to generate %s summary for %s: %v\n",
				periodType, periodStart.Format("2006-01-02"), err)
			continue
		}

		if _, err := g.db.InsertSummary(summary); err != nil {
			return generated, fmt.Errorf("store %s summary for %s: %w",
				periodType, periodStart.Format("2006-01-02"), err)
		}
		generated++
	}

	return generated, nil
}

// GenerateOne generates a single summary for a specific period. Useful for
// on-demand generation (e.g., "generate yesterday's summary").
func (g *PeriodicGenerator) GenerateOne(ctx context.Context, periodType string, periodStart time.Time) (models.Summary, error) {
	periodStart = alignToPeriod(periodStart, periodType)
	periodEnd := endOfPeriod(periodStart, periodType)
	return g.generateOne(ctx, periodType, periodStart, periodEnd)
}

func (g *PeriodicGenerator) generateOne(ctx context.Context, periodType string, start, end time.Time) (models.Summary, error) {
	switch periodType {
	case PeriodDaily, PeriodWeekly:
		return g.generateFromActivities(ctx, periodType, start, end)
	case PeriodMonthly, PeriodQuarterly:
		return g.generateFromChildSummaries(ctx, periodType, start, end)
	default:
		return models.Summary{}, fmt.Errorf("unknown period type: %s", periodType)
	}
}

// generateFromActivities creates a daily or weekly summary from raw activities.
func (g *PeriodicGenerator) generateFromActivities(ctx context.Context, periodType string, start, end time.Time) (models.Summary, error) {
	activities, err := g.db.ListActivities(storage.ActivityFilter{
		After:  start,
		Before: end,
	})
	if err != nil {
		return models.Summary{}, fmt.Errorf("list activities: %w", err)
	}

	var text string
	switch periodType {
	case PeriodDaily:
		text, err = g.summarizer.Standup(activities)
	case PeriodWeekly:
		text, err = g.summarizer.WeeklySummary(activities)
	}
	if err != nil {
		return models.Summary{}, fmt.Errorf("generate %s summary: %w", periodType, err)
	}

	return models.Summary{
		PeriodType:    periodType,
		PeriodStart:   start.Format("2006-01-02"),
		PeriodEnd:     end.Format("2006-01-02"),
		SummaryText:   text,
		ActivityCount: len(activities),
	}, nil
}

// generateFromChildSummaries creates monthly/quarterly summaries from child summaries.
func (g *PeriodicGenerator) generateFromChildSummaries(ctx context.Context, periodType string, start, end time.Time) (models.Summary, error) {
	childType := childPeriodType(periodType)

	children, err := g.db.ListSummariesInRange(childType, start, end)
	if err != nil {
		return models.Summary{}, fmt.Errorf("list child summaries: %w", err)
	}

	// If no child summaries exist, fall back to raw activities.
	if len(children) == 0 {
		return g.generateFromActivities(ctx, periodType, start, end)
	}

	// Build prompt from child summaries.
	var b strings.Builder
	for _, child := range children {
		fmt.Fprintf(&b, "=== %s summary (%s to %s) ===\n%s\n\n",
			child.PeriodType, child.PeriodStart, child.PeriodEnd, child.SummaryText)
	}

	var systemPrompt string
	switch periodType {
	case PeriodMonthly:
		systemPrompt = monthlySystemPrompt
	case PeriodQuarterly:
		systemPrompt = quarterlySystemPrompt
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := g.llmProvider.Chat(timeoutCtx, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: b.String()},
	}, llm.ChatOpts{
		Temperature: 0.3,
		MaxTokens:   2048,
	})
	if err != nil {
		return models.Summary{}, fmt.Errorf("LLM %s summary: %w", periodType, err)
	}

	// Count total activities across child summaries.
	totalActivities := 0
	for _, child := range children {
		totalActivities += child.ActivityCount
	}

	return models.Summary{
		PeriodType:    periodType,
		PeriodStart:   start.Format("2006-01-02"),
		PeriodEnd:     end.Format("2006-01-02"),
		SummaryText:   strings.TrimSpace(resp),
		ActivityCount: totalActivities,
	}, nil
}

// alignToPeriod snaps a date to the start of its containing period.
func alignToPeriod(t time.Time, periodType string) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch periodType {
	case PeriodDaily:
		return t
	case PeriodWeekly:
		// Align to Monday.
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return t.AddDate(0, 0, -(weekday - 1))
	case PeriodMonthly:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case PeriodQuarterly:
		quarter := (int(t.Month()) - 1) / 3
		return time.Date(t.Year(), time.Month(quarter*3+1), 1, 0, 0, 0, 0, time.UTC)
	default:
		return t
	}
}

// endOfPeriod returns the exclusive end date for a period starting at t.
func endOfPeriod(t time.Time, periodType string) time.Time {
	switch periodType {
	case PeriodDaily:
		return t.AddDate(0, 0, 1)
	case PeriodWeekly:
		return t.AddDate(0, 0, 7)
	case PeriodMonthly:
		return t.AddDate(0, 1, 0)
	case PeriodQuarterly:
		return t.AddDate(0, 3, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

func pluralSuffix(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func childPeriodType(periodType string) string {
	switch periodType {
	case PeriodMonthly:
		return PeriodWeekly
	case PeriodQuarterly:
		return PeriodMonthly
	default:
		return PeriodDaily
	}
}
