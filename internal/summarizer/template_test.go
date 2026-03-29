package summarizer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func meta(repo, sha string, files, ins, del int) string {
	b, _ := json.Marshal(commitMeta{
		Repo: repo, SHA: sha, FilesChanged: files, Insertions: ins, Deletions: del,
	})
	return string(b)
}

func activity(title, repo, sha string, files, ins, del int, ts time.Time) models.Activity {
	return models.Activity{
		Source:    models.SourceGit,
		Type:     models.TypeCommit,
		Title:    title,
		Metadata: meta(repo, sha, files, ins, del),
		Timestamp: ts,
	}
}

func TestStandup_Empty(t *testing.T) {
	s := NewTemplateSummarizer()
	out, err := s.Standup(nil)
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}
	if !strings.Contains(out, "No activity") {
		t.Errorf("expected no-activity message, got %q", out)
	}
}

func TestStandup_SingleCommit(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 14, 30, 0, 0, time.UTC) // Friday

	out, err := s.Standup([]models.Activity{
		activity("Fix auth refresh", "backend-api", "abc123def456", 3, 47, 12, ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Friday (2026-03-27):") {
		t.Errorf("missing date header in:\n%s", out)
	}
	if !strings.Contains(out, "- backend-api: Fix auth refresh (abc123d) — 3 files, +47/-12") {
		t.Errorf("unexpected bullet format in:\n%s", out)
	}
}

func TestStandup_GroupsByRepo(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Fix auth", "backend-api", "aaa1111", 3, 47, 12, ts),
		activity("Add tests", "backend-api", "bbb2222", 1, 10, 0, ts.Add(time.Hour)),
		activity("Update login form", "frontend-app", "ccc3333", 2, 20, 5, ts.Add(2*time.Hour)),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	lines := strings.Split(out, "\n")
	// Should have: header + 3 bullets = 4 lines.
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), out)
	}
	// backend-api commits should come before frontend-app (alphabetical).
	if !strings.HasPrefix(lines[1], "- backend-api:") {
		t.Errorf("first bullet should be backend-api: %q", lines[1])
	}
	if !strings.HasPrefix(lines[3], "- frontend-app:") {
		t.Errorf("third bullet should be frontend-app: %q", lines[3])
	}
}

func TestStandup_MultipleDays(t *testing.T) {
	s := NewTemplateSummarizer()
	day1 := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Day 1 work", "repo", "aaa", 1, 5, 0, day1),
		activity("Day 2 work", "repo", "bbb", 2, 10, 3, day2),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	if !strings.Contains(out, "Friday (2026-03-27):") {
		t.Errorf("missing day 1 header:\n%s", out)
	}
	if !strings.Contains(out, "Saturday (2026-03-28):") {
		t.Errorf("missing day 2 header:\n%s", out)
	}
}

func TestStandup_NoStats(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	out, err := s.Standup([]models.Activity{
		activity("Empty commit", "repo", "abc", 0, 0, 0, ts),
	})
	if err != nil {
		t.Fatalf("Standup: %v", err)
	}

	// Should not have the " — " stats separator.
	if strings.Contains(out, " — ") {
		t.Errorf("should not show stats for zero-stat commit:\n%s", out)
	}
}

func TestStandup_ShortSHA(t *testing.T) {
	s := NewTemplateSummarizer()
	ts := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)

	// Short SHA (<=7) should be used as-is.
	out, _ := s.Standup([]models.Activity{
		activity("Short sha", "repo", "abc", 1, 1, 0, ts),
	})
	if !strings.Contains(out, "(abc)") {
		t.Errorf("short SHA should be used as-is:\n%s", out)
	}
}
