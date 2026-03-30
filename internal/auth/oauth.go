package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

const (
	SlackClientID = "10837594647504.10806646919381"
	RelayBaseURL  = "https://relay.devrecall.dev"

	slackAuthURL  = "https://slack.com/oauth/v2/authorize"
	slackScopes   = "channels:history,channels:read,groups:history,groups:read,users:read,users:read.email,reactions:read,search:read"
	pollInterval  = 2 * time.Second
	pollTimeout   = 120 * time.Second
)

// SlackToken represents the token returned by the relay after a successful OAuth flow.
type SlackToken struct {
	AccessToken string `json:"access_token"`
	UserID      string `json:"user_id"`
	TeamID      string `json:"team_id"`
	TeamName    string `json:"team_name"`
	Scope       string `json:"scope"`
}

// SlackOAuthConfig holds parameters for the OAuth flow, allowing overrides for testing.
type SlackOAuthConfig struct {
	ClientID     string
	RelayBaseURL string
	HTTPClient   *http.Client
	OpenBrowser  func(url string) error
}

// DefaultSlackOAuthConfig returns the production configuration.
func DefaultSlackOAuthConfig() SlackOAuthConfig {
	return SlackOAuthConfig{
		ClientID:     SlackClientID,
		RelayBaseURL: RelayBaseURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		OpenBrowser:  openBrowser,
	}
}

// SlackOAuth performs the full Slack OAuth flow:
// 1. Generates a session ID
// 2. Opens the browser to Slack's consent page
// 3. Polls the relay for the token
func SlackOAuth(ctx context.Context, cfg SlackOAuthConfig) (*SlackToken, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generating session ID: %w", err)
	}

	authURL := buildSlackAuthURL(cfg, sessionID)

	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w\n\nManually open this URL:\n%s", err, authURL)
	}

	return pollForToken(ctx, cfg, sessionID)
}

func buildSlackAuthURL(cfg SlackOAuthConfig, state string) string {
	params := url.Values{
		"client_id":    {cfg.ClientID},
		"user_scope":   {slackScopes},
		"redirect_uri": {cfg.RelayBaseURL + "/oauth/slack/callback"},
		"state":        {state},
	}
	return slackAuthURL + "?" + params.Encode()
}

func pollForToken(ctx context.Context, cfg SlackOAuthConfig, sessionID string) (*SlackToken, error) {
	pollURL := cfg.RelayBaseURL + "/oauth/poll?session_id=" + url.QueryEscape(sessionID)
	deadline := time.After(pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting for Slack authorization (waited %s)", pollTimeout)
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := cfg.HTTPClient.Do(req)
		if err != nil {
			// Network error, retry.
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

		var token SlackToken
		if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
			return nil, fmt.Errorf("decoding token: %w", err)
		}
		return &token, nil
	}
}

func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func openBrowser(rawURL string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	args = append(args, rawURL)
	return exec.Command(cmd, args...).Start()
}
