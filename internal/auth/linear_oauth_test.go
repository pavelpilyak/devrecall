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

func TestLinearOAuth_SuccessfulFlow(t *testing.T) {
	token := LinearToken{
		AccessToken: "lin_test_token",
		TokenType:   "Bearer",
		Scope:       "read",
		UserID:      "user-abc",
		UserName:    "Dev User",
		Email:       "dev@example.com",
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
	cfg := LinearOAuthConfig{
		ClientID:     "test-linear-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser: func(url string) error {
			capturedURL = url
			return nil
		},
	}

	result, err := LinearOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("LinearOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if result.Email != token.Email {
		t.Errorf("Email = %q, want %q", result.Email, token.Email)
	}
	if result.UserName != token.UserName {
		t.Errorf("UserName = %q, want %q", result.UserName, token.UserName)
	}
	if capturedURL == "" {
		t.Error("browser was not opened")
	}
}

func TestLinearOAuth_PollsUntilReady(t *testing.T) {
	token := LinearToken{
		AccessToken: "lin_delayed_token",
		Scope:       "read",
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

	cfg := LinearOAuthConfig{
		ClientID:     "test-linear-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	result, err := LinearOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("LinearOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if pollCount.Load() < 3 {
		t.Errorf("expected at least 3 polls, got %d", pollCount.Load())
	}
}

func TestLinearOAuth_ContextCanceled(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"pending"}`, http.StatusNotFound)
	}))
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := LinearOAuthConfig{
		ClientID:     "test-linear-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := LinearOAuth(ctx, cfg)
	if err == nil {
		t.Error("expected error when context is canceled")
	}
}

func TestLinearOAuth_BrowserOpenFails(t *testing.T) {
	cfg := LinearOAuthConfig{
		ClientID:     "test-linear-client-id",
		RelayBaseURL: "http://localhost:0",
		HTTPClient:   http.DefaultClient,
		OpenBrowser: func(url string) error {
			return fmt.Errorf("no browser available")
		},
	}

	_, err := LinearOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error when browser fails to open")
	}
}

func TestLinearOAuth_RelayError(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer relay.Close()

	cfg := LinearOAuthConfig{
		ClientID:     "test-linear-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := LinearOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error on relay 500")
	}
}

func TestBuildLinearAuthURL(t *testing.T) {
	cfg := LinearOAuthConfig{
		ClientID:     "my-linear-client-id",
		RelayBaseURL: "https://relay.devrecall.dev",
	}

	url := buildLinearAuthURL(cfg, "session-linear-123")
	if url == "" {
		t.Fatal("URL is empty")
	}

	tests := []struct {
		name     string
		contains string
	}{
		{"client_id", "client_id=my-linear-client-id"},
		{"redirect_uri", "redirect_uri=https"},
		{"state", "state=session-linear-123"},
		{"scope", "scope=read"},
		{"response_type", "response_type=code"},
		{"prompt", "prompt=consent"},
	}
	for _, tt := range tests {
		if !strings.Contains(url, tt.contains) {
			t.Errorf("URL missing %s: %s", tt.name, url)
		}
	}
}

func TestValidateLinearAPIKey_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_test123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"viewer": map[string]string{
					"id":    "user-xyz",
					"name":  "Test User",
					"email": "test@example.com",
				},
			},
		})
	}))
	defer server.Close()

	cfg := LinearAPIKeyConfig{
		HTTPClient: server.Client(),
		GraphQLURL: server.URL,
	}
	token, err := ValidateLinearAPIKey(context.Background(), "lin_api_test123", cfg)
	if err != nil {
		t.Fatalf("ValidateLinearAPIKey: %v", err)
	}

	if token.AccessToken != "lin_api_test123" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "lin_api_test123")
	}
	if token.UserID != "user-xyz" {
		t.Errorf("UserID = %q, want %q", token.UserID, "user-xyz")
	}
	if token.Email != "test@example.com" {
		t.Errorf("Email = %q, want %q", token.Email, "test@example.com")
	}
}

func TestValidateLinearAPIKey_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := LinearAPIKeyConfig{
		HTTPClient: server.Client(),
		GraphQLURL: server.URL,
	}
	_, err := ValidateLinearAPIKey(context.Background(), "bad-key", cfg)
	if err == nil {
		t.Error("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}

func TestValidateLinearAPIKey_GraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]string{
				{"message": "Authentication required"},
			},
		})
	}))
	defer server.Close()

	cfg := LinearAPIKeyConfig{
		HTTPClient: server.Client(),
		GraphQLURL: server.URL,
	}
	_, err := ValidateLinearAPIKey(context.Background(), "bad-key", cfg)
	if err == nil {
		t.Error("expected error on GraphQL error")
	}
	if !strings.Contains(err.Error(), "Authentication required") {
		t.Errorf("error should mention GraphQL error, got: %v", err)
	}
}
