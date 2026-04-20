package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/pavelpilyak/devrecall/internal/collector/ratelimit"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

const (
	defaultCloudBaseURL = "https://api.atlassian.com"
	perPage             = 25
)

// Collector fetches Confluence pages created or edited by the user.
type Collector struct {
	token     string // OAuth access token or API token
	email     string // for API token auth (Basic Auth)
	cloudID   string // Atlassian cloud site ID
	baseURL   string // API base URL
	accountID string // cached after /myself call
	client    *http.Client
	isCloud   bool
}

// New creates a Confluence collector for cloud (OAuth) with the given cloud site ID.
func New(token, cloudID string) *Collector {
	return &Collector{
		token:   token,
		cloudID: cloudID,
		baseURL: fmt.Sprintf("%s/wiki", defaultCloudBaseURL),
		client:  &http.Client{Timeout: 30 * time.Second},
		isCloud: true,
	}
}

// NewWithAPIToken creates a Confluence collector using API token authentication.
// baseURL should be the Confluence instance URL (e.g., "https://mycompany.atlassian.net/wiki").
func NewWithAPIToken(email, apiToken, baseURL string) *Collector {
	return &Collector{
		token:   apiToken,
		email:   email,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
		isCloud: false,
	}
}

// NewWithClient creates a collector with a custom HTTP client and base URL (for testing).
func NewWithClient(token, baseURL string, isCloud bool, client *http.Client) *Collector {
	return &Collector{
		token:   token,
		baseURL: baseURL,
		client:  client,
		isCloud: isCloud,
	}
}

func (c *Collector) Name() models.Source {
	return models.SourceConfluence
}

// Collect fetches Confluence pages modified in the last 7 days.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().Add(-7*24*time.Hour))
}

// CollectSince fetches pages the user created or edited since the given time.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	if err := c.ensureAccountID(ctx); err != nil {
		return nil, fmt.Errorf("resolving account ID: %w", err)
	}

	pages, err := c.searchPages(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("searching pages: %w", err)
	}

	var activities []models.Activity
	for _, page := range pages {
		a := c.pageToActivity(page)
		activities = append(activities, a)
	}

	return activities, nil
}

// --- Confluence API types ---

type searchResult struct {
	Results []contentResult `json:"results"`
	Start   int             `json:"start"`
	Limit   int             `json:"limit"`
	Size    int             `json:"size"`
}

type contentResult struct {
	ID      string        `json:"id"`
	Type    string        `json:"type"` // "page" or "blogpost"
	Title   string        `json:"title"`
	Status  string        `json:"status"`
	Space   contentSpace  `json:"space,omitempty"`
	History contentHistory `json:"history,omitempty"`
	Links   contentLinks  `json:"_links,omitempty"`
}

type contentSpace struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type contentHistory struct {
	CreatedDate  string        `json:"createdDate,omitempty"`
	CreatedBy    contentUser   `json:"createdBy,omitempty"`
	LastUpdated  lastUpdated   `json:"lastUpdated,omitempty"`
}

type lastUpdated struct {
	When string      `json:"when,omitempty"`
	By   contentUser `json:"by,omitempty"`
}

type contentUser struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

type contentLinks struct {
	WebUI string `json:"webui"`
	Base  string `json:"base"`
}

type myselfResult struct {
	AccountID string `json:"accountId"`
}

// --- Metadata ---

type pageMeta struct {
	PageID    string `json:"page_id"`
	SpaceKey  string `json:"space_key"`
	SpaceName string `json:"space_name"`
	PageType  string `json:"page_type"` // "page" or "blogpost"
	Action    string `json:"action"`    // "created" or "updated"
	URL       string `json:"url"`
}

// --- Collection methods ---

func (c *Collector) ensureAccountID(ctx context.Context) error {
	if c.accountID != "" {
		return nil
	}

	// Confluence Cloud uses the same Atlassian identity API.
	// For cloud, /wiki/rest/api/user/current works; for server, too.
	var myself myselfResult
	if err := c.apiGet(ctx, "/rest/api/user/current", nil, &myself); err != nil {
		return err
	}
	c.accountID = myself.AccountID
	return nil
}

func (c *Collector) searchPages(ctx context.Context, since time.Time) ([]contentResult, error) {
	// CQL: search for pages modified by the current user since the given time.
	cql := fmt.Sprintf(
		"type in (page,blogpost) AND lastmodified >= \"%s\" ORDER BY lastmodified DESC",
		since.Format("2006-01-02"),
	)

	var all []contentResult
	start := 0

	for {
		params := url.Values{
			"cql":    {cql},
			"expand": {"history,history.lastUpdated,space"},
			"limit":  {strconv.Itoa(perPage)},
			"start":  {strconv.Itoa(start)},
		}

		var result searchResult
		if err := c.apiGet(ctx, "/rest/api/content/search", params, &result); err != nil {
			return nil, err
		}

		// Filter to only pages the current user created or last edited.
		for _, page := range result.Results {
			if c.isUserPage(page) {
				all = append(all, page)
			}
		}

		if result.Size < perPage {
			break
		}
		start += result.Size
	}

	return all, nil
}

// isUserPage checks whether the current user created or last edited this page.
func (c *Collector) isUserPage(page contentResult) bool {
	if page.History.CreatedBy.AccountID == c.accountID {
		return true
	}
	if page.History.LastUpdated.By.AccountID == c.accountID {
		return true
	}
	return false
}

func (c *Collector) pageToActivity(page contentResult) models.Activity {
	action := "updated"
	if page.History.CreatedBy.AccountID == c.accountID {
		action = "created"
	}

	ts := c.parseTimestamp(page)
	pageURL := c.pageURL(page)

	meta := pageMeta{
		PageID:    page.ID,
		SpaceKey:  page.Space.Key,
		SpaceName: page.Space.Name,
		PageType:  page.Type,
		Action:    action,
		URL:       pageURL,
	}
	metaJSON, _ := json.Marshal(meta)

	title := fmt.Sprintf("%s %s: %s", capitalize(action), page.Type, page.Title)
	if page.Space.Key != "" {
		title = fmt.Sprintf("%s %s in %s: %s", capitalize(action), page.Type, page.Space.Key, page.Title)
	}

	return models.Activity{
		Source:    models.SourceConfluence,
		SourceID:  fmt.Sprintf("confluence:%s:%s", page.ID, action),
		Type:      models.TypeDocument,
		Title:     title,
		Metadata:  string(metaJSON),
		Timestamp: ts,
	}
}

func (c *Collector) parseTimestamp(page contentResult) time.Time {
	// Prefer last updated time; fall back to created date.
	if when := page.History.LastUpdated.When; when != "" {
		if t, err := time.Parse(time.RFC3339, when); err == nil {
			return t
		}
		// Confluence also uses "2006-01-02T15:04:05.000Z" sometimes.
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", when); err == nil {
			return t
		}
	}
	if created := page.History.CreatedDate; created != "" {
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", created); err == nil {
			return t
		}
	}
	return time.Now()
}

func (c *Collector) pageURL(page contentResult) string {
	if page.Links.Base != "" && page.Links.WebUI != "" {
		return page.Links.Base + page.Links.WebUI
	}
	// Construct from cloud ID if available.
	if c.isCloud && c.cloudID != "" {
		return fmt.Sprintf("https://id.atlassian.net/wiki/spaces/%s/pages/%s", page.Space.Key, page.ID)
	}
	return ""
}

// --- API helper ---

func (c *Collector) apiGet(ctx context.Context, path string, params url.Values, dst any) error {
	reqURL := c.baseURL + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	resp, err := ratelimit.Do(ctx, c.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		if c.isCloud {
			req.Header.Set("Authorization", "Bearer "+c.token)
		} else {
			req.SetBasicAuth(c.email, c.token)
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("confluence request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("confluence %s returned %d: %s", path, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}
