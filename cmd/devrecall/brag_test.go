package main

import (
	"testing"
	"time"
)

func TestParsePeriodArg(t *testing.T) {
	tests := []struct {
		input       string
		wantAfter   string
		wantBefore  string
		wantErr     bool
	}{
		{"Q1-2026", "2026-01-01", "2026-04-01", false},
		{"Q2-2026", "2026-04-01", "2026-07-01", false},
		{"Q3-2026", "2026-07-01", "2026-10-01", false},
		{"Q4-2026", "2026-10-01", "2027-01-01", false},
		{"q1-2026", "2026-01-01", "2026-04-01", false},
		{"2026-03", "2026-03-01", "2026-04-01", false},
		{"2026-12", "2026-12-01", "2027-01-01", false},
		{"2026-03-01..2026-03-31", "2026-03-01", "2026-04-01", false},
		{"last-month", "", "", false},    // relative, just check no error
		{"last-quarter", "", "", false},   // relative, just check no error
		{"this-month", "", "", false},
		{"this-quarter", "", "", false},
		{"banana", "", "", true},
		{"Q0-2026", "", "", true},
		{"Q5-2026", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			after, before, err := parsePeriodArg(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parsePeriodArg(%q) = no error, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePeriodArg(%q) error: %v", tt.input, err)
			}
			if after.IsZero() || before.IsZero() {
				t.Fatalf("parsePeriodArg(%q) returned zero times", tt.input)
			}
			if !before.After(after) {
				t.Errorf("before (%v) should be after (%v)", before, after)
			}

			// Check exact values for non-relative periods.
			if tt.wantAfter != "" {
				expected, _ := time.Parse("2006-01-02", tt.wantAfter)
				if !after.Equal(expected) {
					t.Errorf("after = %v, want %v", after.Format("2006-01-02"), tt.wantAfter)
				}
			}
			if tt.wantBefore != "" {
				expected, _ := time.Parse("2006-01-02", tt.wantBefore)
				if !before.Equal(expected) {
					t.Errorf("before = %v, want %v", before.Format("2006-01-02"), tt.wantBefore)
				}
			}
		})
	}
}
