package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	GoogleClientID = "331254799507-scg3nasfrd7dsd1a6pqgqumhsb8utov4.apps.googleusercontent.com"

	googleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleScopes  = "https://www.googleapis.com/auth/calendar.readonly https://www.googleapis.com/auth/userinfo.email"
)

// GoogleToken represents the token returned by the relay after a successful Google OAuth flow.
type GoogleToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Email        string `json:"email"`
	Scope        string `json:"scope"`
}

// GoogleOAuthConfig holds parameters for the Google OAuth flow.
type GoogleOAuthConfig struct {
	ClientID     string
	RelayBaseURL string
	HTTPClient   *http.Client
	OpenBrowser  func(url string) error
}

// DefaultGoogleOAuthConfig returns the production configuration.
func DefaultGoogleOAuthConfig() GoogleOAuthConfig {
	return GoogleOAuthConfig{
		ClientID:     GoogleClientID,
		RelayBaseURL: RelayBaseURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		OpenBrowser:  openBrowser,
	}
}

// GoogleOAuth performs the full Google OAuth flow:
// 1. Generates a session ID
// 2. Opens the browser to Google's consent page
// 3. Polls the relay for the token (includes refresh_token)
func GoogleOAuth(ctx context.Context, cfg GoogleOAuthConfig) (*GoogleToken, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generating session ID: %w", err)
	}

	authURL := buildGoogleAuthURL(cfg, sessionID)

	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w\n\nManually open this URL:\n%s", err, authURL)
	}

	return pollForGoogleToken(ctx, cfg, sessionID)
}

func buildGoogleAuthURL(cfg GoogleOAuthConfig, state string) string {
	params := url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {cfg.RelayBaseURL + "/oauth/google/callback"},
		"response_type": {"code"},
		"scope":         {googleScopes},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	return googleAuthURL + "?" + params.Encode()
}

func pollForGoogleToken(ctx context.Context, cfg GoogleOAuthConfig, sessionID string) (*GoogleToken, error) {
	pollURL := cfg.RelayBaseURL + "/oauth/poll?session_id=" + url.QueryEscape(sessionID)
	deadline := time.After(pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting for Google authorization (waited %s)", pollTimeout)
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

		var token GoogleToken
		if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
			return nil, fmt.Errorf("decoding token: %w", err)
		}
		return &token, nil
	}
}
