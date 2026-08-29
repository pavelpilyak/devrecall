package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NotionAPIVersion is sent on every request. Notion requires it and pins
// response shapes to that date, so bumping it is a deliberate act.
const NotionAPIVersion = "2022-06-28"

// NotionToken is an internal-integration secret plus the workspace member we
// resolved it to.
//
// Notion's token alone cannot tell us who the user is: for an internal
// integration /v1/users/me returns a *workspace-owned bot* whose owner is the
// workspace itself, with no human attached. So we resolve the person separately
// by matching an email against the workspace member list, and store that — the
// collector needs a user id to tell "pages I touched" from everyone else's.
type NotionToken struct {
	AccessToken   string `json:"access_token"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	UserID        string `json:"user_id"`
	UserName      string `json:"user_name"`
	Email         string `json:"email"`
}

// NotionConfig holds endpoint parameters, overridable in tests.
type NotionConfig struct {
	BaseURL string
	Client  *http.Client
}

// DefaultNotionConfig returns the production configuration.
func DefaultNotionConfig() NotionConfig {
	return NotionConfig{
		BaseURL: "https://api.notion.com",
		Client:  &http.Client{Timeout: 20 * time.Second},
	}
}

// NotionPerson is one human member of a Notion workspace.
type NotionPerson struct {
	ID    string
	Name  string
	Email string
}

func (c NotionConfig) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c NotionConfig) get(ctx context.Context, token, path string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.notion.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Notion-Version", NotionAPIVersion)

	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("contacting Notion: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return fmt.Errorf("Notion rejected the token (401) — check you copied the full secret")
	case http.StatusForbidden:
		return fmt.Errorf("Notion returned 403 — the integration is missing a capability it needs (enable \"Read content\" and \"Read user information including email addresses\")")
	default:
		return fmt.Errorf("Notion returned HTTP %d for %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ValidateNotionToken checks the token works and returns the workspace it
// belongs to. It does not identify a person — see ListNotionPeople.
func ValidateNotionToken(ctx context.Context, token string, cfg NotionConfig) (*NotionToken, error) {
	var me struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Bot  struct {
			WorkspaceID   string `json:"workspace_id"`
			WorkspaceName string `json:"workspace_name"`
		} `json:"bot"`
	}
	if err := cfg.get(ctx, token, "/v1/users/me", &me); err != nil {
		return nil, err
	}
	return &NotionToken{
		AccessToken:   token,
		WorkspaceID:   me.Bot.WorkspaceID,
		WorkspaceName: me.Bot.WorkspaceName,
	}, nil
}

// ListNotionPeople returns the human members of the workspace, so the caller can
// resolve which one is "you". Bots are omitted. Requires the integration's user
// -information capability; without it Notion answers 403.
func ListNotionPeople(ctx context.Context, token string, cfg NotionConfig) ([]NotionPerson, error) {
	var out []NotionPerson
	cursor := ""
	for {
		path := "/v1/users?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Results []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Type   string `json:"type"`
				Person struct {
					Email string `json:"email"`
				} `json:"person"`
			} `json:"results"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := cfg.get(ctx, token, path, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Results {
			if r.Type != "person" {
				continue
			}
			out = append(out, NotionPerson{ID: r.ID, Name: r.Name, Email: r.Person.Email})
		}
		if !page.HasMore || page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
	}
}
