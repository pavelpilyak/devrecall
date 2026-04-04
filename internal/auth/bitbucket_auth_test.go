package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateBitbucketAppPassword_Success(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2.0/user" {
			http.NotFound(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "bbuser" || pass != "app-pass-123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"username": "bbuser"})
	}))
	defer api.Close()

	cfg := BitbucketAuthConfig{
		HTTPClient: api.Client(),
		APIURL:     api.URL,
	}

	token, err := ValidateBitbucketAppPassword(context.Background(), "bbuser", "app-pass-123", cfg)
	if err != nil {
		t.Fatalf("ValidateBitbucketAppPassword: %v", err)
	}
	if token.Username != "bbuser" {
		t.Errorf("Username = %q, want %q", token.Username, "bbuser")
	}
	if token.AppPassword != "app-pass-123" {
		t.Errorf("AppPassword = %q, want %q", token.AppPassword, "app-pass-123")
	}
}

func TestValidateBitbucketAppPassword_InvalidCredentials(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer api.Close()

	cfg := BitbucketAuthConfig{
		HTTPClient: api.Client(),
		APIURL:     api.URL,
	}

	_, err := ValidateBitbucketAppPassword(context.Background(), "bbuser", "wrong-pass", cfg)
	if err == nil {
		t.Error("expected error for invalid credentials")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}
