package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

	pickupSecret, err := startOAuthSession(ctx, cfg.HTTPClient, cfg.RelayBaseURL, sessionID)
	if err != nil {
		return nil, err
	}

	authURL := buildSlackAuthURL(cfg, sessionID)

	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w\n\nManually open this URL:\n%s", err, authURL)
	}

	return pollForToken(ctx, cfg, sessionID, pickupSecret)
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

func pollForToken(ctx context.Context, cfg SlackOAuthConfig, sessionID, pickupSecret string) (*SlackToken, error) {
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
		req.Header.Set("Authorization", "Bearer "+pickupSecret)

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

// generatePickupSecret returns a random 32-byte secret (base64url-encoded,
// no padding) and its SHA-256 digest as lowercase hex. The secret is
// presented on `/oauth/poll` to prove the caller initiated the flow; only
// the digest is sent to the relay at session start.
func generatePickupSecret() (secret, hashHex string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(secret))
	hashHex = hex.EncodeToString(sum[:])
	return secret, hashHex, nil
}

// startOAuthSession pre-registers a session with the relay so that (a) the
// relay will reject callbacks with attacker-chosen `state` values, and
// (b) the poll request can require proof-of-possession of the pickup secret.
// Returns the pickup secret that the caller must present on poll.
func startOAuthSession(ctx context.Context, client *http.Client, relayBaseURL, state string) (string, error) {
	secret, hashHex, err := generatePickupSecret()
	if err != nil {
		return "", fmt.Errorf("generating pickup secret: %w", err)
	}
	body, _ := json.Marshal(map[string]string{
		"state":       state,
		"pickup_hash": hashHex,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		relayBaseURL+"/oauth/session/start", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("registering OAuth session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("relay rejected session registration: status %d", resp.StatusCode)
	}
	return secret, nil
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
