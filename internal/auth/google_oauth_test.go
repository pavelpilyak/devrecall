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

func TestGoogleOAuth_SuccessfulFlow(t *testing.T) {
	token := GoogleToken{
		AccessToken:  "ya29.test-access-token",
		RefreshToken: "1//test-refresh-token",
		ExpiresIn:    3600,
		Email:        "test@example.com",
		Scope:        "https://www.googleapis.com/auth/calendar.readonly",
	}

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/session/start" {
			w.WriteHeader(http.StatusOK)
			return
		}
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
	cfg := GoogleOAuthConfig{
		ClientID:     "test-google-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser: func(url string) error {
			capturedURL = url
			return nil
		},
	}

	result, err := GoogleOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("GoogleOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if result.RefreshToken != token.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", result.RefreshToken, token.RefreshToken)
	}
	if result.Email != token.Email {
		t.Errorf("Email = %q, want %q", result.Email, token.Email)
	}
	if result.ExpiresIn != token.ExpiresIn {
		t.Errorf("ExpiresIn = %d, want %d", result.ExpiresIn, token.ExpiresIn)
	}

	if capturedURL == "" {
		t.Error("browser was not opened")
	}
}

func TestGoogleOAuth_PollsUntilReady(t *testing.T) {
	token := GoogleToken{
		AccessToken:  "ya29.delayed-token",
		RefreshToken: "1//delayed-refresh",
		ExpiresIn:    3600,
		Email:        "delayed@example.com",
		Scope:        "calendar.readonly",
	}

	var pollCount atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/session/start" {
			w.WriteHeader(http.StatusOK)
			return
		}
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

	cfg := GoogleOAuthConfig{
		ClientID:     "test-google-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	result, err := GoogleOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("GoogleOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if pollCount.Load() < 3 {
		t.Errorf("expected at least 3 polls, got %d", pollCount.Load())
	}
}

func TestGoogleOAuth_ContextCanceled(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"pending"}`, http.StatusNotFound)
	}))
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := GoogleOAuthConfig{
		ClientID:     "test-google-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := GoogleOAuth(ctx, cfg)
	if err == nil {
		t.Error("expected error when context is canceled")
	}
}

func TestGoogleOAuth_BrowserOpenFails(t *testing.T) {
	cfg := GoogleOAuthConfig{
		ClientID:     "test-google-client-id",
		RelayBaseURL: "http://localhost:0",
		HTTPClient:   http.DefaultClient,
		OpenBrowser: func(url string) error {
			return fmt.Errorf("no browser available")
		},
	}

	_, err := GoogleOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error when browser fails to open")
	}
}

func TestGoogleOAuth_RelayError(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer relay.Close()

	cfg := GoogleOAuthConfig{
		ClientID:     "test-google-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := GoogleOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error on relay 500")
	}
}

func TestBuildGoogleAuthURL(t *testing.T) {
	cfg := GoogleOAuthConfig{
		ClientID:     "my-google-client-id",
		RelayBaseURL: "https://relay.devrecall.dev",
	}

	url := buildGoogleAuthURL(cfg, "session-google-123")

	if url == "" {
		t.Fatal("URL is empty")
	}

	tests := []struct {
		name     string
		contains string
	}{
		{"client_id", "client_id=my-google-client-id"},
		{"redirect_uri", "redirect_uri=https"},
		{"state", "state=session-google-123"},
		{"scope", "scope=https"},
		{"access_type", "access_type=offline"},
		{"response_type", "response_type=code"},
		{"prompt", "prompt=consent"},
	}
	for _, tt := range tests {
		if !strings.Contains(url, tt.contains) {
			t.Errorf("URL missing %s: %s", tt.name, url)
		}
	}
}
