package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// writeSession lays out a transcript the way Claude Code does:
// <dir>/<slugged-cwd>/<session-id>.jsonl
func writeSession(t *testing.T, dir, project, id string, lines ...string) {
	t.Helper()
	pdir := filepath.Join(dir, project)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(pdir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func userLine(ts, cwd, branch, text string) string {
	return `{"type":"user","timestamp":"` + ts + `","cwd":"` + cwd + `","gitBranch":"` + branch +
		`","message":{"role":"user","content":` + strconv(text) + `}}`
}

func strconv(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func collect(t *testing.T, dir string) []models.Activity {
	t.Helper()
	acts, err := New(dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return acts
}

func metaOf(t *testing.T, a models.Activity) sessionMeta {
	t.Helper()
	var m sessionMeta
	if err := json.Unmarshal([]byte(a.Metadata), &m); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	return m
}

func TestCollect_BuildsOneActivityPerSession(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-me-Pets-api", "sess-1",
		userLine("2026-08-01T10:00:00Z", "/Users/me/Pets/api", "main", "add retry to the client"),
		userLine("2026-08-01T10:30:00Z", "/Users/me/Pets/api", "main", "now add a test"),
		`{"type":"ai-title","aiTitle":"Add retry logic","sessionId":"sess-1"}`,
	)

	acts := collect(t, dir)
	if len(acts) != 1 {
		t.Fatalf("got %d activities, want 1", len(acts))
	}
	a := acts[0]
	if a.Source != models.SourceClaudeCode || a.Type != models.TypeSession {
		t.Errorf("source/type = %s/%s", a.Source, a.Type)
	}
	if a.Title != "Add retry logic" {
		t.Errorf("title = %q, want the ai-title", a.Title)
	}
	// Timestamp is last touch, so a session resumed later lands on the day it
	// was actually worked on rather than the day it was opened.
	if !a.Timestamp.Equal(time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v, want the last message time", a.Timestamp)
	}
	m := metaOf(t, a)
	if m.Repo != "api" || m.Branch != "main" || m.PromptCount != 2 {
		t.Errorf("meta = %+v", m)
	}
	if !strings.Contains(a.Content, "add retry to the client") {
		t.Errorf("content missing the prompt text: %q", a.Content)
	}
}

func TestCollect_TitlePrecedence(t *testing.T) {
	dir := t.TempDir()
	// A user-set title beats a generated one.
	writeSession(t, dir, "p", "both",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "hello"),
		`{"type":"ai-title","aiTitle":"Generated","sessionId":"both"}`,
		`{"type":"custom-title","customTitle":"Mine","sessionId":"both"}`,
	)
	// With no titles at all, fall back to the opening prompt.
	writeSession(t, dir, "p", "neither",
		userLine("2026-08-02T10:00:00Z", "/x/repo", "main", "fix the flaky login test"),
	)

	byID := map[string]string{}
	for _, a := range collect(t, dir) {
		byID[a.SourceID] = a.Title
	}
	if byID["both"] != "Mine" {
		t.Errorf("custom title should win, got %q", byID["both"])
	}
	if byID["neither"] != "fix the flaky login test" {
		t.Errorf("prompt fallback = %q", byID["neither"])
	}
}

// A prompt opening with a separator or banner used to become the title, which
// produced rows titled "========================".
func TestCollect_SkipsDecorativeLinesInTitle(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p", "banner",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main",
			"========================\n>>> ### ---\nrewrite the parser"),
	)
	got := collect(t, dir)[0].Title
	if got != "rewrite the parser" {
		t.Errorf("title = %q, want the first line with actual words", got)
	}
}

// Meta and sidechain records are harness bookkeeping and subagent traffic, and
// tool_result parts are tool output echoed under the user role — none of it is
// the user talking, so none of it should count as a prompt.
func TestCollect_IgnoresNonUserContent(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p", "noise",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "real prompt"),
		`{"type":"user","isMeta":true,"timestamp":"2026-08-01T10:01:00Z","message":{"role":"user","content":"meta noise"}}`,
		`{"type":"user","isSidechain":true,"timestamp":"2026-08-01T10:02:00Z","message":{"role":"user","content":"subagent noise"}}`,
		`{"type":"user","timestamp":"2026-08-01T10:03:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"tool output"}]}}`,
		`{"type":"user","timestamp":"2026-08-01T10:04:00Z","message":{"role":"user","content":[{"type":"text","text":"second real prompt"}]}}`,
		`{"type":"user","timestamp":"2026-08-01T10:05:00Z","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
	)
	a := collect(t, dir)[0]
	if got := metaOf(t, a).PromptCount; got != 2 {
		t.Errorf("prompt_count = %d, want 2 (real prompts only)", got)
	}
	for _, unwanted := range []string{"meta noise", "subagent noise", "tool output", "command-name"} {
		if strings.Contains(a.Content, unwanted) {
			t.Errorf("content leaked %q", unwanted)
		}
	}
}

// Branch names are the reliable ticket signal, so a session on a ticket branch
// can be folded into the same work item as its commits and PRs.
func TestCollect_ExtractsIssueKeysFromBranch(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p", "ticketed",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "feature/PROJ-123-retry", "work"),
	)
	m := metaOf(t, collect(t, dir)[0])
	if len(m.IssueKeys) != 1 || m.IssueKeys[0] != "PROJ-123" {
		t.Errorf("issue_keys = %v, want [PROJ-123]", m.IssueKeys)
	}
}

func TestCollect_CapturesPRLinks(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p", "withpr",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "open a PR"),
		`{"type":"pr-link","sessionId":"withpr","prNumber":42,"prUrl":"https://github.com/o/r/pull/42"}`,
	)
	m := metaOf(t, collect(t, dir)[0])
	if len(m.PRURLs) != 1 || m.PRURLs[0] != "https://github.com/o/r/pull/42" {
		t.Errorf("pr_urls = %v", m.PRURLs)
	}
}

// Claude Code re-emits pr-link on every turn once a PR exists, so a long
// session produced hundreds of copies of the same URL in metadata.
func TestCollect_DedupesRepeatedPRLinks(t *testing.T) {
	lines := []string{userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "open a PR")}
	for i := 0; i < 200; i++ {
		lines = append(lines, `{"type":"pr-link","sessionId":"dup","prUrl":"https://github.com/o/r/pull/42"}`)
	}
	lines = append(lines, `{"type":"pr-link","sessionId":"dup","prUrl":"https://github.com/o/r/pull/99"}`)

	dir := t.TempDir()
	writeSession(t, dir, "p", "dup", lines...)

	m := metaOf(t, collect(t, dir)[0])
	if len(m.PRURLs) != 2 {
		t.Errorf("pr_urls has %d entries, want 2 distinct", len(m.PRURLs))
	}
	// Order should follow first appearance, not map iteration.
	if m.PRURLs[0] != "https://github.com/o/r/pull/42" {
		t.Errorf("pr_urls[0] = %q, want the first-seen URL", m.PRURLs[0])
	}
}

func TestCollectSince_FiltersOldSessions(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p", "old",
		userLine("2026-01-01T10:00:00Z", "/x/repo", "main", "ancient"))
	writeSession(t, dir, "p", "new",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "recent"))

	acts, err := New(dir).CollectSince(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	if len(acts) != 1 || acts[0].SourceID != "new" {
		t.Fatalf("got %d activities (%v), want just the recent one", len(acts), acts)
	}
}

// Claude Code simply may not be installed — that's normal, not a sync failure.
func TestCollect_MissingDirectoryIsNotAnError(t *testing.T) {
	acts, err := New(filepath.Join(t.TempDir(), "nope")).Collect(context.Background())
	if err != nil {
		t.Errorf("missing dir returned error: %v", err)
	}
	if len(acts) != 0 {
		t.Errorf("got %d activities from a missing dir", len(acts))
	}
}

// One unreadable transcript shouldn't cost you every other session.
func TestCollect_SkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p", "corrupt",
		"{not json at all",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "still parsed"),
		"}{",
	)
	acts := collect(t, dir)
	if len(acts) != 1 || !strings.Contains(acts[0].Content, "still parsed") {
		t.Fatalf("corrupt lines broke parsing: %+v", acts)
	}
}

func TestCollect_SortedChronologically(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p", "zzz-late",
		userLine("2026-08-05T10:00:00Z", "/x/repo", "main", "later"))
	writeSession(t, dir, "p", "aaa-early",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "earlier"))

	acts := collect(t, dir)
	if len(acts) != 2 || acts[0].SourceID != "aaa-early" {
		t.Fatalf("not sorted by time: %v", acts)
	}
}

// A single oversized prompt used to blow past the cap entirely, producing a row
// far beyond the embedding model's 512-token limit — which fails the whole
// batch it lands in, taking unrelated activities with it.
func TestCollect_ContentIsHardCapped(t *testing.T) {
	huge := strings.Repeat("pasted stack trace line. ", 4000) // ~100KB
	dir := t.TempDir()
	writeSession(t, dir, "p", "huge",
		userLine("2026-08-01T10:00:00Z", "/x/repo", "main", "short opener"),
		userLine("2026-08-01T10:01:00Z", "/x/repo", "main", huge),
		userLine("2026-08-01T10:02:00Z", "/x/repo", "main", huge),
	)
	a := collect(t, dir)[0]
	if len(a.Content) > maxPromptChars {
		t.Errorf("content is %d chars, exceeds the %d cap", len(a.Content), maxPromptChars)
	}
	// The cap must not cost us the count of what actually happened.
	if got := metaOf(t, a).PromptCount; got != 3 {
		t.Errorf("prompt_count = %d, want 3 — truncation shouldn't lose the tally", got)
	}
	if !strings.Contains(a.Content, "short opener") {
		t.Error("dropped the opening prompt, which is the part that describes intent")
	}
}

// Go strings are byte-indexed, so a naive s[:n] splits any multi-byte rune
// straddling the cut and writes invalid UTF-8 into the database. Pasted
// terminal output (box-drawing rules, emoji) hits this constantly.
func TestCollect_TruncationKeepsValidUTF8(t *testing.T) {
	// Box-drawing "─" is 3 bytes, so a byte-aligned cut at 600/2000 is
	// guaranteed to land mid-rune for some repeat count.
	for _, filler := range []string{"─", "é", "🙂", "日本語"} {
		t.Run(filler, func(t *testing.T) {
			dir := t.TempDir()
			writeSession(t, dir, "p", "uni",
				userLine("2026-08-01T10:00:00Z", "/x/repo", "main",
					strings.Repeat(filler, 5000)),
				userLine("2026-08-01T10:01:00Z", "/x/repo", "main",
					strings.Repeat(filler, 5000)),
			)
			a := collect(t, dir)[0]
			if !utf8.ValidString(a.Content) {
				t.Errorf("content is not valid UTF-8 after truncation (filler %q)", filler)
			}
			if !utf8.ValidString(a.Title) {
				t.Errorf("title is not valid UTF-8 after truncation (filler %q)", filler)
			}
			if len(a.Content) > maxPromptChars+8 { // +8 allows the ellipsis bytes
				t.Errorf("content is %d bytes, over the %d cap", len(a.Content), maxPromptChars)
			}
		})
	}
}
