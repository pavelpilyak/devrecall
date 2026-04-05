package summarizer

import (
	"context"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func TestCompletedQuartersWithoutSummary(t *testing.T) {
	db := mustOpenDB(t)

	sum := &mockSummarizer{}
	provider := &mockLLMProvider{}
	gen := NewPeriodicGenerator(db, sum, provider)

	// Now is April 5, 2026 — Q1 2026 (Jan-Mar) is complete but has no summary.
	now := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)

	missing, err := CompletedQuartersWithoutSummary(gen, now, 4)
	if err != nil {
		t.Fatalf("CompletedQuartersWithoutSummary: %v", err)
	}

	// Should find Q1 2026, Q2 2025, Q3 2025, Q4 2025 (looking back 4 quarters).
	if len(missing) != 4 {
		t.Fatalf("missing = %d, want 4", len(missing))
	}

	// The first should be Q2 2025.
	if missing[0] != (time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("first missing = %v, want 2025-04-01", missing[0])
	}

	// Add Q1 2026 summary and check again.
	db.InsertSummary(models.Summary{
		PeriodType: "quarterly", PeriodStart: "2026-01-01", PeriodEnd: "2026-04-01",
		SummaryText: "Q1 summary.", ActivityCount: 100,
	})

	missing2, err := CompletedQuartersWithoutSummary(gen, now, 4)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	// Should now find 3 (Q1 2026 exists).
	if len(missing2) != 3 {
		t.Errorf("after insert, missing = %d, want 3", len(missing2))
	}
}

func TestCompletedQuartersWithoutSummary_NoneNeeded(t *testing.T) {
	db := mustOpenDB(t)

	sum := &mockSummarizer{}
	provider := &mockLLMProvider{}
	gen := NewPeriodicGenerator(db, sum, provider)

	// Mid-quarter — current quarter not complete yet, and we insert summary for the previous.
	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)

	// Insert Q4 2025 summary.
	db.InsertSummary(models.Summary{
		PeriodType: "quarterly", PeriodStart: "2025-10-01", PeriodEnd: "2026-01-01",
		SummaryText: "Q4 summary.", ActivityCount: 50,
	})

	missing, err := CompletedQuartersWithoutSummary(gen, now, 1)
	if err != nil {
		t.Fatalf("CompletedQuartersWithoutSummary: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %d, want 0", len(missing))
	}
}

func TestAutoSnapshot(t *testing.T) {
	db := mustOpenDB(t)

	// Insert monthly summaries for Q1 2026 so the quarterly can build from children.
	for _, m := range []struct{ start, end string }{
		{"2026-01-01", "2026-02-01"},
		{"2026-02-01", "2026-03-01"},
		{"2026-03-01", "2026-04-01"},
	} {
		db.InsertSummary(models.Summary{
			PeriodType: "monthly", PeriodStart: m.start, PeriodEnd: m.end,
			SummaryText: "Monthly work for " + m.start, ActivityCount: 30,
		})
	}

	sum := &mockSummarizer{standupText: "daily"}
	provider := &mockLLMProvider{response: "Comprehensive Q1 2026 summary."}
	gen := NewPeriodicGenerator(db, sum, provider)

	// Now is April 5 — Q1 is complete.
	now := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)

	// Only look back 1 quarter so we just get Q1.
	generated, err := autoSnapshotWithLookback(context.Background(), gen, now, 1)
	if err != nil {
		t.Fatalf("AutoSnapshot: %v", err)
	}
	if generated != 1 {
		t.Fatalf("generated = %d, want 1", generated)
	}

	// Verify the quarterly summary was stored.
	s, err := db.GetSummary("quarterly", "2026-01-01")
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if s.SummaryText != "Comprehensive Q1 2026 summary." {
		t.Errorf("summary text = %q", s.SummaryText)
	}
	if s.ActivityCount != 90 {
		t.Errorf("activity_count = %d, want 90", s.ActivityCount)
	}

	// Running again should generate 0.
	generated2, _ := autoSnapshotWithLookback(context.Background(), gen, now, 1)
	if generated2 != 0 {
		t.Errorf("second run generated = %d, want 0", generated2)
	}
}

func TestAutoSnapshot_NoCompletedQuarters(t *testing.T) {
	db := mustOpenDB(t)

	sum := &mockSummarizer{}
	provider := &mockLLMProvider{}
	gen := NewPeriodicGenerator(db, sum, provider)

	// Mid-quarter, no completed quarters to look at.
	now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	generated, err := autoSnapshotWithLookback(context.Background(), gen, now, 0)
	if err != nil {
		t.Fatalf("AutoSnapshot: %v", err)
	}
	if generated != 0 {
		t.Errorf("generated = %d, want 0", generated)
	}
}

func TestQuarterLabel(t *testing.T) {
	tests := []struct {
		date time.Time
		want string
	}{
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "Q1 2026"},
		{time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), "Q2 2026"},
		{time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), "Q3 2026"},
		{time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "Q4 2026"},
	}

	for _, tt := range tests {
		got := QuarterLabel(tt.date)
		if got != tt.want {
			t.Errorf("QuarterLabel(%v) = %q, want %q", tt.date, got, tt.want)
		}
	}
}

// autoSnapshotWithLookback is a test helper that wraps the internal logic with configurable lookback.
func autoSnapshotWithLookback(ctx context.Context, gen *PeriodicGenerator, now time.Time, maxLookback int) (int, error) {
	missing, err := CompletedQuartersWithoutSummary(gen, now, maxLookback)
	if err != nil {
		return 0, err
	}

	if len(missing) == 0 {
		return 0, nil
	}

	generated := 0
	for _, qStart := range missing {
		qEnd := endOfPeriod(qStart, PeriodQuarterly)

		gen.GenerateMissing(ctx, PeriodMonthly, qStart, qEnd)

		summary, err := gen.generateOne(ctx, PeriodQuarterly, qStart, qEnd)
		if err != nil {
			continue
		}

		if _, err := gen.db.InsertSummary(summary); err != nil {
			return generated, err
		}
		generated++
	}

	return generated, nil
}
