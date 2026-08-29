package summarizer

import (
	"context"
	"fmt"
	"os"
	"time"
)

// CompletedQuartersWithoutSummary returns the start dates of quarters that have
// ended but don't yet have a quarterly summary. It looks back up to maxLookback
// quarters from now.
func CompletedQuartersWithoutSummary(gen *PeriodicGenerator, now time.Time, maxLookback int) ([]time.Time, error) {
	// Find the start of the current quarter (which is not yet complete).
	currentQStart := alignToPeriod(now, PeriodQuarterly)

	// Look back up to maxLookback quarters.
	lookbackStart := currentQStart.AddDate(0, -3*maxLookback, 0)

	missing, err := gen.db.MissingSummaryPeriods(PeriodQuarterly, lookbackStart, currentQStart)
	if err != nil {
		return nil, fmt.Errorf("check missing quarters: %w", err)
	}

	return missing, nil
}

// AutoSnapshot generates quarterly summaries for any completed quarters that
// are missing them. Returns the number of summaries generated.
func AutoSnapshot(ctx context.Context, gen *PeriodicGenerator, now time.Time) (int, error) {
	missing, err := CompletedQuartersWithoutSummary(gen, now, 4)
	if err != nil {
		return 0, err
	}

	if len(missing) == 0 {
		return 0, nil
	}

	generated := 0
	for _, qStart := range missing {
		qEnd := endOfPeriod(qStart, PeriodQuarterly)

		// A quarterly summary is written from monthly summaries, not from raw
		// activities, so the quarter's months have to exist first — up to three
		// extra LLM calls per quarter. Say so: an unexplained pause and a
		// surprise bill is what made this feel broken.
		fmt.Fprintf(os.Stderr, "Auto-snapshot: %s has no summary; generating its monthly summaries first...\n",
			QuarterLabel(qStart))
		monthlyGenerated, err := gen.GenerateMissing(ctx, PeriodMonthly, qStart, qEnd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to generate monthly summaries for %s: %v\n", qStart.Format("2006-01-02"), err)
		}
		if monthlyGenerated > 0 {
			fmt.Fprintf(os.Stderr, "Auto-snapshot: generated %d monthly %s for %s.\n",
				monthlyGenerated, pluralizeSummary(monthlyGenerated), QuarterLabel(qStart))
		}

		// Now generate the quarterly summary.
		summary, err := gen.generateOne(ctx, PeriodQuarterly, qStart, qEnd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to generate quarterly snapshot for %s: %v\n",
				QuarterLabel(qStart), err)
			continue
		}

		if _, err := gen.db.InsertSummary(summary); err != nil {
			return generated, fmt.Errorf("store quarterly snapshot: %w", err)
		}
		generated++
	}

	return generated, nil
}

// QuarterLabel returns a human-readable label like "Q1 2026" for a quarter start date.
func QuarterLabel(t time.Time) string {
	q := (int(t.Month())-1)/3 + 1
	return fmt.Sprintf("Q%d %d", q, t.Year())
}

func pluralizeSummary(n int) string {
	if n == 1 {
		return "summary"
	}
	return "summaries"
}
