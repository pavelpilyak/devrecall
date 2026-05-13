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
	// maxPaginationPages caps the number of /search pages we walk to protect
	// against Atlassian's offset-pagination cap (~10k results) and any API
	// edge case where Size keeps reporting full but content doesn't advance.
	maxPaginationPages = 100
)

// Collector fetches Confluence pages created or edited by the user.
type Collector struct {
	token     string // OAuth access token or API token
	email     string // for API token auth (Basic Auth)
	cloudID   string // Atlassian cloud site ID
	baseURL   string // API base URL
	accountID string // cached after /myself call
	linkBase  string // workspace URL captured from search response _links.base, used to build page URLs
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

// CollectSince fetches pages and comments the user created or edited since the given time.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	if err := c.ensureAccountID(ctx); err != nil {
		return nil, fmt.Errorf("resolving account ID: %w", err)
	}

	pages, err := c.searchPages(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("searching pages: %w", err)
	}

	comments, err := c.searchComments(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("searching comments: %w", err)
	}

	activities := make([]models.Activity, 0, len(pages)+len(comments))
	for _, page := range pages {
		activities = append(activities, c.pageToActivity(page))
	}
	for _, comment := range comments {
		activities = append(activities, c.commentToActivity(comment))
	}

	return activities, nil
}

// --- Confluence API types ---

type searchResult struct {
	Results []contentResult `json:"results"`
	Start   int             `json:"start"`
	Limit   int             `json:"limit"`
	Size    int             `json:"size"`
	// Atlassian returns the workspace base URL here (e.g.
	// https://acme.atlassian.net/wiki) — per-page _links only carry the
	// relative webui path, so we need this to build absolute URLs.
	Links contentLinks `json:"_links,omitempty"`
}

type contentResult struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"` // "page", "blogpost", or "comment"
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	Space     contentSpace      `json:"space,omitempty"`
	History   contentHistory    `json:"history,omitempty"`
	Links     contentLinks      `json:"_links,omitempty"`
	Container *contentContainer `json:"container,omitempty"` // parent for comments
}

// contentContainer is a slim view of a comment's parent page/blogpost.
// Defined separately from contentResult to avoid recursive JSON types.
type contentContainer struct {
	ID    string       `json:"id"`
	Type  string       `json:"type"`
	Title string       `json:"title"`
	Space contentSpace `json:"space,omitempty"`
	Links contentLinks `json:"_links,omitempty"`
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
	PageID      string `json:"page_id"`                // for comments: the parent page's ID
	CommentID   string `json:"comment_id,omitempty"`   // comments only
	SpaceKey    string `json:"space_key"`
	SpaceName   string `json:"space_name"`
	PageType    string `json:"page_type"`              // "page", "blogpost", or "comment"
	Action      string `json:"action"`                 // "created", "updated", or "commented"
	URL         string `json:"url"`
	ParentTitle string `json:"parent_title,omitempty"` // comments only
	ParentType  string `json:"parent_type,omitempty"`  // comments only ("page" or "blogpost")
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
	// CQL: pages/blogposts the current user contributed to within the window.
	// `contributor` is server-side filtered so we avoid downloading the entire
	// workspace's recent activity just to drop it client-side. On large
	// instances this turns a tens-of-minutes scan into a handful of requests.
	// We still defensively re-check on the client in case a result slips past.
	cql := fmt.Sprintf(
		"type in (page,blogpost) AND contributor = currentUser() AND lastmodified >= \"%s\" ORDER BY lastmodified DESC",
		since.Format("2006-01-02"),
	)

	var all []contentResult
	start := 0

	for page := 0; page < maxPaginationPages; page++ {
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
		if c.linkBase == "" && result.Links.Base != "" {
			c.linkBase = result.Links.Base
		}

		for _, p := range result.Results {
			if c.isUserPage(p) {
				all = append(all, p)
			}
		}

		if result.Size < perPage {
			break
		}
		start += result.Size
	}

	return all, nil
}

func (c *Collector) searchComments(ctx context.Context, since time.Time) ([]contentResult, error) {
	// CQL: comments authored by the current user within the window.
	// Server-side filter via `creator` keeps the response size tiny even on
	// busy instances where tens of thousands of comments are written per week.
	cql := fmt.Sprintf(
		"type = comment AND creator = currentUser() AND lastmodified >= \"%s\" ORDER BY lastmodified DESC",
		since.Format("2006-01-02"),
	)

	var all []contentResult
	start := 0

	for page := 0; page < maxPaginationPages; page++ {
		params := url.Values{
			"cql":    {cql},
			"expand": {"history,history.lastUpdated,space,container"},
			"limit":  {strconv.Itoa(perPage)},
			"start":  {strconv.Itoa(start)},
		}

		var result searchResult
		if err := c.apiGet(ctx, "/rest/api/content/search", params, &result); err != nil {
			return nil, err
		}
		if c.linkBase == "" && result.Links.Base != "" {
			c.linkBase = result.Links.Base
		}

		for _, comment := range result.Results {
			if c.isUserComment(comment) {
				all = append(all, comment)
			}
		}

		if result.Size < perPage {
			break
		}
		start += result.Size
	}

	return all, nil
}

// isUserComment returns true when the current user authored this comment.
// Edits to others' comments are not counted (Confluence doesn't really support that anyway).
func (c *Collector) isUserComment(comment contentResult) bool {
	return comment.History.CreatedBy.AccountID == c.accountID
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

func (c *Collector) commentToActivity(comment contentResult) models.Activity {
	ts := c.parseTimestamp(comment)

	parentTitle := ""
	parentType := ""
	parentID := ""
	space := comment.Space
	commentURL := c.pageURL(comment)
	if comment.Container != nil {
		parentTitle = comment.Container.Title
		parentType = comment.Container.Type
		parentID = comment.Container.ID
		if space.Key == "" {
			space = comment.Container.Space
		}
		// Comment's own _links may be empty; prefer the container's URL for navigation.
		if commentURL == "" && comment.Container.Links.Base != "" && comment.Container.Links.WebUI != "" {
			commentURL = comment.Container.Links.Base + comment.Container.Links.WebUI
		}
	}

	meta := pageMeta{
		PageID:      parentID,
		CommentID:   comment.ID,
		SpaceKey:    space.Key,
		SpaceName:   space.Name,
		PageType:    "comment",
		Action:      "commented",
		URL:         commentURL,
		ParentTitle: parentTitle,
		ParentType:  parentType,
	}
	metaJSON, _ := json.Marshal(meta)

	parentLabel := parentType
	if parentLabel == "" {
		parentLabel = "page"
	}
	title := fmt.Sprintf("Commented on %s: %s", parentLabel, parentTitle)
	if parentTitle == "" {
		title = fmt.Sprintf("Commented on %s", parentLabel)
	}
	if space.Key != "" {
		if parentTitle != "" {
			title = fmt.Sprintf("Commented on %s in %s: %s", parentLabel, space.Key, parentTitle)
		} else {
			title = fmt.Sprintf("Commented on %s in %s", parentLabel, space.Key)
		}
	}

	return models.Activity{
		Source:    models.SourceConfluence,
		SourceID:  fmt.Sprintf("confluence:comment:%s", comment.ID),
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
	if page.Links.WebUI == "" {
		return ""
	}
	// Atlassian only returns _links.base at the top level of the search response;
	// per-page results carry just the relative webui path. Prefer per-page base
	// if present, fall back to the workspace base captured from the search response.
	if page.Links.Base != "" {
		return page.Links.Base + page.Links.WebUI
	}
	if c.linkBase != "" {
		return c.linkBase + page.Links.WebUI
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
