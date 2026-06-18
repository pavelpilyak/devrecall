package git

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/collector/ticketlink"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// commitMeta holds per-commit diff stats stored as JSON in Activity.Metadata.
type commitMeta struct {
	Repo         string   `json:"repo"`
	SHA          string   `json:"sha"`
	AuthorName   string   `json:"author_name"`
	AuthorEmail  string   `json:"author_email"`
	FilesChanged int      `json:"files_changed"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
	IssueKeys    []string `json:"issue_keys,omitempty"`
}

// Separator used in git log format. Chosen to be unlikely in commit messages.
const fieldSep = "‡"

// recordSep delimits commits in git log output.
const recordSep = "‡‡‡"

// logFormat is the --format string passed to git log.
// Fields: SHA, ISO date, subject, author name, author email — all on the
// first line. %n%b appends the commit body on subsequent lines, ending at
// the shortstat line (or end of block).
var logFormat = strings.Join([]string{"%H", "%aI", "%s", "%an", "%ae"}, fieldSep) + "%n%b"

// maxBodyBytes caps how much commit body text we store per row. Generous
// enough for typical design-doc-style commits, small enough to keep the DB
// and embeddings tractable. Truncated bodies get an ellipsis suffix.
const maxBodyBytes = 4096

// statRe matches the summary line of --shortstat output.
var statRe = regexp.MustCompile(
	`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`,
)

// Collector gathers commit activity from local git repositories.
type Collector struct {
	repos  []string
	emails []string
}

// New creates a git collector for the given repos and author emails.
func New(repos, emails []string) *Collector {
	return &Collector{repos: repos, emails: emails}
}

func (c *Collector) Name() models.Source {
	return models.SourceGit
}

// Collect scans each configured repo and returns commits authored by the
// configured emails. Repos that fail (e.g. missing directory) are skipped
// with no error — partial results are better than none.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	// Without at least one self-email we cannot tell the user's commits from
	// their teammates'. `git log --all` with no --author would ingest every
	// contributor across every branch, so collect nothing rather than pollute
	// the timeline with other people's work. (This guards a fresh machine
	// where neither config nor `git config user.email` yields an identity.)
	authors := nonEmptyEmails(c.emails)
	if len(authors) == 0 {
		return nil, nil
	}

	var all []models.Activity
	for _, repo := range c.repos {
		activities, err := c.collectRepo(ctx, repo, authors)
		if err != nil {
			continue // skip broken repos
		}
		all = append(all, activities...)
	}
	return all, nil
}

func (c *Collector) collectRepo(ctx context.Context, repoPath string, authors []string) ([]models.Activity, error) {
	args := []string{
		"log",
		"--all",
		"--format=" + recordSep + "%n" + logFormat,
		"--shortstat",
	}
	for _, email := range authors {
		args = append(args, "--author="+email)
	}

	out, err := runGit(ctx, repoPath, args...)
	if err != nil {
		return nil, err
	}

	// Get current branch name for ticket key extraction.
	branch := currentBranch(ctx, repoPath)

	repoName := filepath.Base(repoPath)
	return parseLog(repoName, repoPath, branch, out)
}

// nonEmptyEmails trims and drops blank entries so a stray "" in config can't
// turn into an empty `--author=` regex that matches every author.
func nonEmptyEmails(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// currentBranch returns the current branch name, or empty string on error.
func currentBranch(ctx context.Context, repoPath string) string {
	out, err := runGit(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// parseLog splits raw git log output into Activity records.
func parseLog(repoName, repoPath, branch string, raw []byte) ([]models.Activity, error) {
	// Split on record separator. First element is empty (before first record).
	parts := strings.Split(string(raw), recordSep)

	var activities []models.Activity
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		act, err := parseCommit(repoName, repoPath, branch, part)
		if err != nil {
			continue // skip malformed entries
		}
		activities = append(activities, act)
	}
	return activities, nil
}

// parseCommit parses a single commit block (header line + optional stat line).
func parseCommit(repoName, repoPath, branch, block string) (models.Activity, error) {
	scanner := bufio.NewScanner(strings.NewReader(block))

	// First non-empty line is the header.
	var headerLine string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			headerLine = line
			break
		}
	}
	if headerLine == "" {
		return models.Activity{}, fmt.Errorf("empty commit block")
	}

	fields := strings.SplitN(headerLine, fieldSep, 5)
	if len(fields) < 5 {
		return models.Activity{}, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}

	sha := fields[0]
	ts, err := time.Parse(time.RFC3339, fields[1])
	if err != nil {
		return models.Activity{}, fmt.Errorf("bad timestamp %q: %w", fields[1], err)
	}
	subject := fields[2]
	authorName := fields[3]
	authorEmail := fields[4]

	// After the header line, every subsequent non-empty line is either the
	// shortstat (matched by statRe) or part of the commit body.
	var bodyLines []string
	var filesChanged, insertions, deletions int
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if m := statRe.FindStringSubmatch(trimmed); m != nil {
			filesChanged, _ = strconv.Atoi(m[1])
			insertions, _ = strconv.Atoi(m[2])
			deletions, _ = strconv.Atoi(m[3])
			continue
		}
		bodyLines = append(bodyLines, raw)
	}
	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "…"
	}

	// Extract ticket keys from the full commit message (subject + body) and
	// branch name — keys mentioned only in the body weren't caught before.
	fullMessage := subject
	if body != "" {
		fullMessage = subject + "\n" + body
	}
	issueKeys := ticketlink.Extract(fullMessage, branch)

	meta := commitMeta{
		Repo:         repoName,
		SHA:          sha,
		AuthorName:   authorName,
		AuthorEmail:  authorEmail,
		FilesChanged: filesChanged,
		Insertions:   insertions,
		Deletions:    deletions,
		IssueKeys:    issueKeys,
	}
	metaJSON, _ := json.Marshal(meta)

	return models.Activity{
		Source:    models.SourceGit,
		SourceID:  repoPath + ":" + sha,
		Type:      models.TypeCommit,
		Title:     subject,
		Content:   body,
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}, nil
}

// runGit executes a git command in the given directory.
var runGit = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s in %s: %s: %w", args[0], dir, stderr.String(), err)
	}
	return stdout.Bytes(), nil
}
