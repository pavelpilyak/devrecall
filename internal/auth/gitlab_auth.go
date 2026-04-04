package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	gitlabDefaultBaseURL = "https://gitlab.com"
)

// GitLabToken represents a validated GitLab PAT with user info.
type GitLabToken struct {
	AccessToken string `json:"access_token"`
	Username    string `json:"username"`
	BaseURL     string `json:"base_url"` // gitlab.com or self-hosted URL
}

// GitLabAuthConfig holds parameters for GitLab PAT validation.
type GitLabAuthConfig struct {
	HTTPClient *http.Client
	BaseURL    string // override for testing or self-hosted; defaults to https://gitlab.com
}

// DefaultGitLabAuthConfig returns the production configuration.
func DefaultGitLabAuthConfig() GitLabAuthConfig {
	return GitLabAuthConfig{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		BaseURL:    gitlabDefaultBaseURL,
	}
}

// ValidateGitLabPAT validates a personal access token by calling /api/v4/user.
func ValidateGitLabPAT(ctx context.Context, pat string, cfg GitLabAuthConfig) (*GitLabToken, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = gitlabDefaultBaseURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v4/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", pat)

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitLab API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid token: GitLab returned 401 Unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned status %d", resp.StatusCode)
	}

	var user struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user response: %w", err)
	}

	return &GitLabToken{
		AccessToken: pat,
		Username:    user.Username,
		BaseURL:     baseURL,
	}, nil
}
