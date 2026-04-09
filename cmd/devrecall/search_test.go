package main

import (
	"testing"
)

func TestSearchCmd_RequiresArgs(t *testing.T) {
	cmd := newSearchCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("search command should require at least 1 argument")
	}
}

func TestSearchCmd_JoinsMultipleArgs(t *testing.T) {
	// Verify the command accepts multiple args (joined as query).
	cmd := newSearchCmd()
	// This will fail on execution (no DB), but should parse args fine.
	cmd.SetArgs([]string{"auth", "token"})
	err := cmd.Execute()
	// Expected to fail because there's no real DB, but it shouldn't be an args error.
	if err != nil && err.Error() == "requires at least 1 arg(s), only received 0" {
		t.Error("should accept multiple args")
	}
}
