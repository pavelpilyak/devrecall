package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pavelpilyak/devrecall/internal/update"
)

// handleUpdate reports whether a newer devrecall release is available. It's a
// thin wrapper over update.PassiveCheck — throttled to once per 24h and cached
// on disk (shared with the CLI's passive notice) — so the desktop app can poll
// it cheaply to drive an "update available" badge and the Settings About row.
//
// It always responds 200: a network failure yields update_available:false with
// a non-empty error, never an HTTP error, so a transient GitHub outage doesn't
// surface as a broken app.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	current := s.version
	method, command := detectUpdateMethod()

	resp := map[string]any{
		"current":          current,
		"latest":           current,
		"update_available": false,
		"install_method":   method,
		"upgrade_command":  command,
	}

	rel, err := update.PassiveCheck(s.configDir(), current, "")
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if rel != nil {
		resp["latest"] = rel.Version
		resp["update_available"] = true
		resp["notes"] = rel.Changelog
	}
	writeJSON(w, http.StatusOK, resp)
}

// detectUpdateMethod inspects the running binary's path to decide how the user
// should upgrade, returning a coarse method and the exact command to run:
//
//   - "cask"       → desktop app installed via `brew install --cask`; the binary
//                    lives inside DevRecall.app, so the whole cask is upgraded.
//   - "brew"       → CLI installed via the Homebrew formula (a Cellar symlink).
//   - "standalone" → curl/tarball install; the binary self-updates.
//   - "unknown"    → path couldn't be resolved; fall back to the cask command,
//                    which is the desktop app's overwhelmingly common case.
func detectUpdateMethod() (method, command string) {
	exe, err := os.Executable()
	if err != nil {
		return "unknown", "brew upgrade --cask devrecall"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return classifyExePath(exe)
}

// classifyExePath maps a resolved executable path to an install method and the
// command that upgrades it. Split out from detectUpdateMethod so the path logic
// is testable without touching os.Executable.
func classifyExePath(exe string) (method, command string) {
	switch {
	case strings.Contains(exe, ".app/Contents/"):
		return "cask", "brew upgrade --cask devrecall"
	case strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/homebrew/"):
		return "brew", "brew upgrade devrecall"
	default:
		return "standalone", "devrecall update"
	}
}
