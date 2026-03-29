package git

import (
	"os"
	"path/filepath"
	"testing"
)

func makeRepo(t *testing.T, base, name string) string {
	t.Helper()
	repoDir := filepath.Join(base, name)
	gitDir := filepath.Join(repoDir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return repoDir
}

func TestDiscoverRepos_FindsRepos(t *testing.T) {
	tmp := t.TempDir()
	makeRepo(t, tmp, "project-a")
	makeRepo(t, tmp, "project-b")
	// Non-repo directory.
	os.MkdirAll(filepath.Join(tmp, "not-a-repo"), 0o755)

	repos := DiscoverRepos([]string{tmp})
	if len(repos) != 2 {
		t.Fatalf("got %d repos, want 2: %v", len(repos), repos)
	}
}

func TestDiscoverRepos_NestedRepos(t *testing.T) {
	tmp := t.TempDir()
	// Repo nested 2 levels deep (within maxScanDepth=3).
	makeRepo(t, filepath.Join(tmp, "work", "team"), "deep-repo")

	repos := DiscoverRepos([]string{tmp})
	if len(repos) != 1 {
		t.Fatalf("got %d repos, want 1: %v", len(repos), repos)
	}
}

func TestDiscoverRepos_StopsAtMaxDepth(t *testing.T) {
	tmp := t.TempDir()
	// 4 levels deep — beyond maxScanDepth.
	makeRepo(t, filepath.Join(tmp, "a", "b", "c", "d"), "too-deep")

	repos := DiscoverRepos([]string{tmp})
	if len(repos) != 0 {
		t.Fatalf("got %d repos, want 0 (too deep): %v", len(repos), repos)
	}
}

func TestDiscoverRepos_DoesNotRecurseIntoRepo(t *testing.T) {
	tmp := t.TempDir()
	parent := makeRepo(t, tmp, "parent")
	// Nested repo inside parent — should NOT be discovered.
	makeRepo(t, parent, "child")

	repos := DiscoverRepos([]string{tmp})
	if len(repos) != 1 {
		t.Fatalf("got %d repos, want 1 (should not recurse into .git repos): %v", len(repos), repos)
	}
}

func TestDiscoverRepos_EmptyInput(t *testing.T) {
	repos := DiscoverRepos(nil)
	if len(repos) != 0 {
		t.Fatalf("got %d repos, want 0", len(repos))
	}
}

func TestDiscoverRepos_NonexistentPath(t *testing.T) {
	repos := DiscoverRepos([]string{"/nonexistent/path/xyz"})
	if len(repos) != 0 {
		t.Fatalf("got %d repos, want 0", len(repos))
	}
}

func TestDiscoverRepos_MultipleScanPaths(t *testing.T) {
	tmp := t.TempDir()
	dir1 := filepath.Join(tmp, "area1")
	dir2 := filepath.Join(tmp, "area2")
	makeRepo(t, dir1, "repo1")
	makeRepo(t, dir2, "repo2")

	repos := DiscoverRepos([]string{dir1, dir2})
	if len(repos) != 2 {
		t.Fatalf("got %d repos, want 2: %v", len(repos), repos)
	}
}

func TestDiscoverRepos_DeduplicatesOverlappingPaths(t *testing.T) {
	tmp := t.TempDir()
	makeRepo(t, tmp, "repo")

	// Same directory referenced twice.
	repos := DiscoverRepos([]string{tmp, tmp})
	if len(repos) != 1 {
		t.Fatalf("got %d repos, want 1 (should deduplicate): %v", len(repos), repos)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	got := expandHome("~/Projects")
	want := filepath.Join(home, "Projects")
	if got != want {
		t.Errorf("expandHome(~/Projects) = %q, want %q", got, want)
	}

	// Non-home path should be unchanged.
	got = expandHome("/absolute/path")
	if got != "/absolute/path" {
		t.Errorf("expandHome(/absolute/path) = %q", got)
	}
}
