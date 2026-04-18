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
// Username is the Basic Auth principal (email for scoped API tokens, nickname for legacy app
// passwords). UUID identifies the user in PR author/reviewer payloads regardless of auth mode.
type BitbucketToken struct {
	Username    string `json:"username"`
	AppPassword string `json:"app_password"`
	UUID        string `json:"uuid,omitempty"`
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
		UUID     string `json:"uuid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user response: %w", err)
	}

	// Preserve the caller-provided principal as-is: scoped API tokens require the
	// email for Basic Auth, while legacy app passwords use the nickname. Using
	// user.Username from the response would break scoped-token auth on reload.
	return &BitbucketToken{
		Username:    username,
		AppPassword: appPassword,
		UUID:        user.UUID,
	}, nil
}
