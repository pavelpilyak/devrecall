package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// mockTokenStore is a no-op token store for tests.
type mockTokenStore struct{}

func (m *mockTokenStore) Save(vendor, key string, token any) error { return nil }
func (m *mockTokenStore) Load(vendor, key string, dst any) error {
	return fmt.Errorf("no tokens in test")
}
func (m *mockTokenStore) Delete(vendor, key string) error { return nil }

func setupTestServer(t *testing.T) (*Server, *storage.DB) {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{
		Git: config.GitConfig{Enabled: true},
	}

	srv := NewServer(0, db, cfg, &mockTokenStore{})
	srv.dataDir = t.TempDir()
	return srv, db
}

func newRequest(method, path string, body any) *http.Request {
	if body != nil {
		data, _ := json.Marshal(body)
		return httptest.NewRequest(method, path, bytes.NewReader(data))
	}
	return httptest.NewRequest(method, path, nil)
}

func TestHandleStatus(t *testing.T) {
	srv, db := setupTestServer(t)

	// Insert some activities.
	db.InsertActivities([]models.Activity{
		{Source: models.SourceGit, SourceID: "abc123", Type: models.TypeCommit, Title: "Fix bug", Timestamp: time.Now()},
		{Source: models.SourceGit, SourceID: "def456", Type: models.TypeCommit, Title: "Add feature", Timestamp: time.Now()},
	})

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("GET", "/api/status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp["status"])
	}

	sources, ok := resp["sources"].([]any)
	if !ok || len(sources) == 0 {
		t.Fatal("expected non-empty sources array")
	}

	// Check git source has count 2.
	gitSrc := sources[0].(map[string]any)
	if gitSrc["name"] != "git" {
		t.Errorf("expected first source to be git, got %v", gitSrc["name"])
	}
	if gitSrc["count"] != float64(2) {
		t.Errorf("expected git count 2, got %v", gitSrc["count"])
	}
	if gitSrc["enabled"] != true {
		t.Errorf("expected git enabled true")
	}
	// No error recorded yet — last_error should be omitted.
	if _, present := gitSrc["last_error"]; present {
		t.Errorf("expected no last_error when sync succeeded, got %v", gitSrc["last_error"])
	}
}

func TestHandleLLMHealth_NotConfigured(t *testing.T) {
	srv, _ := setupTestServer(t) // default config has no LLM provider
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("GET", "/api/llm/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (health is always 200), got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != false {
		t.Errorf("expected ok=false with no provider, got %v", resp["ok"])
	}
	if resp["error"] == "" {
		t.Errorf("expected a reason when not configured")
	}
}

func TestHandleLLMHealth_OllamaUnreachable(t *testing.T) {
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Point Ollama at a closed port so the probe fails fast and deterministically.
	cfg := &config.Config{LLM: config.LLMConfig{Provider: "ollama", BaseURL: "http://127.0.0.1:1"}}
	srv := NewServer(0, db, cfg, &mockTokenStore{})
	srv.dataDir = t.TempDir()

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("GET", "/api/llm/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != false {
		t.Errorf("expected ok=false when Ollama is unreachable, got %v", resp["ok"])
	}
	if resp["provider"] != "ollama" {
		t.Errorf("expected provider ollama echoed back, got %v", resp["provider"])
	}
}

func TestHandleStatus_SurfacesSyncError(t *testing.T) {
	srv, db := setupTestServer(t)
	if err := db.SetSyncError("jira", "401 unauthorized"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("GET", "/api/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	var jira map[string]any
	for _, s := range resp["sources"].([]any) {
		m := s.(map[string]any)
		if m["name"] == "jira" {
			jira = m
			break
		}
	}
	if jira == nil {
		t.Fatal("jira source not present in status response")
	}
	if jira["last_error"] != "401 unauthorized" {
		t.Errorf("expected jira last_error to surface, got %v", jira["last_error"])
	}
}

func TestHandleActivities(t *testing.T) {
	srv, db := setupTestServer(t)

	now := time.Now().UTC().Truncate(time.Second)
	db.InsertActivities([]models.Activity{
		{Source: models.SourceGit, SourceID: "a1", Type: models.TypeCommit, Title: "Commit 1", Timestamp: now},
		{Source: models.SourceSlack, SourceID: "s1", Type: models.TypeMessage, Title: "Message 1", Timestamp: now},
		{Source: models.SourceGit, SourceID: "a2", Type: models.TypeCommit, Title: "Commit 2", Timestamp: now.Add(-time.Hour)},
	})

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	t.Run("all activities", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/activities", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["count"] != float64(3) {
			t.Errorf("expected 3 activities, got %v", resp["count"])
		}
	})

	t.Run("filter by source", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/activities?source=git", nil))

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["count"] != float64(2) {
			t.Errorf("expected 2 git activities, got %v", resp["count"])
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/activities?type=message", nil))

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["count"] != float64(1) {
			t.Errorf("expected 1 message activity, got %v", resp["count"])
		}
	})

	t.Run("custom limit", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/activities?limit=1", nil))

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["count"] != float64(1) {
			t.Errorf("expected 1 activity with limit=1, got %v", resp["count"])
		}
	})

	t.Run("invalid limit", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/activities?limit=abc", nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandleSearch(t *testing.T) {
	srv, db := setupTestServer(t)

	db.InsertActivities([]models.Activity{
		{Source: models.SourceGit, SourceID: "c1", Type: models.TypeCommit, Title: "Fix authentication bug", Content: "Fixed token refresh logic", Timestamp: time.Now()},
		{Source: models.SourceGit, SourceID: "c2", Type: models.TypeCommit, Title: "Add payment feature", Content: "Stripe integration", Timestamp: time.Now()},
	})

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	t.Run("search with results", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/search?q=authentication", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["query"] != "authentication" {
			t.Errorf("expected query 'authentication', got %v", resp["query"])
		}
		count := resp["count"].(float64)
		if count < 1 {
			t.Errorf("expected at least 1 result, got %v", count)
		}
	})

	t.Run("search with no results", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/search?q=zzzznonexistent", nil))

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["count"] != float64(0) {
			t.Errorf("expected 0 results, got %v", resp["count"])
		}
	})

	t.Run("missing query", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/search", nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandleStandup(t *testing.T) {
	srv, db := setupTestServer(t)

	// Insert activities for yesterday.
	yesterday := time.Now().AddDate(0, 0, -1)
	dayStart := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 10, 0, 0, 0, time.UTC)
	db.InsertActivities([]models.Activity{
		{Source: models.SourceGit, SourceID: "s1", Type: models.TypeCommit, Title: "Fix login bug", Timestamp: dayStart},
	})

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	t.Run("default date (yesterday)", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/standup", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["date"] != yesterday.Format("2006-01-02") {
			t.Errorf("expected date %s, got %v", yesterday.Format("2006-01-02"), resp["date"])
		}
		if resp["report"] == nil || resp["report"] == "" {
			t.Error("expected non-empty report")
		}
	})

	t.Run("explicit date", func(t *testing.T) {
		dateStr := yesterday.Format("2006-01-02")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/standup?date="+dateStr, nil))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid date", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/standup?date=not-a-date", nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandleWeek(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	t.Run("current week", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/week", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["week_start"] == nil {
			t.Error("expected week_start")
		}
		if resp["report"] == nil {
			t.Error("expected report")
		}
	})

	t.Run("invalid weeks_back", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("GET", "/api/week?weeks_back=abc", nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandleChat_NoLLM(t *testing.T) {
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Use "anthropic" provider without a token — FromConfig will return an error.
	cfg := &config.Config{
		LLM: config.LLMConfig{Provider: "anthropic"},
	}
	srv := NewServer(0, db, cfg, &mockTokenStore{})
	srv.dataDir = t.TempDir()

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	body := map[string]string{"message": "What did I do yesterday?"}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/chat", body))

	// Without LLM configured, should return 503.
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without LLM, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleChat_InvalidBody(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	t.Run("empty message", func(t *testing.T) {
		body := map[string]string{"message": ""}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("POST", "/api/chat", body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/chat", bytes.NewReader([]byte("not json")))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandleSync(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/sync", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "sync complete") {
		t.Errorf("expected 'sync complete' message, got %q", msg)
	}
}

func TestHandleActivities_DateFilter(t *testing.T) {
	srv, db := setupTestServer(t)

	march1 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	march15 := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	db.InsertActivities([]models.Activity{
		{Source: models.SourceGit, SourceID: "d1", Type: models.TypeCommit, Title: "March 1 commit", Timestamp: march1},
		{Source: models.SourceGit, SourceID: "d2", Type: models.TypeCommit, Title: "March 15 commit", Timestamp: march15},
	})

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("GET", "/api/activities?after=2026-03-10&before=2026-03-20", nil))

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["count"] != float64(1) {
		t.Errorf("expected 1 activity in date range, got %v", resp["count"])
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	// POST to a GET-only endpoint should fail.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, newRequest("POST", "/api/status", nil))
	if w.Code == http.StatusOK {
		t.Error("expected non-200 for POST to GET-only endpoint")
	}
}

func TestNewServer_DefaultPort(t *testing.T) {
	srv := NewServer(0, nil, &config.Config{}, &mockTokenStore{})
	if srv.Port() != 3725 {
		t.Errorf("expected default port 3725, got %d", srv.Port())
	}
}

func TestNewServer_CustomPort(t *testing.T) {
	srv := NewServer(8080, nil, &config.Config{}, &mockTokenStore{})
	if srv.Port() != 8080 {
		t.Errorf("expected port 8080, got %d", srv.Port())
	}
}

func TestHandleLog(t *testing.T) {
	srv, db := setupTestServer(t)
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	t.Run("creates manual activity", func(t *testing.T) {
		body := map[string]any{
			"text":   "Talked to mobile team about API contract",
			"tags":   []string{"meeting"},
			"people": []string{"anna@example.com"},
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("POST", "/api/log", body))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if _, ok := resp["id"]; !ok {
			t.Error("response missing id")
		}
		if resp["title"] != "Talked to mobile team about API contract" {
			t.Errorf("title = %v", resp["title"])
		}

		// Verify it was stored as a manual activity.
		acts, err := db.ListActivities(storage.ActivityFilter{Source: models.SourceManual, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(acts) != 1 {
			t.Fatalf("expected 1 manual activity, got %d", len(acts))
		}
		if acts[0].Type != models.TypeNote {
			t.Errorf("type = %q, want note", acts[0].Type)
		}
		if !strings.Contains(acts[0].Metadata, "anna@example.com") {
			t.Errorf("metadata missing person: %q", acts[0].Metadata)
		}
	})

	t.Run("custom timestamp", func(t *testing.T) {
		body := map[string]any{
			"text": "Past event",
			"at":   "2026-04-01 09:30",
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("POST", "/api/log", body))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		ts, _ := resp["timestamp"].(string)
		if !strings.HasPrefix(ts, "2026-04-01T09:30") {
			t.Errorf("timestamp = %q, want 2026-04-01T09:30...", ts)
		}
	})

	t.Run("missing text", func(t *testing.T) {
		body := map[string]any{"text": "   "}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("POST", "/api/log", body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("invalid timestamp", func(t *testing.T) {
		body := map[string]any{"text": "ok", "at": "garbage"}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newRequest("POST", "/api/log", body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/log", bytes.NewReader([]byte("not json")))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
}

// TestWatchConfig_ReloadsOnFileChange verifies that an external edit to
// config.json (e.g., manual `vim ~/.devrecall/config.json` while the daemon
// is running) is picked up live, without restarting the server.
func TestWatchConfig_ReloadsOnFileChange(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir := filepath.Join(tmpHome, ".devrecall")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.json")

	initial := `{"llm":{"provider":"ollama","model":"gemma4"}}`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(0, nil, cfg, &mockTokenStore{})
	if got := srv.Cfg().LLM.Provider; got != "ollama" {
		t.Fatalf("baseline provider = %q, want ollama", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.WatchConfig(ctx)

	// Give fsnotify a moment to register the dir watch before we mutate.
	time.Sleep(100 * time.Millisecond)

	updated := `{"llm":{"provider":"openai","model":"gpt-4o-mini"}}`
	if err := os.WriteFile(cfgPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Cfg().LLM.Provider == "openai" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("config did not reload within 2s; provider still = %q", srv.Cfg().LLM.Provider)
}

func TestCORSMiddlewareAlwaysReturnsWildcard(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		origin string
	}{
		{"no origin", ""},
		{"tauri origin", "tauri://localhost"},
		{"vite dev origin", "http://localhost:5173"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			got := w.Header().Get("Access-Control-Allow-Origin")
			if got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q (origin=%q)", got, "*", tc.origin)
			}
		})
	}
}

