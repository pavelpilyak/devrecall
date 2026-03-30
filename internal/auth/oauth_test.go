package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSlackOAuth_SuccessfulFlow(t *testing.T) {
	token := SlackToken{
		AccessToken: "xoxp-test-token-123",
		UserID:      "U123ABC",
		TeamID:      "T456DEF",
		TeamName:    "Test Workspace",
		Scope:       "channels:history,channels:read",
	}

	// Mock relay that returns the token on first poll.
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/poll" {
			http.NotFound(w, r)
			return
		}
		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(token)
	}))
	defer relay.Close()

	var capturedURL string
	cfg := SlackOAuthConfig{
		ClientID:     "test-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser: func(url string) error {
			capturedURL = url
			return nil
		},
	}

	result, err := SlackOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("SlackOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if result.TeamID != token.TeamID {
		t.Errorf("TeamID = %q, want %q", result.TeamID, token.TeamID)
	}

	// Verify the browser was opened with correct URL.
	if capturedURL == "" {
		t.Error("browser was not opened")
	}
}

func TestSlackOAuth_PollsUntilReady(t *testing.T) {
	token := SlackToken{
		AccessToken: "xoxp-delayed-token",
		UserID:      "U999",
		TeamID:      "T999",
		TeamName:    "Delayed Workspace",
		Scope:       "channels:history",
	}

	var pollCount atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/poll" {
			http.NotFound(w, r)
			return
		}
		count := pollCount.Add(1)
		// Return 404 for first 2 polls, then return the token.
		if count <= 2 {
			http.Error(w, `{"error":"pending"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(token)
	}))
	defer relay.Close()

	cfg := SlackOAuthConfig{
		ClientID:     "test-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	result, err := SlackOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("SlackOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if pollCount.Load() < 3 {
		t.Errorf("expected at least 3 polls, got %d", pollCount.Load())
	}
}

func TestSlackOAuth_ContextCanceled(t *testing.T) {
	// Relay that always returns 404 (token never ready).
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"pending"}`, http.StatusNotFound)
	}))
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	cfg := SlackOAuthConfig{
		ClientID:     "test-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := SlackOAuth(ctx, cfg)
	if err == nil {
		t.Error("expected error when context is canceled")
	}
}

func TestSlackOAuth_BrowserOpenFails(t *testing.T) {
	cfg := SlackOAuthConfig{
		ClientID:     "test-client-id",
		RelayBaseURL: "http://localhost:0",
		HTTPClient:   http.DefaultClient,
		OpenBrowser: func(url string) error {
			return fmt.Errorf("no browser available")
		},
	}

	_, err := SlackOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error when browser fails to open")
	}
}

func TestSlackOAuth_RelayError(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer relay.Close()

	cfg := SlackOAuthConfig{
		ClientID:     "test-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := SlackOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error on relay 500")
	}
}

func TestBuildSlackAuthURL(t *testing.T) {
	cfg := SlackOAuthConfig{
		ClientID:     "my-client-id",
		RelayBaseURL: "https://relay.devrecall.dev",
	}

	url := buildSlackAuthURL(cfg, "session-abc-123")

	if url == "" {
		t.Fatal("URL is empty")
	}

	// Check key parameters are present.
	tests := []struct {
		name     string
		contains string
	}{
		{"client_id", "client_id=my-client-id"},
		{"redirect_uri", "redirect_uri=https"},
		{"state", "state=session-abc-123"},
		{"user_scope", "user_scope=channels"},
	}
	for _, tt := range tests {
		if !containsStr(url, tt.contains) {
			t.Errorf("URL missing %s: %s", tt.name, url)
		}
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
