package main

import (
	"testing"
)

func TestTimelineCmd_RequiresArgs(t *testing.T) {
	cmd := newTimelineCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("timeline command should require exactly 1 argument")
	}
}

func TestTimelineCmd_AcceptsPeriod(t *testing.T) {
	cmd := newTimelineCmd()
	cmd.SetArgs([]string{"Q1-2026"})
	// Will fail on DB open, but should parse args fine.
	err := cmd.Execute()
	if err != nil && err.Error() == "accepts 1 arg(s), received 0" {
		t.Error("should accept period argument")
	}
}
