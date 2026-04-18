package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const (
	GitHubClientID = "Ov23liogroUUKiV5COtF"

	githubAuthURL = "https://github.com/login/oauth/authorize"
	githubScopes  = "repo read:user user:email"
	githubAPIBase = "https://api.github.com"
)

// GitHubToken represents the token returned by the relay after a successful GitHub OAuth flow.
type GitHubToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Username    string `json:"username"`
}

// GitHubOAuthConfig holds parameters for the GitHub OAuth flow.
type GitHubOAuthConfig struct {
	ClientID     string
	RelayBaseURL string
	HTTPClient   *http.Client
	OpenBrowser  func(url string) error
}

// DefaultGitHubOAuthConfig returns the production configuration.
func DefaultGitHubOAuthConfig() GitHubOAuthConfig {
	return GitHubOAuthConfig{
		ClientID:     GitHubClientID,
		RelayBaseURL: RelayBaseURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		OpenBrowser:  openBrowser,
	}
}

// GitHubOAuth performs the full GitHub OAuth flow:
// 1. Generates a session ID
// 2. Opens the browser to GitHub's consent page
// 3. Polls the relay for the token
func GitHubOAuth(ctx context.Context, cfg GitHubOAuthConfig) (*GitHubToken, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generating session ID: %w", err)
	}

	pickupSecret, err := startOAuthSession(ctx, cfg.HTTPClient, cfg.RelayBaseURL, sessionID)
	if err != nil {
		return nil, err
	}

	authURL := buildGitHubAuthURL(cfg, sessionID)

	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w\n\nManually open this URL:\n%s", err, authURL)
	}

	return pollForGitHubToken(ctx, cfg, sessionID, pickupSecret)
}

func buildGitHubAuthURL(cfg GitHubOAuthConfig, state string) string {
	params := url.Values{
		"client_id":    {cfg.ClientID},
		"redirect_uri": {cfg.RelayBaseURL + "/oauth/github/callback"},
		"scope":        {githubScopes},
		"state":        {state},
	}
	return githubAuthURL + "?" + params.Encode()
}

func pollForGitHubToken(ctx context.Context, cfg GitHubOAuthConfig, sessionID, pickupSecret string) (*GitHubToken, error) {
	pollURL := cfg.RelayBaseURL + "/oauth/poll?session_id=" + url.QueryEscape(sessionID)
	deadline := time.After(pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting for GitHub authorization (waited %s)", pollTimeout)
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

		var token GitHubToken
		if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
			return nil, fmt.Errorf("decoding token: %w", err)
		}
		return &token, nil
	}
}

// GitHubPATConfig holds parameters for PAT validation.
type GitHubPATConfig struct {
	HTTPClient *http.Client
	APIURL     string // override for testing; defaults to githubAPIBase
}

// DefaultGitHubPATConfig returns the production PAT configuration.
func DefaultGitHubPATConfig() GitHubPATConfig {
	return GitHubPATConfig{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		APIURL:     githubAPIBase,
	}
}

// ValidateGitHubPAT validates a personal access token by calling /user and returns username.
func ValidateGitHubPAT(ctx context.Context, pat string, cfg GitHubPATConfig) (*GitHubToken, error) {
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = githubAPIBase
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid token: GitHub returned 401 Unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user response: %w", err)
	}

	// Extract scopes from response header.
	scopes := resp.Header.Get("X-OAuth-Scopes")

	return &GitHubToken{
		AccessToken: pat,
		TokenType:   "bearer",
		Scope:       scopes,
		Username:    user.Login,
	}, nil
}

// GitHubFromGHCLI attempts to get a token from the gh CLI tool.
func GitHubFromGHCLI(ctx context.Context, cfg GitHubPATConfig) (*GitHubToken, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return nil, fmt.Errorf("gh CLI not available or not authenticated: %w", err)
	}

	pat := strings.TrimSpace(string(out))
	if pat == "" {
		return nil, fmt.Errorf("gh auth token returned empty string")
	}

	return ValidateGitHubPAT(ctx, pat, cfg)
}
