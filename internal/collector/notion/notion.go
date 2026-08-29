// Package notion collects pages you authored or edited in a Notion workspace.
//
// Notion's search API has no server-side "mine only" filter, so the collector
// pages through results newest-first and keeps the ones where you are the
// creator or the last editor. That means the useful stopping condition is time,
// not a match count: once results fall behind the sync window there is nothing
// older worth scanning.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/collector/ratelimit"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

const (
	defaultBaseURL = "https://api.notion.com"
	pageSize       = 100
	// maxPages bounds a single collection run. Notion allows ~3 requests/second,
	// so an unbounded walk of a large workspace would take minutes and mostly
	// return other people's pages.
	maxPages = 20

	// maxContentChars bounds the page text we store. Enough for search and a
	// useful digest without turning every page into a wall of text; also stays
	// under the embedding model's sequence limit.
	maxContentChars = 2000

	// maxContentFetches bounds how many pages we pull bodies for in one run.
	// Body text needs one extra request per page against a ~3 req/s limit, so
	// this is the difference between a sync that takes seconds and one that
	// takes minutes.
	maxContentFetches = 60
)

// Collector reads pages from one Notion workspace.
type Collector struct {
	token   string
	userID  string
	baseURL string
	client  *http.Client
}

// New returns a collector for the given integration token, attributing activity
// to the workspace member userID.
func New(token, userID string) *Collector {
	return &Collector{
		token:   token,
		userID:  userID,
		baseURL: defaultBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient overrides the endpoint and HTTP client, for tests.
func NewWithClient(token, userID, baseURL string, client *http.Client) *Collector {
	c := New(token, userID)
	if baseURL != "" {
		c.baseURL = baseURL
	}
	if client != nil {
		c.client = client
	}
	return c
}

func (c *Collector) Name() models.Source { return models.SourceNotion }

// Collect fetches recent pages using the default window.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
}

// CollectSince returns pages you created or last edited at or after `since`.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	var out []models.Activity
	cursor := ""
	budget := maxContentFetches

	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res, err := c.search(ctx, cursor)
		if err != nil {
			return nil, err
		}

		for _, p := range res.Results {
			edited := parseTime(p.LastEditedTime)
			// Results are newest-first, so the first page older than the window
			// means everything after it is older too.
			if !since.IsZero() && !edited.IsZero() && edited.Before(since) {
				return out, nil
			}
			a, ok := c.toActivity(p)
			if !ok {
				continue
			}
			// Titles alone make for useless search and a digest that just
			// restates the title, so pull a slice of the body — but only while
			// the per-run budget lasts, since it costs a request per page.
			if budget > 0 {
				budget--
				if text, err := c.pageText(ctx, p.ID); err == nil {
					a.Content = text
				}
			}
			out = append(out, a)
		}

		if !res.HasMore || res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return out, nil
}

type searchResponse struct {
	Results    []notionPage `json:"results"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor"`
}

type notionPage struct {
	Object         string          `json:"object"`
	ID             string          `json:"id"`
	CreatedTime    string          `json:"created_time"`
	LastEditedTime string          `json:"last_edited_time"`
	CreatedBy      partialUser     `json:"created_by"`
	LastEditedBy   partialUser     `json:"last_edited_by"`
	URL            string          `json:"url"`
	Archived       bool            `json:"archived"`
	InTrash        bool            `json:"in_trash"`
	Parent         notionParent    `json:"parent"`
	Properties     json.RawMessage `json:"properties"`
}

type partialUser struct {
	ID string `json:"id"`
}

type notionParent struct {
	Type       string `json:"type"`
	DatabaseID string `json:"database_id"`
	PageID     string `json:"page_id"`
	Workspace  bool   `json:"workspace"`
}

func (c *Collector) search(ctx context.Context, cursor string) (*searchResponse, error) {
	body := map[string]any{
		"page_size": pageSize,
		// Pages only: databases are containers, not work you did.
		"filter": map[string]any{"value": "page", "property": "object"},
		"sort":   map[string]any{"direction": "descending", "timestamp": "last_edited_time"},
	}
	if cursor != "" {
		body["start_cursor"] = cursor
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	resp, err := ratelimit.Do(ctx, c.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(c.baseURL, "/")+"/v1/search", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Notion-Version", auth.NotionAPIVersion)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("notion search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("notion search returned HTTP %d", resp.StatusCode)
	}
	var out searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding notion search: %w", err)
	}
	return &out, nil
}

// pageText returns the leading plain text of a page's top-level blocks.
//
// Only the first level is walked: nested children would need a request per
// block and the opening paragraphs carry the gist, which is what search and the
// digest need. Failures are the caller's cue to store the page without a body
// rather than lose the page.
func (c *Collector) pageText(ctx context.Context, pageID string) (string, error) {
	resp, err := ratelimit.Do(ctx, c.client, func() (*http.Request, error) {
		u := fmt.Sprintf("%s/v1/blocks/%s/children?page_size=100",
			strings.TrimRight(c.baseURL, "/"), url.PathEscape(pageID))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Notion-Version", auth.NotionAPIVersion)
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("notion blocks returned HTTP %d", resp.StatusCode)
	}

	// Every text-bearing block type nests its content the same way:
	// {"type":"paragraph","paragraph":{"rich_text":[{"plain_text":"..."}]}}
	// so decode generically rather than enumerating ~15 block types.
	var body struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}

	var b strings.Builder
	for _, block := range body.Results {
		typeRaw, ok := block["type"]
		if !ok {
			continue
		}
		var blockType string
		if json.Unmarshal(typeRaw, &blockType) != nil {
			continue
		}
		payload, ok := block[blockType]
		if !ok {
			continue
		}
		var inner struct {
			RichText []struct {
				PlainText string `json:"plain_text"`
			} `json:"rich_text"`
		}
		if json.Unmarshal(payload, &inner) != nil {
			continue
		}
		var line strings.Builder
		for _, t := range inner.RichText {
			line.WriteString(t.PlainText)
		}
		if s := strings.TrimSpace(line.String()); s != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s)
			if b.Len() >= maxContentChars {
				break
			}
		}
	}
	return truncateRunes(b.String(), maxContentChars), nil
}

// truncateRunes clips to at most n bytes without splitting a rune. Go strings
// are byte-indexed, so a naive slice writes invalid UTF-8 whenever the cut lands
// mid-codepoint — which non-ASCII page content does routinely.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut])
}

// pageMeta is the metadata blob stored on the activity.
type pageMeta struct {
	PageID       string `json:"page_id"`
	URL          string `json:"url,omitempty"`
	ParentType   string `json:"parent_type,omitempty"`
	ParentID     string `json:"parent_id,omitempty"`
	Action       string `json:"action"`
	CreatedAt    string `json:"created_at,omitempty"`
	LastEditedAt string `json:"last_edited_at,omitempty"`
}

// sameMoment reports whether a page was created and last edited in the same
// sitting. Notion timestamps are minute-granular, so an exact match is too
// strict for a page written over a few minutes.
func sameMoment(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	d := b.Sub(a)
	if d < 0 {
		d = -d
	}
	return d <= 10*time.Minute
}

// toActivity converts a page to an activity, or reports false when the page
// isn't yours or isn't worth storing.
func (c *Collector) toActivity(p notionPage) (models.Activity, bool) {
	if p.Archived || p.InTrash {
		return models.Activity{}, false
	}
	created := p.CreatedBy.ID == c.userID
	edited := p.LastEditedBy.ID == c.userID
	if !created && !edited {
		return models.Activity{}, false
	}

	createdAt := parseTime(p.CreatedTime)
	editedAt := parseTime(p.LastEditedTime)

	// The action has to describe what happened *at the activity's timestamp*,
	// or a page you made years ago and touched yesterday gets reported as
	// "created" yesterday.
	//
	// If you made the last edit, the activity is that edit — and it only counts
	// as authoring when the page was created in the same breath. If someone else
	// edited last, the newest thing *you* did is creating it, so the activity
	// belongs at creation time rather than at an edit that wasn't yours.
	action := "edited"
	ts := editedAt
	switch {
	case edited && created && sameMoment(createdAt, editedAt):
		action = "created"
	case !edited && created:
		action = "created"
		ts = createdAt
	}

	title := pageTitle(p.Properties)
	if title == "" {
		title = "Untitled Notion page"
	}

	meta := pageMeta{
		PageID:       p.ID,
		URL:          p.URL,
		ParentType:   p.Parent.Type,
		ParentID:     firstNonEmpty(p.Parent.DatabaseID, p.Parent.PageID),
		Action:       action,
		CreatedAt:    p.CreatedTime,
		LastEditedAt: p.LastEditedTime,
	}
	metaJSON, _ := json.Marshal(meta)

	return models.Activity{
		Source:    models.SourceNotion,
		SourceID:  p.ID,
		Type:      models.TypeDocument,
		Title:     title,
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}, true
}

// pageTitle pulls the title out of a page's properties. The title lives under
// whichever property has type "title" — its name is "title" for a standalone
// page but arbitrary (often "Name") for a row in a database, so we match on the
// type rather than the key.
func pageTitle(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var props map[string]struct {
		Type  string `json:"type"`
		Title []struct {
			PlainText string `json:"plain_text"`
		} `json:"title"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return ""
	}
	for _, p := range props {
		if p.Type != "title" {
			continue
		}
		var b strings.Builder
		for _, t := range p.Title {
			b.WriteString(t.PlainText)
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			return s
		}
	}
	return ""
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
