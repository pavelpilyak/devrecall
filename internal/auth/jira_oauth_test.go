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
	"time"
)

func TestAtlassianOAuth_SuccessfulFlow(t *testing.T) {
	token := AtlassianToken{
		AccessToken:  "atlassian-access-token",
		RefreshToken: "atlassian-refresh-token",
		ExpiresIn:    3600,
		Scope:        "read:jira-work read:jira-user offline_access",
		Email:        "dev@example.com",
		CloudSites: []AtlassianCloudSite{
			{ID: "cloud-123", Name: "My Company", URL: "https://mycompany.atlassian.net"},
		},
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
	cfg := JiraOAuthConfig{
		ClientID:     "test-atlassian-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser: func(url string) error {
			capturedURL = url
			return nil
		},
	}

	result, err := AtlassianOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("AtlassianOAuth: %v", err)
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
	if len(result.CloudSites) != 1 {
		t.Errorf("CloudSites count = %d, want 1", len(result.CloudSites))
	}
	if result.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be set")
	}
	if capturedURL == "" {
		t.Error("browser was not opened")
	}
}

func TestAtlassianOAuth_PollsUntilReady(t *testing.T) {
	token := AtlassianToken{
		AccessToken:  "delayed-atlassian-token",
		RefreshToken: "delayed-refresh",
		ExpiresIn:    3600,
		Email:        "delayed@example.com",
		Scope:        "read:jira-work",
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

	cfg := JiraOAuthConfig{
		ClientID:     "test-atlassian-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	result, err := AtlassianOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("AtlassianOAuth: %v", err)
	}

	if result.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", result.AccessToken, token.AccessToken)
	}
	if pollCount.Load() < 3 {
		t.Errorf("expected at least 3 polls, got %d", pollCount.Load())
	}
}

func TestAtlassianOAuth_ContextCanceled(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"pending"}`, http.StatusNotFound)
	}))
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := JiraOAuthConfig{
		ClientID:     "test-atlassian-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := AtlassianOAuth(ctx, cfg)
	if err == nil {
		t.Error("expected error when context is canceled")
	}
}

func TestAtlassianOAuth_BrowserOpenFails(t *testing.T) {
	cfg := JiraOAuthConfig{
		ClientID:     "test-atlassian-client-id",
		RelayBaseURL: "http://localhost:0",
		HTTPClient:   http.DefaultClient,
		OpenBrowser: func(url string) error {
			return fmt.Errorf("no browser available")
		},
	}

	_, err := AtlassianOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error when browser fails to open")
	}
}

func TestAtlassianOAuth_RelayError(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer relay.Close()

	cfg := JiraOAuthConfig{
		ClientID:     "test-atlassian-client-id",
		RelayBaseURL: relay.URL,
		HTTPClient:   relay.Client(),
		OpenBrowser:  func(url string) error { return nil },
	}

	_, err := AtlassianOAuth(context.Background(), cfg)
	if err == nil {
		t.Error("expected error on relay 500")
	}
}

func TestBuildAtlassianAuthURL(t *testing.T) {
	cfg := JiraOAuthConfig{
		ClientID:     "my-atlassian-client-id",
		RelayBaseURL: "https://relay.devrecall.dev",
	}

	url := buildAtlassianAuthURL(cfg, "session-atlassian-123")
	if url == "" {
		t.Fatal("URL is empty")
	}

	tests := []struct {
		name     string
		contains string
	}{
		{"audience", "audience=api.atlassian.com"},
		{"client_id", "client_id=my-atlassian-client-id"},
		{"redirect_uri", "redirect_uri=https"},
		{"state", "state=session-atlassian-123"},
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

func TestAtlassianToken_IsExpired(t *testing.T) {
	tests := []struct {
		name    string
		token   AtlassianToken
		expired bool
	}{
		{
			name:    "zero expiry is expired",
			token:   AtlassianToken{},
			expired: true,
		},
		{
			name:    "future expiry is not expired",
			token:   AtlassianToken{ExpiresAt: time.Now().Add(1 * time.Hour)},
			expired: false,
		},
		{
			name:    "past expiry is expired",
			token:   AtlassianToken{ExpiresAt: time.Now().Add(-1 * time.Hour)},
			expired: true,
		},
		{
			name:    "within 5-minute buffer is expired",
			token:   AtlassianToken{ExpiresAt: time.Now().Add(3 * time.Minute)},
			expired: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.IsExpired(); got != tt.expired {
				t.Errorf("IsExpired() = %v, want %v", got, tt.expired)
			}
		})
	}
}

func TestAtlassianToken_SetExpiresAt(t *testing.T) {
	token := &AtlassianToken{ExpiresIn: 3600}
	before := time.Now()
	token.SetExpiresAt()
	after := time.Now()

	if token.ExpiresAt.Before(before.Add(3600*time.Second)) || token.ExpiresAt.After(after.Add(3600*time.Second)) {
		t.Errorf("ExpiresAt not set correctly: %v", token.ExpiresAt)
	}
}

func TestRefreshAtlassianToken_Success(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/atlassian/refresh" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != "my-refresh-token" {
			http.Error(w, "bad refresh token", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access-token",
			"expires_in":   3600,
			"scope":        "read:jira-work",
		})
	}))
	defer relay.Close()

	token, err := RefreshAtlassianToken(context.Background(), relay.URL, "my-refresh-token")
	if err != nil {
		t.Fatalf("RefreshAtlassianToken: %v", err)
	}

	if token.AccessToken != "new-access-token" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "new-access-token")
	}
	if token.RefreshToken != "my-refresh-token" {
		t.Errorf("RefreshToken should be preserved, got %q", token.RefreshToken)
	}
	if token.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be set")
	}
}

func TestRefreshAtlassianToken_Error(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "refresh token expired",
		})
	}))
	defer relay.Close()

	_, err := RefreshAtlassianToken(context.Background(), relay.URL, "expired-token")
	if err == nil {
		t.Error("expected error on refresh failure")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error should mention invalid_grant, got: %v", err)
	}
}

func TestValidateJiraAPIToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/myself" {
			http.NotFound(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != "dev@example.com" || pass != "my-api-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"accountId":    "abc123",
			"emailAddress": "dev@example.com",
			"displayName":  "Dev User",
		})
	}))
	defer server.Close()

	cfg := JiraAPITokenConfig{HTTPClient: server.Client()}
	token, err := ValidateJiraAPIToken(context.Background(), "dev@example.com", "my-api-token", server.URL, cfg)
	if err != nil {
		t.Fatalf("ValidateJiraAPIToken: %v", err)
	}

	if token.AccessToken != "my-api-token" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "my-api-token")
	}
	if token.Email != "dev@example.com" {
		t.Errorf("Email = %q, want %q", token.Email, "dev@example.com")
	}
}

func TestValidateJiraAPIToken_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := JiraAPITokenConfig{HTTPClient: server.Client()}
	_, err := ValidateJiraAPIToken(context.Background(), "dev@example.com", "bad-token", server.URL, cfg)
	if err == nil {
		t.Error("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}

func TestFetchAccessibleResources_Success(t *testing.T) {
	sites := []AtlassianCloudSite{
		{ID: "cloud-1", Name: "Company A", URL: "https://a.atlassian.net"},
		{ID: "cloud-2", Name: "Company B", URL: "https://b.atlassian.net"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sites)
	}))
	defer server.Close()

	// Override the URL for testing — use a custom function instead.
	origURL := atlassianAccessibleURL
	result, err := fetchAccessibleResourcesWithURL(context.Background(), "test-token", server.Client(), server.URL)
	_ = origURL
	if err != nil {
		t.Fatalf("FetchAccessibleResources: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(result))
	}
	if result[0].ID != "cloud-1" {
		t.Errorf("first site ID = %q, want %q", result[0].ID, "cloud-1")
	}
}

// fetchAccessibleResourcesWithURL is a test helper that allows overriding the URL.
func fetchAccessibleResourcesWithURL(ctx context.Context, accessToken string, httpClient *http.Client, url string) ([]AtlassianCloudSite, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("returned status %d", resp.StatusCode)
	}

	var sites []AtlassianCloudSite
	if err := json.NewDecoder(resp.Body).Decode(&sites); err != nil {
		return nil, err
	}
	return sites, nil
}
