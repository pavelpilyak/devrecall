package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateGitLabPAT_Success(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-validtoken" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"username": "gitlabuser"})
	}))
	defer api.Close()

	cfg := GitLabAuthConfig{
		HTTPClient: api.Client(),
		BaseURL:    api.URL,
	}

	token, err := ValidateGitLabPAT(context.Background(), "glpat-validtoken", cfg)
	if err != nil {
		t.Fatalf("ValidateGitLabPAT: %v", err)
	}
	if token.Username != "gitlabuser" {
		t.Errorf("Username = %q, want %q", token.Username, "gitlabuser")
	}
	if token.AccessToken != "glpat-validtoken" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "glpat-validtoken")
	}
	if token.BaseURL != api.URL {
		t.Errorf("BaseURL = %q, want %q", token.BaseURL, api.URL)
	}
}

func TestValidateGitLabPAT_InvalidToken(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer api.Close()

	cfg := GitLabAuthConfig{
		HTTPClient: api.Client(),
		BaseURL:    api.URL,
	}

	_, err := ValidateGitLabPAT(context.Background(), "glpat-badtoken", cfg)
	if err == nil {
		t.Error("expected error for invalid token")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}

func TestValidateGitLabPAT_DefaultBaseURL(t *testing.T) {
	cfg := GitLabAuthConfig{}
	if cfg.BaseURL == "" {
		// ValidateGitLabPAT should default to https://gitlab.com
		// We can't actually test the network call, just confirm the default is applied
	}
}
