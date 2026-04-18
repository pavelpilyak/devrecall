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
	LinearClientID = "ced4e92c7d07188d43d00201010e6e5b"

	linearAuthURL    = "https://linear.app/oauth/authorize"
	linearScopes     = "read"
	linearGraphQLURL = "https://api.linear.app/graphql"
)

// LinearToken represents the token returned by the relay after a successful Linear OAuth flow.
// Linear tokens do not expire, so there is no refresh token or expiry.
type LinearToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	Email       string `json:"email"`
}

// LinearOAuthConfig holds parameters for the Linear OAuth flow.
type LinearOAuthConfig struct {
	ClientID     string
	RelayBaseURL string
	HTTPClient   *http.Client
	OpenBrowser  func(url string) error
}

// DefaultLinearOAuthConfig returns the production configuration.
func DefaultLinearOAuthConfig() LinearOAuthConfig {
	return LinearOAuthConfig{
		ClientID:     LinearClientID,
		RelayBaseURL: RelayBaseURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		OpenBrowser:  openBrowser,
	}
}

// LinearOAuth performs the full Linear OAuth 2.0 flow:
// 1. Generates a session ID
// 2. Opens the browser to Linear's consent page
// 3. Polls the relay for the token
func LinearOAuth(ctx context.Context, cfg LinearOAuthConfig) (*LinearToken, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generating session ID: %w", err)
	}

	pickupSecret, err := startOAuthSession(ctx, cfg.HTTPClient, cfg.RelayBaseURL, sessionID)
	if err != nil {
		return nil, err
	}

	authURL := buildLinearAuthURL(cfg, sessionID)

	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w\n\nManually open this URL:\n%s", err, authURL)
	}

	return pollForLinearToken(ctx, cfg, sessionID, pickupSecret)
}

func buildLinearAuthURL(cfg LinearOAuthConfig, state string) string {
	params := url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {cfg.RelayBaseURL + "/oauth/linear/callback"},
		"response_type": {"code"},
		"scope":         {linearScopes},
		"prompt":        {"consent"},
		"state":         {state},
	}
	return linearAuthURL + "?" + params.Encode()
}

func pollForLinearToken(ctx context.Context, cfg LinearOAuthConfig, sessionID, pickupSecret string) (*LinearToken, error) {
	pollURL := cfg.RelayBaseURL + "/oauth/poll?session_id=" + url.QueryEscape(sessionID)
	deadline := time.After(pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting for Linear authorization (waited %s)", pollTimeout)
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

		var token LinearToken
		if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
			return nil, fmt.Errorf("decoding token: %w", err)
		}
		return &token, nil
	}
}

// LinearAPIKeyConfig holds parameters for API key validation.
type LinearAPIKeyConfig struct {
	HTTPClient *http.Client
	GraphQLURL string // override for testing; defaults to linearGraphQLURL
}

// DefaultLinearAPIKeyConfig returns the production configuration.
func DefaultLinearAPIKeyConfig() LinearAPIKeyConfig {
	return LinearAPIKeyConfig{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		GraphQLURL: linearGraphQLURL,
	}
}

// ValidateLinearAPIKey validates a personal API key by querying the viewer.
func ValidateLinearAPIKey(ctx context.Context, apiKey string, cfg LinearAPIKeyConfig) (*LinearToken, error) {
	gqlURL := cfg.GraphQLURL
	if gqlURL == "" {
		gqlURL = linearGraphQLURL
	}

	query := `{"query":"{ viewer { id name email } }"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gqlURL, bytes.NewBufferString(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Linear API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid API key: Linear returned 401 Unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Linear API returned status %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Viewer struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding viewer response: %w", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("Linear GraphQL error: %s", result.Errors[0].Message)
	}

	return &LinearToken{
		AccessToken: apiKey,
		UserID:      result.Data.Viewer.ID,
		UserName:    result.Data.Viewer.Name,
		Email:       result.Data.Viewer.Email,
	}, nil
}
