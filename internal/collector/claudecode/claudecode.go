// Package claudecode collects Claude Code sessions from the transcripts the
// CLI already writes to ~/.claude/projects/<slugged-cwd>/<session-id>.jsonl.
//
// It is a local collector like git: no API, no OAuth, no network. That matters
// beyond convenience — an increasing share of a developer's work now happens in
// agent sessions that leave no trace in commit history, and this is the only way
// to capture it without shipping conversations to a vendor.
package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pavelpilyak/devrecall/internal/collector/ticketlink"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// maxPromptChars caps how much prompt text one activity carries. Sessions can
// run to hundreds of turns; the opening prompts describe the intent, which is
// what search needs, and the rest is mostly iteration noise.
//
// The cap is a hard ceiling, not a budget to overshoot: the embedding model has
// a 512-token sequence limit, and a single oversized row fails the whole batch
// it is embedded in — taking unrelated activities down with it.
const maxPromptChars = 2000

// maxSinglePromptChars bounds one prompt before it counts against the budget.
// Pasted specs, logs, and stack traces are routinely tens of KB and would blow
// the total on their own.
const maxSinglePromptChars = 600

// scanBuffer is the per-line cap for the transcript scanner. Individual records
// embed tool results and file contents and routinely exceed bufio's 64KB
// default, which would otherwise truncate a session mid-parse.
const scanBuffer = 8 * 1024 * 1024

// Collector reads Claude Code session transcripts from a projects directory.
type Collector struct {
	dir string
}

// New returns a collector reading from projectsDir (typically
// ~/.claude/projects). An empty dir resolves to the default location.
func New(projectsDir string) *Collector {
	if projectsDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			projectsDir = filepath.Join(home, ".claude", "projects")
		}
	}
	return &Collector{dir: projectsDir}
}

func (c *Collector) Name() models.Source { return models.SourceClaudeCode }

// Collect returns every session found on disk.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Time{})
}

// CollectSince returns sessions whose last activity is at or after `since`.
// A zero `since` collects everything.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	if c.dir == "" {
		return nil, nil
	}
	// A missing directory just means Claude Code isn't installed — not an error.
	if _, err := os.Stat(c.dir); os.IsNotExist(err) {
		return nil, nil
	}

	var files []string
	err := filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries shouldn't abort the whole scan
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", c.dir, err)
	}
	sort.Strings(files)

	var out []models.Activity
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		s, err := parseSession(path)
		// One corrupt transcript shouldn't lose the rest.
		if err != nil || s == nil || s.Start.IsZero() {
			continue
		}
		if !since.IsZero() && s.End.Before(since) {
			continue
		}
		out = append(out, s.toActivity())
	}
	// Chronological output keeps sync order stable across runs.
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

// session is the aggregate built from one transcript file.
type session struct {
	ID          string
	CWD         string
	Branch      string
	CustomTitle string
	AITitle     string
	Prompts     []string
	PromptCount int
	Start       time.Time
	End         time.Time
	PRURLs      []string
	Version     string
}

// record is the subset of a transcript line we care about. Claude Code writes
// several record types per session; unknown ones are ignored.
type record struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"sessionId"`
	Timestamp   string          `json:"timestamp"`
	CWD         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Version     string          `json:"version"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	CustomTitle string          `json:"customTitle"`
	AITitle     string          `json:"aiTitle"`
	PRURL       string          `json:"prUrl"`
	Message     json.RawMessage `json:"message"`
}

func parseSession(path string) (*session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := &session{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	promptChars := 0
	seenPR := map[string]bool{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), scanBuffer)
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}

		if r.CWD != "" {
			s.CWD = r.CWD
		}
		if r.GitBranch != "" {
			s.Branch = r.GitBranch
		}
		if r.Version != "" {
			s.Version = r.Version
		}
		if ts := parseTime(r.Timestamp); !ts.IsZero() {
			if s.Start.IsZero() || ts.Before(s.Start) {
				s.Start = ts
			}
			if ts.After(s.End) {
				s.End = ts
			}
		}

		switch r.Type {
		case "custom-title":
			s.CustomTitle = r.CustomTitle
		case "ai-title":
			s.AITitle = r.AITitle
		case "pr-link":
			// Claude Code re-emits pr-link on every turn after a PR is opened,
			// so a long session yields hundreds of copies of the same URL.
			if r.PRURL != "" && !seenPR[r.PRURL] {
				seenPR[r.PRURL] = true
				s.PRURLs = append(s.PRURLs, r.PRURL)
			}
		case "user":
			// Sidechain messages are subagent traffic and meta messages are
			// harness bookkeeping — neither is something the user typed.
			if r.IsMeta || r.IsSidechain {
				continue
			}
			text := userText(r.Message)
			if text == "" {
				continue
			}
			s.PromptCount++
			if promptChars >= maxPromptChars {
				continue
			}
			text = truncate(text, maxSinglePromptChars)
			if room := maxPromptChars - promptChars; len(text) > room {
				text = truncate(text, room)
			}
			s.Prompts = append(s.Prompts, text)
			promptChars += len(text)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

// userText pulls the human-typed text out of a user record. Content is either a
// plain string or a list of parts, where only `text` parts are the user talking
// — `tool_result` parts are tool output echoed back under the user role.
func userText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ""
	}

	var asString string
	if err := json.Unmarshal(msg.Content, &asString); err == nil {
		return cleanPrompt(asString)
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			b.WriteString(p.Text)
		}
	}
	return cleanPrompt(b.String())
}

// cleanPrompt drops harness-generated wrappers (slash-command echoes, local
// command output, context-compaction notices) that aren't things the user said.
func cleanPrompt(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, marker := range []string{
		"<command-name>", "<local-command-caveat>", "<command-message>",
		"<system-reminder>", "Caveat: The messages below were generated",
		"This session is being continued from a previous conversation",
	} {
		if strings.HasPrefix(s, marker) {
			return ""
		}
	}
	return s
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// sessionMeta is the metadata blob stored on the activity. Repo and IssueKeys
// are what let work-item linking fold a session in beside the commits and PRs
// from the same piece of work.
type sessionMeta struct {
	SessionID   string `json:"session_id"`
	Tool        string `json:"tool"`
	Repo        string `json:"repo,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PromptCount int    `json:"prompt_count"`
	// SpanMinutes is wall-clock first-to-last touch, NOT time spent: sessions
	// are routinely resumed days later, so treating this as effort would report
	// absurd numbers in a brag doc.
	SpanMinutes int      `json:"span_minutes"`
	StartedAt   string   `json:"started_at,omitempty"`
	IssueKeys   []string `json:"issue_keys,omitempty"`
	PRURLs      []string `json:"pr_urls,omitempty"`
	Version     string   `json:"version,omitempty"`
}

func (s *session) toActivity() models.Activity {
	repo := ""
	if s.CWD != "" {
		repo = filepath.Base(s.CWD)
	}

	// Branch names are the reliable ticket signal here: a session on
	// `feature/PROJ-123-retry` belongs to PROJ-123 even when no prompt says so.
	var issueKeys []string
	if s.Branch != "" {
		issueKeys = ticketlink.ExtractFromBranch(s.Branch)
	}

	meta := sessionMeta{
		SessionID:   s.ID,
		Tool:        "claude-code",
		Repo:        repo,
		CWD:         s.CWD,
		Branch:      s.Branch,
		PromptCount: s.PromptCount,
		SpanMinutes: int(s.End.Sub(s.Start).Minutes()),
		StartedAt:   s.Start.UTC().Format(time.RFC3339),
		IssueKeys:   issueKeys,
		PRURLs:      s.PRURLs,
		Version:     s.Version,
	}
	metaJSON, _ := json.Marshal(meta)

	return models.Activity{
		Source:    models.SourceClaudeCode,
		SourceID:  s.ID,
		Type:      models.TypeSession,
		Title:     s.title(repo),
		Content:   strings.Join(s.Prompts, "\n"),
		Metadata:  string(metaJSON),
		Timestamp: s.End,
	}
}

// title prefers a title the user set, then one Claude generated, then the
// opening prompt, and finally the repo name so a row is never blank.
func (s *session) title(repo string) string {
	if t := strings.TrimSpace(s.CustomTitle); t != "" {
		return t
	}
	if t := strings.TrimSpace(s.AITitle); t != "" {
		return t
	}
	for _, p := range s.Prompts {
		if line := firstMeaningfulLine(p); line != "" {
			return truncate(line, 80)
		}
	}
	if repo != "" {
		return "Claude Code session in " + repo
	}
	return "Claude Code session"
}

// firstMeaningfulLine returns the first line carrying actual words. Prompts
// often open with a separator rule or banner, which makes a useless title.
func firstMeaningfulLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		letters := 0
		for _, r := range line {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				letters++
			}
		}
		// Require the line to be mostly words, not decoration.
		if letters*2 >= len([]rune(line)) {
			return line
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// truncate clips s to at most n bytes without splitting a rune. Slicing a Go
// string is byte-indexed, so a naive s[:n] lands mid-codepoint whenever the cut
// falls inside a multi-byte character — box-drawing rules and emoji in pasted
// terminal output do this constantly — and stores invalid UTF-8.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Walk back to the start of the rune that straddles the boundary.
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}
