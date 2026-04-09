package main

import (
	"testing"
	"time"
)

func TestFormatTimeAgo(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{59 * time.Second, "just now"},
		{1 * time.Minute, "1 minute ago"},
		{30 * time.Minute, "30 minutes ago"},
		{59 * time.Minute, "59 minutes ago"},
		{1 * time.Hour, "1 hour ago"},
		{2 * time.Hour, "2 hours ago"},
		{23 * time.Hour, "23 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{48 * time.Hour, "2 days ago"},
		{7 * 24 * time.Hour, "7 days ago"},
	}

	for _, tt := range tests {
		got := formatTimeAgo(tt.d)
		if got != tt.want {
			t.Errorf("formatTimeAgo(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
