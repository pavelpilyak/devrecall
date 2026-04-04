package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	bitbucketAPIBase = "https://api.bitbucket.org"
)

// BitbucketToken represents a validated Bitbucket app password with user info.
type BitbucketToken struct {
	Username    string `json:"username"`
	AppPassword string `json:"app_password"`
}

// BitbucketAuthConfig holds parameters for Bitbucket app password validation.
type BitbucketAuthConfig struct {
	HTTPClient *http.Client
	APIURL     string // override for testing; defaults to bitbucketAPIBase
}

// DefaultBitbucketAuthConfig returns the production configuration.
func DefaultBitbucketAuthConfig() BitbucketAuthConfig {
	return BitbucketAuthConfig{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		APIURL:     bitbucketAPIBase,
	}
}

// ValidateBitbucketAppPassword validates credentials by calling /2.0/user.
func ValidateBitbucketAppPassword(ctx context.Context, username, appPassword string, cfg BitbucketAuthConfig) (*BitbucketToken, error) {
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = bitbucketAPIBase
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/2.0/user", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(username, appPassword)

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Bitbucket API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid credentials: Bitbucket returned 401 Unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bitbucket API returned status %d", resp.StatusCode)
	}

	var user struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user response: %w", err)
	}

	return &BitbucketToken{
		Username:    user.Username,
		AppPassword: appPassword,
	}, nil
}
