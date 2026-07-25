package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClassifyExePath(t *testing.T) {
	tests := []struct {
		name       string
		exe        string
		wantMethod string
		wantCmd    string
	}{
		{
			name:       "cask app bundle",
			exe:        "/Applications/DevRecall.app/Contents/MacOS/devrecall",
			wantMethod: "cask",
			wantCmd:    "brew upgrade --cask devrecall",
		},
		{
			name:       "homebrew formula cellar",
			exe:        "/opt/homebrew/Cellar/devrecall/0.1.23/bin/devrecall",
			wantMethod: "brew",
			wantCmd:    "brew upgrade devrecall",
		},
		{
			name:       "standalone binary",
			exe:        "/usr/local/bin/devrecall",
			wantMethod: "standalone",
			wantCmd:    "devrecall update",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method, cmd := classifyExePath(tt.exe)
			if method != tt.wantMethod || cmd != tt.wantCmd {
				t.Errorf("classifyExePath(%q) = (%q, %q), want (%q, %q)",
					tt.exe, method, cmd, tt.wantMethod, tt.wantCmd)
			}
		})
	}
}

// seedVersionCache writes a fresh version_check.json into dir so PassiveCheck
// returns from cache without a network call. Mirrors update.checkCache's JSON.
func seedVersionCache(t *testing.T, dir, latest string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"checked_at":     time.Now().UTC(),
		"latest_version": latest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "version_check.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHandleUpdate_Available(t *testing.T) {
	dir := t.TempDir()
	seedVersionCache(t, dir, "v0.1.99")

	s := &Server{dataDir: dir, version: "v0.1.20"}
	rr := httptest.NewRecorder()
	s.handleUpdate(rr, httptest.NewRequest(http.MethodGet, "/api/update", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp struct {
		Current         string `json:"current"`
		Latest          string `json:"latest"`
		UpdateAvailable bool   `json:"update_available"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.UpdateAvailable {
		t.Error("update_available = false, want true")
	}
	if resp.Latest != "v0.1.99" || resp.Current != "v0.1.20" {
		t.Errorf("current/latest = %q/%q, want v0.1.20/v0.1.99", resp.Current, resp.Latest)
	}
}

func TestHandleUpdate_UpToDate(t *testing.T) {
	dir := t.TempDir()
	seedVersionCache(t, dir, "v0.1.20")

	s := &Server{dataDir: dir, version: "v0.1.20"}
	rr := httptest.NewRecorder()
	s.handleUpdate(rr, httptest.NewRequest(http.MethodGet, "/api/update", nil))

	var resp struct {
		UpdateAvailable bool `json:"update_available"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.UpdateAvailable {
		t.Error("update_available = true for equal versions, want false")
	}
}
