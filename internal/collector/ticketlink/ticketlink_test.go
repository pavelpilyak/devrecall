package ticketlink

import (
	"reflect"
	"testing"
)

func TestExtractFromMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    []string
	}{
		{
			name:    "single key",
			message: "PROJ-123: fix payment retry logic",
			want:    []string{"PROJ-123"},
		},
		{
			name:    "multiple keys",
			message: "PROJ-123 PROJ-456: fix retry and update tests",
			want:    []string{"PROJ-123", "PROJ-456"},
		},
		{
			name:    "key in middle of message",
			message: "fix ENG-42 auth refresh issue",
			want:    []string{"ENG-42"},
		},
		{
			name:    "duplicate keys",
			message: "PROJ-123: fix PROJ-123 retry logic",
			want:    []string{"PROJ-123"},
		},
		{
			name:    "no keys",
			message: "fix auth refresh issue",
			want:    nil,
		},
		{
			name:    "lowercase ignored in message",
			message: "proj-123: fix something",
			want:    nil,
		},
		{
			name:    "key with prefix text",
			message: "[PROJ-123] fix retry backoff",
			want:    []string{"PROJ-123"},
		},
		{
			name:    "multiple projects",
			message: "PROJ-123 ENG-456 AB-1: cross-team fix",
			want:    []string{"PROJ-123", "ENG-456", "AB-1"},
		},
		{
			name:    "key with numbers in project",
			message: "FE2-99: update styles",
			want:    []string{"FE2-99"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFromMessage(tt.message)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractFromMessage(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}

func TestExtractFromBranch(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		want   []string
	}{
		{
			name:   "lowercase key",
			branch: "eng-456-fix-auth",
			want:   []string{"ENG-456"},
		},
		{
			name:   "uppercase key",
			branch: "PROJ-123-update-ui",
			want:   []string{"PROJ-123"},
		},
		{
			name:   "with feature prefix",
			branch: "feature/proj-123-update",
			want:   []string{"PROJ-123"},
		},
		{
			name:   "with bugfix prefix",
			branch: "bugfix/eng-42-fix-crash",
			want:   []string{"ENG-42"},
		},
		{
			name:   "no key",
			branch: "feature/update-auth-flow",
			want:   nil,
		},
		{
			name:   "multiple keys",
			branch: "proj-123-eng-456-cross-fix",
			want:   []string{"PROJ-123", "ENG-456"},
		},
		{
			name:   "main branch",
			branch: "main",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFromBranch(tt.branch)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractFromBranch(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name    string
		message string
		branch  string
		want    []string
	}{
		{
			name:    "key from both sources deduplicated",
			message: "PROJ-123: fix retry",
			branch:  "proj-123-fix-retry",
			want:    []string{"PROJ-123"},
		},
		{
			name:    "different keys from each source",
			message: "PROJ-123: fix retry",
			branch:  "eng-456-related",
			want:    []string{"PROJ-123", "ENG-456"},
		},
		{
			name:    "only from message",
			message: "PROJ-123: fix",
			branch:  "main",
			want:    []string{"PROJ-123"},
		},
		{
			name:    "only from branch",
			message: "fix auth issue",
			branch:  "eng-42-fix",
			want:    []string{"ENG-42"},
		},
		{
			name:    "no keys anywhere",
			message: "fix auth",
			branch:  "main",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Extract(tt.message, tt.branch)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Extract(%q, %q) = %v, want %v", tt.message, tt.branch, got, tt.want)
			}
		})
	}
}
