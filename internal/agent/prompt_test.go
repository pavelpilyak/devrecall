package agent

import (
	"strings"
	"testing"
	"time"
)

func TestDatePreamble(t *testing.T) {
	// 2026-08-07 is a Friday. GMT+2 puts local time a day ahead of UTC in the
	// small hours, which is exactly the case that used to break date filters.
	loc := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 8, 7, 0, 30, 0, 0, loc)

	got := DatePreamble(now)

	for _, want := range []string{
		"Friday",
		"2026-08-07", // today, local
		"2026-08-06", // yesterday
		"+02:00",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("DatePreamble() = %q, missing %q", got, want)
		}
	}

	// The UTC date here is 2026-08-06 (22:30 the previous day). The preamble must
	// state the *local* date, or the model resolves "today" to yesterday.
	if strings.HasPrefix(got, "Thursday") {
		t.Error("DatePreamble() reported the UTC weekday; want the user's local date")
	}
}

func TestDatePreamble_StableWithinADay(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)
	morning := DatePreamble(time.Date(2026, 8, 7, 9, 0, 0, 0, loc))
	evening := DatePreamble(time.Date(2026, 8, 7, 21, 45, 12, 0, loc))

	// Day granularity keeps the system prompt byte-identical across requests on
	// the same day, so BYOK prompt caching still hits.
	if morning != evening {
		t.Errorf("preamble changed within a day, breaking prompt caching:\n  %q\n  %q", morning, evening)
	}
}

func TestDatePreamble_YearBoundary(t *testing.T) {
	got := DatePreamble(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
	if !strings.Contains(got, "2025-12-31") {
		t.Errorf("DatePreamble() = %q, want yesterday to cross into 2025-12-31", got)
	}
}
