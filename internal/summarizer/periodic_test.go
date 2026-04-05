package summarizer

import (
	"context"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/internal/storage"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

type mockSummarizer struct {
	standupText string
	weeklyText  string
}

func (m *mockSummarizer) Standup(_ []models.Activity) (string, error) {
	return m.standupText, nil
}

func (m *mockSummarizer) WeeklySummary(_ []models.Activity) (string, error) {
	return m.weeklyText, nil
}

type mockLLMProvider struct {
	response string
}

func (m *mockLLMProvider) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOpts) (string, error) {
	return m.response, nil
}

func (m *mockLLMProvider) Name() string { return "mock" }

func mustOpenDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestAlignToPeriod(t *testing.T) {
	tests := []struct {
		name     string
		date     time.Time
		period   string
		expected time.Time
	}{
		{
			name:     "daily unchanged",
			date:     time.Date(2026, 3, 27, 14, 30, 0, 0, time.UTC),
			period:   PeriodDaily,
			expected: time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "weekly to Monday (from Wednesday)",
			date:     time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC), // Wednesday
			period:   PeriodWeekly,
			expected: time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC), // Monday
		},
		{
			name:     "weekly to Monday (from Sunday)",
			date:     time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), // Sunday
			period:   PeriodWeekly,
			expected: time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC), // Monday
		},
		{
			name:     "weekly already Monday",
			date:     time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC), // Monday
			period:   PeriodWeekly,
			expected: time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "monthly to 1st",
			date:     time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
			period:   PeriodMonthly,
			expected: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "quarterly Q1",
			date:     time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC),
			period:   PeriodQuarterly,
			expected: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "quarterly Q2",
			date:     time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			period:   PeriodQuarterly,
			expected: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alignToPeriod(tt.date, tt.period)
			if !got.Equal(tt.expected) {
				t.Errorf("alignToPeriod(%v, %s) = %v, want %v", tt.date, tt.period, got, tt.expected)
			}
		})
	}
}

func TestEndOfPeriod(t *testing.T) {
	tests := []struct {
		period   string
		start    time.Time
		expected time.Time
	}{
		{PeriodDaily, time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC)},
		{PeriodWeekly, time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)},
		{PeriodMonthly, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
		{PeriodQuarterly, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			got := endOfPeriod(tt.start, tt.period)
			if !got.Equal(tt.expected) {
				t.Errorf("endOfPeriod(%v, %s) = %v, want %v", tt.start, tt.period, got, tt.expected)
			}
		})
	}
}

func TestGenerateMissing_Daily(t *testing.T) {
	db := mustOpenDB(t)

	// Insert some activities.
	for i := 0; i < 3; i++ {
		db.InsertActivity(models.Activity{
			Source:    models.SourceGit,
			SourceID:  "r:a" + string(rune('1'+i)),
			Type:      models.TypeCommit,
			Title:     "Commit " + string(rune('1'+i)),
			Timestamp: time.Date(2026, 3, 25+i, 10, 0, 0, 0, time.UTC),
		})
	}

	sum := &mockSummarizer{standupText: "Did stuff today."}
	provider := &mockLLMProvider{response: "monthly overview"}
	gen := NewPeriodicGenerator(db, sum, provider)

	since := time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC)

	n, err := gen.GenerateMissing(context.Background(), PeriodDaily, since, until)
	if err != nil {
		t.Fatalf("GenerateMissing: %v", err)
	}
	if n != 3 {
		t.Errorf("generated %d, want 3", n)
	}

	// Verify summaries exist.
	summaries, err := db.ListSummaries("daily", 10)
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if len(summaries) != 3 {
		t.Errorf("stored %d summaries, want 3", len(summaries))
	}

	// Running again should generate 0 (all exist now).
	n2, _ := gen.GenerateMissing(context.Background(), PeriodDaily, since, until)
	if n2 != 0 {
		t.Errorf("second run generated %d, want 0", n2)
	}
}

func TestGenerateMissing_SkipsCurrentPeriod(t *testing.T) {
	db := mustOpenDB(t)

	sum := &mockSummarizer{standupText: "stuff"}
	provider := &mockLLMProvider{}
	gen := NewPeriodicGenerator(db, sum, provider)

	// Since yesterday, until tomorrow — the "today" period should be skipped
	// because its end (tomorrow) is after `until` (now).
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -1)

	n, err := gen.GenerateMissing(context.Background(), PeriodDaily, since, now)
	if err != nil {
		t.Fatalf("GenerateMissing: %v", err)
	}
	// Should generate yesterday only (today is incomplete).
	if n != 1 {
		t.Errorf("generated %d, want 1 (yesterday only)", n)
	}
}

func TestGenerateOne_Daily(t *testing.T) {
	db := mustOpenDB(t)

	db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:a1", Type: models.TypeCommit,
		Title: "Fix bug", Timestamp: time.Date(2026, 3, 27, 14, 0, 0, 0, time.UTC),
	})

	sum := &mockSummarizer{standupText: "Fixed a bug."}
	provider := &mockLLMProvider{}
	gen := NewPeriodicGenerator(db, sum, provider)

	s, err := gen.GenerateOne(context.Background(), PeriodDaily, time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateOne: %v", err)
	}
	if s.PeriodType != PeriodDaily {
		t.Errorf("type = %q", s.PeriodType)
	}
	if s.SummaryText != "Fixed a bug." {
		t.Errorf("text = %q", s.SummaryText)
	}
	if s.ActivityCount != 1 {
		t.Errorf("activity_count = %d, want 1", s.ActivityCount)
	}
}

func TestGenerateOne_MonthlyFromChildSummaries(t *testing.T) {
	db := mustOpenDB(t)

	// Insert weekly child summaries for March 2026.
	db.InsertSummary(models.Summary{
		PeriodType: "weekly", PeriodStart: "2026-03-02", PeriodEnd: "2026-03-09",
		SummaryText: "Worked on auth system.", ActivityCount: 10,
	})
	db.InsertSummary(models.Summary{
		PeriodType: "weekly", PeriodStart: "2026-03-09", PeriodEnd: "2026-03-16",
		SummaryText: "Reviewed PRs and fixed bugs.", ActivityCount: 8,
	})

	sum := &mockSummarizer{}
	provider := &mockLLMProvider{response: "March was productive: auth system and bug fixes."}
	gen := NewPeriodicGenerator(db, sum, provider)

	s, err := gen.GenerateOne(context.Background(), PeriodMonthly, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateOne monthly: %v", err)
	}
	if s.PeriodType != PeriodMonthly {
		t.Errorf("type = %q", s.PeriodType)
	}
	if s.SummaryText != "March was productive: auth system and bug fixes." {
		t.Errorf("text = %q", s.SummaryText)
	}
	if s.ActivityCount != 18 {
		t.Errorf("activity_count = %d, want 18 (sum of children)", s.ActivityCount)
	}
}

func TestChildPeriodType(t *testing.T) {
	if childPeriodType(PeriodMonthly) != PeriodWeekly {
		t.Error("monthly child should be weekly")
	}
	if childPeriodType(PeriodQuarterly) != PeriodMonthly {
		t.Error("quarterly child should be monthly")
	}
}
