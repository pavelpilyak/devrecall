package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGitHubOAuth_SuccessfulFlow(t *testing.T) {
	token := GitHubToken{
		AccessToken: "gho_test-access-token",
		TokenType:   "bearer",
		Scope:       "repo,read:user",
		Username:    "testuser",
	}

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
	cfg := GitHubOAuthConfig{
		ClientID:     "test-github-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser: func(url string) error {
			capturedURL = url
			return nil
		},
	}

	result, err := GitHubOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("GitHubOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if result.Username != token.Username {
		t.Errorf("Username = %q, want %q", result.Username, token.Username)
	}
	if result.Scope != token.Scope {
		t.Errorf("Scope = %q, want %q", result.Scope, token.Scope)
	}
	if capturedURL == "" {
		t.Error("browser was not opened")
	}
}

func TestGitHubOAuth_PollsUntilReady(t *testing.T) {
	token := GitHubToken{
		AccessToken: "gho_delayed-token",
		TokenType:   "bearer",
		Scope:       "repo",
		Username:    "delayeduser",
	}

	var pollCount atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/poll" {
			http.NotFound(w, r)
			return
		}
		count := pollCount.Add(1)
		if count <= 2 {
			http.Error(w, `{"error":"pending"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(token)
	}))
	defer relay.Close()

	cfg := GitHubOAuthConfig{
		ClientID:     "test-github-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	result, err := GitHubOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("GitHubOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if pollCount.Load() < 3 {
		t.Errorf("expected at least 3 polls, got %d", pollCount.Load())
	}
}

func TestGitHubOAuth_ContextCanceled(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"pending"}`, http.StatusNotFound)
	}))
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := GitHubOAuthConfig{
		ClientID:     "test-github-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := GitHubOAuth(ctx, cfg)
	if err == nil {
		t.Error("expected error when context is canceled")
	}
}

func TestGitHubOAuth_BrowserOpenFails(t *testing.T) {
	cfg := GitHubOAuthConfig{
		ClientID:     "test-github-client-id",
		RelayBaseURL: "http://localhost:0",
		HTTPClient:   http.DefaultClient,
		OpenBrowser: func(url string) error {
			return fmt.Errorf("no browser available")
		},
	}

	_, err := GitHubOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error when browser fails to open")
	}
}

func TestGitHubOAuth_RelayError(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer relay.Close()

	cfg := GitHubOAuthConfig{
		ClientID:     "test-github-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := GitHubOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error on relay 500")
	}
}

func TestBuildGitHubAuthURL(t *testing.T) {
	cfg := GitHubOAuthConfig{
		ClientID:     "my-github-client-id",
		RelayBaseURL: "https://relay.devrecall.dev",
	}

	url := buildGitHubAuthURL(cfg, "session-github-123")

	if url == "" {
		t.Fatal("URL is empty")
	}

	tests := []struct {
		name     string
		contains string
	}{
		{"client_id", "client_id=my-github-client-id"},
		{"redirect_uri", "redirect_uri=https"},
		{"github callback", "oauth%2Fgithub%2Fcallback"},
		{"state", "state=session-github-123"},
		{"scope", "scope=repo"},
	}
	for _, tt := range tests {
		if !strings.Contains(url, tt.contains) {
			t.Errorf("URL missing %s: %s", tt.name, url)
		}
	}
}

func TestValidateGitHubPAT_Success(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			http.NotFound(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer ghp_validtoken123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo, read:user")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"login": "octocat"})
	}))
	defer api.Close()

	cfg := GitHubPATConfig{
		HTTPClient: api.Client(),
		APIURL:     api.URL,
	}

	token, err := ValidateGitHubPAT(context.Background(), "ghp_validtoken123", cfg)
	if err != nil {
		t.Fatalf("ValidateGitHubPAT: %v", err)
	}
	if token.Username != "octocat" {
		t.Errorf("Username = %q, want %q", token.Username, "octocat")
	}
	if token.AccessToken != "ghp_validtoken123" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "ghp_validtoken123")
	}
	if token.Scope != "repo, read:user" {
		t.Errorf("Scope = %q, want %q", token.Scope, "repo, read:user")
	}
}

func TestValidateGitHubPAT_InvalidToken(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer api.Close()

	cfg := GitHubPATConfig{
		HTTPClient: api.Client(),
		APIURL:     api.URL,
	}

	_, err := ValidateGitHubPAT(context.Background(), "ghp_badtoken", cfg)
	if err == nil {
		t.Error("expected error for invalid token")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}
