package api

import (
	"strings"
	"testing"
	"time"
)

// Guards the wiring, not the helper: agent.DatePreamble is unit-tested on its
// own, but nothing else catches the prompt silently losing the date — which is
// the whole fix for models answering date questions against a hallucinated year.
func TestChatSystemPrompt_AnchorsTodaysDate(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)
	got := chatSystemPrompt(time.Date(2026, 8, 7, 0, 30, 0, 0, loc))

	for _, want := range []string{
		"2026-08-07", // today, in the user's local timezone
		"2026-08-06", // yesterday
		"Friday",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("chatSystemPrompt() is missing %q; the model has no date anchor", want)
		}
	}
}

func TestChatSystemPrompt_KeepsDateAndFallbackRules(t *testing.T) {
	got := chatSystemPrompt(time.Now())

	// Both rules exist because a model without them produces a confidently
	// wrong "you did nothing" answer: the first from guessing the year, the
	// second from treating an empty list_work_items as "no activity".
	if !strings.Contains(got, "Never infer the date or the year") {
		t.Error("prompt lost the rule forbidding date/year inference from context")
	}
	if !strings.Contains(got, "fall back to list_activities") {
		t.Error("prompt lost the list_work_items -> list_activities fallback rule")
	}
}
