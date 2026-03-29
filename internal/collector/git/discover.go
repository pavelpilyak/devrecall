package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// maxScanDepth limits how deep we walk when looking for .git directories.
const maxScanDepth = 3

// DiscoverRepos walks the given directories (up to maxScanDepth levels deep)
// and returns paths to directories that contain a .git subdirectory.
func DiscoverRepos(scanPaths []string) []string {
	seen := make(map[string]bool)
	var repos []string

	for _, root := range scanPaths {
		root = expandHome(root)
		walkForRepos(root, 0, seen, &repos)
	}
	return repos
}

func walkForRepos(dir string, depth int, seen map[string]bool, repos *[]string) {
	if depth > maxScanDepth {
		return
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	if seen[abs] {
		return
	}
	seen[abs] = true

	gitDir := filepath.Join(abs, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		*repos = append(*repos, abs)
		return // don't recurse into git repos looking for nested repos
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		walkForRepos(filepath.Join(abs, e.Name()), depth+1, seen, repos)
	}
}

// DetectEmails runs `git config user.email` in each repo and returns
// the unique emails found. Repos that fail are silently skipped.
func DetectEmails(repos []string) []string {
	seen := make(map[string]bool)
	var emails []string

	for _, repo := range repos {
		out, err := runGit(context.Background(), repo, "config", "user.email")
		if err != nil {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(string(out)))
		if email != "" && !seen[email] {
			seen[email] = true
			emails = append(emails, email)
		}
	}
	return emails
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
