package main

import (
	"strings"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func TestLogCmd_RequiresArgs(t *testing.T) {
	cmd := newLogCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("log command should require at least 1 argument")
	}
}

func TestBuildManualActivity_Defaults(t *testing.T) {
	now := time.Date(2026, 4, 8, 10, 30, 0, 0, time.UTC)

	a, err := buildManualActivity("Talked to mobile team", "", "", "", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Source != models.SourceManual {
		t.Errorf("source = %q, want manual", a.Source)
	}
	if a.Type != models.TypeNote {
		t.Errorf("type = %q, want note", a.Type)
	}
	if a.Title != "Talked to mobile team" {
		t.Errorf("title = %q", a.Title)
	}
	if a.Content != "Talked to mobile team" {
		t.Errorf("content = %q", a.Content)
	}
	if !a.Timestamp.Equal(now) {
		t.Errorf("timestamp = %v, want %v", a.Timestamp, now)
	}
	if a.Metadata != "" {
		t.Errorf("metadata should be empty for plain entry, got %q", a.Metadata)
	}
	if !strings.HasPrefix(a.SourceID, "manual-") {
		t.Errorf("source_id should start with manual-, got %q", a.SourceID)
	}
}

func TestBuildManualActivity_TitleTruncated(t *testing.T) {
	long := strings.Repeat("x", 500)
	a, err := buildManualActivity(long, "", "", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Title) != 200 {
		t.Errorf("title length = %d, want 200", len(a.Title))
	}
	if a.Content != long {
		t.Error("content should retain full text")
	}
}

func TestBuildManualActivity_TitleFromFirstLine(t *testing.T) {
	text := "Quick sync\nDiscussed Q2 priorities and deploys."
	a, err := buildManualActivity(text, "", "", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if a.Title != "Quick sync" {
		t.Errorf("title = %q, want Quick sync", a.Title)
	}
}

func TestBuildManualActivity_TagsAndPeople(t *testing.T) {
	a, err := buildManualActivity("Decision call", "", "decision, deploy", "anna@example.com,bob", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Metadata, `"tags":["decision","deploy"]`) {
		t.Errorf("metadata missing tags: %q", a.Metadata)
	}
	if !strings.Contains(a.Metadata, `"people":["anna@example.com","bob"]`) {
		t.Errorf("metadata missing people: %q", a.Metadata)
	}
}

func TestBuildManualActivity_AtFlag(t *testing.T) {
	now := time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		in   string
		want time.Time
	}{
		{"2026-04-07", time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)},
		{"2026-04-07 14:30", time.Date(2026, 4, 7, 14, 30, 0, 0, time.UTC)},
		{"2026-04-07T14:30", time.Date(2026, 4, 7, 14, 30, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		a, err := buildManualActivity("note", tt.in, "", "", now)
		if err != nil {
			t.Errorf("parse %q: %v", tt.in, err)
			continue
		}
		if !a.Timestamp.Equal(tt.want) {
			t.Errorf("parse %q: timestamp = %v, want %v", tt.in, a.Timestamp, tt.want)
		}
	}
}

func TestBuildManualActivity_InvalidAt(t *testing.T) {
	_, err := buildManualActivity("note", "not-a-date", "", "", time.Now())
	if err == nil {
		t.Error("expected error for invalid --at")
	}
}

func TestBuildManualActivity_EmptyText(t *testing.T) {
	_, err := buildManualActivity("   ", "", "", "", time.Now())
	if err == nil {
		t.Error("expected error for empty text")
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c", []string{"a", "b", "c"}},
		{",,a,,", []string{"a"}},
	}
	for _, tt := range tests {
		got := splitCSV(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestShortHash_Stable(t *testing.T) {
	a := shortHash("hello")
	b := shortHash("hello")
	c := shortHash("world")
	if a != b {
		t.Error("shortHash should be deterministic")
	}
	if a == c {
		t.Error("different inputs should yield different hashes")
	}
	if len(a) != 8 {
		t.Errorf("shortHash length = %d, want 8", len(a))
	}
}
