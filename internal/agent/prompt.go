package agent

import (
	"fmt"
	"time"
)

// DatePreamble returns a system-prompt fragment stating today's date in the
// user's local timezone.
//
// Anchoring the date in the prompt rather than relying on the current_time tool
// is deliberate. Small local models (gemma4 and friends) routinely ignore an
// instruction to call a tool first, then answer date-relative questions against
// whatever year they hallucinate or scrape out of the conversation history —
// which produces confidently wrong "you did nothing" answers. Stating the date
// removes the dependency on the model choosing correctly.
//
// It also fixes a subtler bug: current_time reports UTC, so for a user east of
// Greenwich the "current date" it returns is yesterday's for the first hours of
// every local day.
//
// The fragment is deliberately day-granular. A second-precise timestamp would
// change on every request and defeat prompt caching for BYOK providers; the date
// only changes once a day, so the system prompt stays cacheable within a day.
func DatePreamble(now time.Time) string {
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	return fmt.Sprintf(
		"Today is %s (%s), local time %s. Yesterday was %s.",
		now.Format("Monday"), today, now.Format("-07:00"), yesterday,
	)
}
