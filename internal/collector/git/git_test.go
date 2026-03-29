package git

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func TestName(t *testing.T) {
	c := New(nil, nil)
	if c.Name() != models.SourceGit {
		t.Fatalf("got %q, want %q", c.Name(), models.SourceGit)
	}
}

func TestParseLog_SingleCommit(t *testing.T) {
	raw := recordSep + "\n" +
		"abc123" + fieldSep + "2026-03-27T14:30:00+02:00" + fieldSep + "Fix auth refresh" + fieldSep + "Pavel" + fieldSep + "pavel@example.com\n" +
		" 3 files changed, 47 insertions(+), 12 deletions(-)\n"

	activities, err := parseLog("backend-api", "/home/user/Projects/backend-api", []byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	act := activities[0]
	if act.Source != models.SourceGit {
		t.Errorf("source = %q, want %q", act.Source, models.SourceGit)
	}
	if act.Type != models.TypeCommit {
		t.Errorf("type = %q, want %q", act.Type, models.TypeCommit)
	}
	if act.Title != "Fix auth refresh" {
		t.Errorf("title = %q, want %q", act.Title, "Fix auth refresh")
	}
	if act.SourceID != "/home/user/Projects/backend-api:abc123" {
		t.Errorf("source_id = %q, want %q", act.SourceID, "/home/user/Projects/backend-api:abc123")
	}

	wantTime, _ := time.Parse(time.RFC3339, "2026-03-27T14:30:00+02:00")
	if !act.Timestamp.Equal(wantTime) {
		t.Errorf("timestamp = %v, want %v", act.Timestamp, wantTime)
	}

	var meta commitMeta
	if err := json.Unmarshal([]byte(act.Metadata), &meta); err != nil {
		t.Fatalf("bad metadata JSON: %v", err)
	}
	if meta.Repo != "backend-api" {
		t.Errorf("meta.Repo = %q, want %q", meta.Repo, "backend-api")
	}
	if meta.SHA != "abc123" {
		t.Errorf("meta.SHA = %q, want %q", meta.SHA, "abc123")
	}
	if meta.FilesChanged != 3 || meta.Insertions != 47 || meta.Deletions != 12 {
		t.Errorf("stats = %d/%d/%d, want 3/47/12", meta.FilesChanged, meta.Insertions, meta.Deletions)
	}
}

func TestParseLog_MultipleCommits(t *testing.T) {
	raw := recordSep + "\n" +
		"aaa" + fieldSep + "2026-03-27T10:00:00Z" + fieldSep + "First commit" + fieldSep + "Pavel" + fieldSep + "p@x.com\n" +
		" 1 file changed, 5 insertions(+)\n" +
		recordSep + "\n" +
		"bbb" + fieldSep + "2026-03-27T11:00:00Z" + fieldSep + "Second commit" + fieldSep + "Pavel" + fieldSep + "p@x.com\n" +
		" 2 files changed, 10 insertions(+), 3 deletions(-)\n"

	activities, err := parseLog("repo", "/repo", []byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(activities) != 2 {
		t.Fatalf("got %d activities, want 2", len(activities))
	}
	if activities[0].Title != "First commit" {
		t.Errorf("first title = %q", activities[0].Title)
	}
	if activities[1].Title != "Second commit" {
		t.Errorf("second title = %q", activities[1].Title)
	}
}

func TestParseLog_NoStatLine(t *testing.T) {
	raw := recordSep + "\n" +
		"ccc" + fieldSep + "2026-03-27T12:00:00Z" + fieldSep + "Empty commit" + fieldSep + "Pavel" + fieldSep + "p@x.com\n"

	activities, err := parseLog("repo", "/repo", []byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	var meta commitMeta
	json.Unmarshal([]byte(activities[0].Metadata), &meta)
	if meta.FilesChanged != 0 || meta.Insertions != 0 || meta.Deletions != 0 {
		t.Errorf("expected zero stats for commit without stat line")
	}
}

func TestParseLog_EmptyOutput(t *testing.T) {
	activities, err := parseLog("repo", "/repo", []byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(activities) != 0 {
		t.Fatalf("got %d activities, want 0", len(activities))
	}
}

func TestParseLog_MalformedEntrySkipped(t *testing.T) {
	raw := recordSep + "\n" +
		"not-enough-fields\n" +
		recordSep + "\n" +
		"aaa" + fieldSep + "2026-03-27T10:00:00Z" + fieldSep + "Good commit" + fieldSep + "Pavel" + fieldSep + "p@x.com\n"

	activities, err := parseLog("repo", "/repo", []byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1 (malformed should be skipped)", len(activities))
	}
	if activities[0].Title != "Good commit" {
		t.Errorf("title = %q", activities[0].Title)
	}
}

func TestParseLog_DeletionsOnly(t *testing.T) {
	raw := recordSep + "\n" +
		"ddd" + fieldSep + "2026-03-27T12:00:00Z" + fieldSep + "Delete stuff" + fieldSep + "Pavel" + fieldSep + "p@x.com\n" +
		" 1 file changed, 5 deletions(-)\n"

	activities, err := parseLog("repo", "/repo", []byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var meta commitMeta
	json.Unmarshal([]byte(activities[0].Metadata), &meta)
	if meta.FilesChanged != 1 || meta.Insertions != 0 || meta.Deletions != 5 {
		t.Errorf("stats = %d/%d/%d, want 1/0/5", meta.FilesChanged, meta.Insertions, meta.Deletions)
	}
}

func TestCollect_UsesRunGit(t *testing.T) {
	// Stub runGit to return canned output instead of shelling out.
	origRunGit := runGit
	t.Cleanup(func() { runGit = origRunGit })

	runGit = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		out := recordSep + "\n" +
			"abc" + fieldSep + "2026-03-27T10:00:00Z" + fieldSep + "Test commit" + fieldSep + "Pavel" + fieldSep + "pavel@test.com\n" +
			" 1 file changed, 1 insertion(+)\n"
		return []byte(out), nil
	}

	c := New([]string{"/fake/repo"}, []string{"pavel@test.com"})
	activities, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}
	if activities[0].Title != "Test commit" {
		t.Errorf("title = %q", activities[0].Title)
	}
}

func TestCollect_SkipsBrokenRepos(t *testing.T) {
	origRunGit := runGit
	t.Cleanup(func() { runGit = origRunGit })

	callCount := 0
	runGit = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		callCount++
		if dir == "/bad/repo" {
			return nil, fmt.Errorf("not a git repository")
		}
		out := recordSep + "\n" +
			"abc" + fieldSep + "2026-03-27T10:00:00Z" + fieldSep + "Good" + fieldSep + "P" + fieldSep + "p@x.com\n"
		return []byte(out), nil
	}

	c := New([]string{"/bad/repo", "/good/repo"}, []string{"p@x.com"})
	activities, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}
	if callCount != 2 {
		t.Errorf("expected 2 git calls, got %d", callCount)
	}
}
