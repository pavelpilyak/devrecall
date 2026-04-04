package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	AtlassianClientID = "h23pXmwIPnKbmzUpeWw3s0bhiHrLKOJB"

	atlassianAuthURL       = "https://auth.atlassian.com/authorize"
	atlassianScopes        = "read:jira-work read:jira-user offline_access"
	atlassianAccessibleURL = "https://api.atlassian.com/oauth/token/accessible-resources"
	atlassianMyselfURL     = "https://api.atlassian.com/ex/jira/%s/rest/api/3/myself"
)

// AtlassianCloudSite represents an accessible Jira cloud site.
type AtlassianCloudSite struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// AtlassianToken represents the token returned by the relay after a successful Atlassian OAuth flow.
type AtlassianToken struct {
	AccessToken  string               `json:"access_token"`
	RefreshToken string               `json:"refresh_token"`
	ExpiresIn    int                  `json:"expires_in"`
	ExpiresAt    time.Time            `json:"expires_at,omitempty"`
	Scope        string               `json:"scope"`
	Email        string               `json:"email"`
	CloudSites   []AtlassianCloudSite `json:"cloud_sites"`
}

// IsExpired returns true if the access token has expired (with a 5-minute buffer).
func (t *AtlassianToken) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().After(t.ExpiresAt.Add(-5 * time.Minute))
}

// SetExpiresAt sets ExpiresAt from ExpiresIn (seconds from now).
func (t *AtlassianToken) SetExpiresAt() {
	if t.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
}

// JiraOAuthConfig holds parameters for the Atlassian OAuth flow.
type JiraOAuthConfig struct {
	ClientID     string
	RelayBaseURL string
	HTTPClient   *http.Client
	OpenBrowser  func(url string) error
}

// DefaultJiraOAuthConfig returns the production configuration.
func DefaultJiraOAuthConfig() JiraOAuthConfig {
	return JiraOAuthConfig{
		ClientID:     AtlassianClientID,
		RelayBaseURL: RelayBaseURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		OpenBrowser:  openBrowser,
	}
}

// AtlassianOAuth performs the full Atlassian OAuth 2.0 (3LO) flow:
// 1. Generates a session ID
// 2. Opens the browser to Atlassian's consent page
// 3. Polls the relay for the token (includes refresh_token + cloud sites)
func AtlassianOAuth(ctx context.Context, cfg JiraOAuthConfig) (*AtlassianToken, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generating session ID: %w", err)
	}

	authURL := buildAtlassianAuthURL(cfg, sessionID)

	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w\n\nManually open this URL:\n%s", err, authURL)
	}

	return pollForAtlassianToken(ctx, cfg, sessionID)
}

func buildAtlassianAuthURL(cfg JiraOAuthConfig, state string) string {
	params := url.Values{
		"audience":      {"api.atlassian.com"},
		"client_id":     {cfg.ClientID},
		"scope":         {atlassianScopes},
		"redirect_uri":  {cfg.RelayBaseURL + "/oauth/atlassian/callback"},
		"response_type": {"code"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	return atlassianAuthURL + "?" + params.Encode()
}

func pollForAtlassianToken(ctx context.Context, cfg JiraOAuthConfig, sessionID string) (*AtlassianToken, error) {
	pollURL := cfg.RelayBaseURL + "/oauth/poll?session_id=" + url.QueryEscape(sessionID)
	deadline := time.After(pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting for Atlassian authorization (waited %s)", pollTimeout)
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := cfg.HTTPClient.Do(req)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			time.Sleep(pollInterval)
			continue
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("relay returned status %d", resp.StatusCode)
		}

		var token AtlassianToken
		if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
			return nil, fmt.Errorf("decoding token: %w", err)
		}
		token.SetExpiresAt()
		return &token, nil
	}
}

// RefreshAtlassianToken uses the relay to exchange a refresh token for a new access token.
func RefreshAtlassianToken(ctx context.Context, relayBaseURL string, refreshToken string) (*AtlassianToken, error) {
	body, _ := json.Marshal(map[string]string{
		"refresh_token": refreshToken,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		relayBaseURL+"/oauth/atlassian/refresh",
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("refresh failed: %s (%s)", errResp.Error, errResp.Description)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding refresh response: %w", err)
	}

	token := &AtlassianToken{
		AccessToken:  result.AccessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    result.ExpiresIn,
		Scope:        result.Scope,
	}
	token.SetExpiresAt()
	return token, nil
}

// JiraAPITokenConfig holds parameters for API token validation.
type JiraAPITokenConfig struct {
	HTTPClient *http.Client
}

// DefaultJiraAPITokenConfig returns the production configuration.
func DefaultJiraAPITokenConfig() JiraAPITokenConfig {
	return JiraAPITokenConfig{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ValidateJiraAPIToken validates a Jira API token by calling /rest/api/3/myself.
// baseURL should be the Jira instance URL (e.g., "https://mycompany.atlassian.net").
func ValidateJiraAPIToken(ctx context.Context, email, apiToken, baseURL string, cfg JiraAPITokenConfig) (*AtlassianToken, error) {
	myselfURL := baseURL + "/rest/api/3/myself"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, myselfURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(email, apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Jira API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid credentials: Jira returned 401 Unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Jira API returned status %d", resp.StatusCode)
	}

	var user struct {
		AccountID    string `json:"accountId"`
		EmailAddress string `json:"emailAddress"`
		DisplayName  string `json:"displayName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user response: %w", err)
	}

	return &AtlassianToken{
		AccessToken: apiToken,
		Email:       user.EmailAddress,
	}, nil
}

// FetchAccessibleResources fetches the Jira cloud sites accessible with the given OAuth token.
func FetchAccessibleResources(ctx context.Context, accessToken string, httpClient *http.Client) ([]AtlassianCloudSite, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, atlassianAccessibleURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching accessible resources: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("accessible resources returned status %d", resp.StatusCode)
	}

	var sites []AtlassianCloudSite
	if err := json.NewDecoder(resp.Body).Decode(&sites); err != nil {
		return nil, fmt.Errorf("decoding accessible resources: %w", err)
	}
	return sites, nil
}
