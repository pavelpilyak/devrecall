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
	GoogleClientID = "331254799507-scg3nasfrd7dsd1a6pqgqumhsb8utov4.apps.googleusercontent.com"

	googleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleScopes  = "https://www.googleapis.com/auth/calendar.readonly https://www.googleapis.com/auth/userinfo.email"
)

// GoogleToken represents the token returned by the relay after a successful Google OAuth flow.
type GoogleToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Email        string    `json:"email"`
	Scope        string    `json:"scope"`
}

// IsExpired returns true if the access token has expired (with a 5-minute buffer).
func (t *GoogleToken) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return true // no expiry info → assume expired
	}
	return time.Now().After(t.ExpiresAt.Add(-5 * time.Minute))
}

// SetExpiresAt sets ExpiresAt from ExpiresIn (seconds from now).
func (t *GoogleToken) SetExpiresAt() {
	if t.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
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

	pickupSecret, err := startOAuthSession(ctx, cfg.HTTPClient, cfg.RelayBaseURL, sessionID)
	if err != nil {
		return nil, err
	}

	authURL := buildGoogleAuthURL(cfg, sessionID)

	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w\n\nManually open this URL:\n%s", err, authURL)
	}

	return pollForGoogleToken(ctx, cfg, sessionID, pickupSecret)
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

func pollForGoogleToken(ctx context.Context, cfg GoogleOAuthConfig, sessionID, pickupSecret string) (*GoogleToken, error) {
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
		req.Header.Set("Authorization", "Bearer "+pickupSecret)

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
		token.SetExpiresAt()
		return &token, nil
	}
}

// RefreshGoogleToken uses the relay to exchange a refresh token for a new access token.
func RefreshGoogleToken(ctx context.Context, relayBaseURL string, refreshToken string) (*GoogleToken, error) {
	body, _ := json.Marshal(map[string]string{
		"refresh_token": refreshToken,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		relayBaseURL+"/oauth/google/refresh",
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

	token := &GoogleToken{
		AccessToken:  result.AccessToken,
		RefreshToken: refreshToken, // refresh token doesn't change
		ExpiresIn:    result.ExpiresIn,
		Scope:        result.Scope,
	}
	token.SetExpiresAt()
	return token, nil
}
