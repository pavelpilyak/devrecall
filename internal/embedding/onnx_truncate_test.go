package embedding

import (
	"strings"
	"testing"
)

// The backend panics during graph compilation on over-length input and fails
// the whole batch, so nothing may reach it above the cap.
func TestTruncateForModel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected rune length
	}{
		{"short text untouched", "a short commit message", len("a short commit message")},
		{"exactly at cap", strings.Repeat("a", maxInputChars), maxInputChars},
		{"long ascii clipped", strings.Repeat("a", 50_000), maxInputChars},
		{"long multibyte clipped", strings.Repeat("日", 50_000), maxInputChars},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateForModel(tt.in)
			if n := len([]rune(got)); n != tt.want {
				t.Errorf("length = %d runes, want %d", n, tt.want)
			}
			if !strings.HasPrefix(tt.in, got) {
				t.Error("truncation should keep a prefix of the input")
			}
		})
	}
}

// Clipping mid-codepoint would emit invalid UTF-8 into the tokenizer.
func TestTruncateForModel_RuneSafe(t *testing.T) {
	got := truncateForModel(strings.Repeat("é", 50_000))
	if !utf8Valid(got) {
		t.Error("truncation split a multi-byte rune")
	}
	if len([]rune(got)) != maxInputChars {
		t.Errorf("got %d runes, want %d", len([]rune(got)), maxInputChars)
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
