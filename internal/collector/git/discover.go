package git

import (
	"os"
	"path/filepath"
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
