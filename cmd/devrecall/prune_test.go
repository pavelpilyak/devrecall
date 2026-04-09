package main

import (
	"testing"
	"time"
)

func TestParseOlderThan(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
		err   bool
	}{
		{"1y", 365 * 24 * time.Hour, false},
		{"2y", 2 * 365 * 24 * time.Hour, false},
		{"6m", 6 * 30 * 24 * time.Hour, false},
		{"90d", 90 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"", 0, true},
		{"x", 0, true},
		{"0d", 0, true},
		{"-1y", 0, true},
		{"1x", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseOlderThan(tt.input)
			if tt.err {
				if err == nil {
					t.Errorf("parseOlderThan(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOlderThan(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseOlderThan(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestPruneCmd_HasFlags(t *testing.T) {
	cmd := newPruneCmd()

	for _, flag := range []string{"older-than", "keep-summaries", "dry-run", "force"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing flag: --%s", flag)
		}
	}
}
