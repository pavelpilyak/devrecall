package main

import "testing"

func TestShouldSkipMandatoryCheck(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"bare update", []string{"devrecall", "update"}, true},
		{"update with --yes", []string{"devrecall", "update", "--yes"}, true},
		{"flag before update", []string{"devrecall", "-y", "update"}, true},
		{"flags then update", []string{"devrecall", "--verbose", "update"}, true},
		{"sync command", []string{"devrecall", "sync"}, false},
		{"standup command", []string{"devrecall", "standup"}, false},
		{"sync with update arg", []string{"devrecall", "sync", "update"}, false},
		{"empty argv", []string{"devrecall"}, false},
	}
	for _, tc := range cases {
		got := shouldSkipMandatoryCheck(tc.args)
		if got != tc.want {
			t.Errorf("%s: shouldSkipMandatoryCheck(%v) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}
