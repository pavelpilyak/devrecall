package chat

import (
	"strings"
	"testing"
	"time"
)

// The REPL prompt is a separate copy from the HTTP one, so it needs its own
// guard — otherwise `devrecall chat` could quietly regress while the desktop
// app stays correct.
func TestSystemPromptAt_AnchorsTodaysDate(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)
	got := systemPromptAt(time.Date(2026, 8, 7, 0, 30, 0, 0, loc))

	for _, want := range []string{"2026-08-07", "2026-08-06", "Friday"} {
		if !strings.Contains(got, want) {
			t.Errorf("systemPromptAt() is missing %q; the model has no date anchor", want)
		}
	}
}

func TestSystemPromptAt_KeepsDateAndFallbackRules(t *testing.T) {
	got := systemPromptAt(time.Now())

	if !strings.Contains(got, "Never infer the date or the year") {
		t.Error("prompt lost the rule forbidding date/year inference from context")
	}
	if !strings.Contains(got, "fall back to list_activities") {
		t.Error("prompt lost the list_work_items -> list_activities fallback rule")
	}
}
