package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/pavelpiliak/devrecall/internal/collector/ratelimit"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

const (
	defaultCloudBaseURL = "https://api.atlassian.com"
	perPage             = 50
)

// Collector fetches Jira issues, status transitions, and comments.
type Collector struct {
	token    string // OAuth access token or API token
	email    string // for API token auth (Basic Auth)
	cloudID  string // Jira cloud site ID
	baseURL  string // e.g., "https://api.atlassian.com/ex/jira/{cloudId}" or self-hosted URL
	accountID string // cached after first /myself call
	client   *http.Client
	isCloud  bool // true = OAuth Bearer auth, false = API token Basic Auth
}

// New creates a Jira collector for cloud (OAuth) with the given cloud site ID.
func New(token, cloudID string) *Collector {
	return &Collector{
		token:   token,
		cloudID: cloudID,
		baseURL: fmt.Sprintf("%s/ex/jira/%s", defaultCloudBaseURL, cloudID),
		client:  &http.Client{Timeout: 30 * time.Second},
		isCloud: true,
	}
}

// NewWithAPIToken creates a Jira collector using API token authentication.
// baseURL should be the Jira instance URL (e.g., "https://mycompany.atlassian.net").
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
	return models.SourceJira
}

// Collect fetches Jira activity from the last 24 hours.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().Add(-24*time.Hour))
}

// CollectSince fetches assigned issues updated since the given time,
// then collects status transitions and comments for each.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	if err := c.ensureAccountID(ctx); err != nil {
		return nil, fmt.Errorf("resolving account ID: %w", err)
	}

	issues, err := c.searchIssues(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("searching issues: %w", err)
	}

	var all []models.Activity

	for _, issue := range issues {
		transitions, err := c.collectStatusTransitions(ctx, issue, since)
		if err != nil {
			// Non-fatal: skip changelog for this issue.
			transitions = nil
		}
		all = append(all, transitions...)

		comments, err := c.collectComments(ctx, issue, since)
		if err != nil {
			// Non-fatal: skip comments for this issue.
			comments = nil
		}
		all = append(all, comments...)

		// If no transitions or comments, emit a ticket activity for the issue itself.
		if len(transitions) == 0 && len(comments) == 0 {
			meta := ticketMeta{
				IssueKey:     issue.Key,
				IssueSummary: issue.Fields.Summary,
				Project:      issue.Fields.Project.Key,
				Status:       issue.Fields.Status.Name,
				Priority:     issue.Fields.Priority.Name,
				Labels:       issue.Fields.Labels,
				URL:          c.issueURL(issue.Key),
			}
			if issue.Fields.Sprint != nil {
				meta.Sprint = issue.Fields.Sprint.Name
			}
			metaJSON, _ := json.Marshal(meta)

			all = append(all, models.Activity{
				Source:    models.SourceJira,
				SourceID:  fmt.Sprintf("jira:%s", issue.Key),
				Type:      models.TypeTicket,
				Title:     fmt.Sprintf("%s: %s", issue.Key, issue.Fields.Summary),
				Metadata:  string(metaJSON),
				Timestamp: issue.Fields.Updated,
			})
		}
	}

	return all, nil
}

// --- Jira API types ---

type jiraSearchResult struct {
	StartAt    int          `json:"startAt"`
	MaxResults int          `json:"maxResults"`
	Total      int          `json:"total"`
	Issues     []jiraIssue  `json:"issues"`
}

type jiraIssue struct {
	ID     string      `json:"id"`
	Key    string      `json:"key"`
	Fields jiraFields  `json:"fields"`
}

type jiraFields struct {
	Summary  string       `json:"summary"`
	Status   jiraStatus   `json:"status"`
	Priority jiraPriority `json:"priority"`
	Labels   []string     `json:"labels"`
	Project  jiraProject  `json:"project"`
	Updated  time.Time    `json:"updated"`
	Created  time.Time    `json:"created"`
	Sprint   *jiraSprint  `json:"sprint"`
}

type jiraStatus struct {
	Name string `json:"name"`
}

type jiraPriority struct {
	Name string `json:"name"`
}

type jiraProject struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type jiraSprint struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type jiraChangelog struct {
	StartAt    int              `json:"startAt"`
	MaxResults int              `json:"maxResults"`
	Total      int              `json:"total"`
	Values     []jiraChangeItem `json:"values"`
}

type jiraChangeItem struct {
	ID      string              `json:"id"`
	Author  jiraChangeAuthor    `json:"author"`
	Created time.Time           `json:"created"`
	Items   []jiraChangeDetail  `json:"items"`
}

type jiraChangeAuthor struct {
	AccountID string `json:"accountId"`
}

type jiraChangeDetail struct {
	Field      string `json:"field"`
	FromString string `json:"fromString"`
	ToString   string `json:"toString"`
}

type jiraCommentResult struct {
	StartAt    int            `json:"startAt"`
	MaxResults int            `json:"maxResults"`
	Total      int            `json:"total"`
	Comments   []jiraComment  `json:"comments"`
}

type jiraComment struct {
	ID      string            `json:"id"`
	Author  jiraCommentAuthor `json:"author"`
	Body    any               `json:"body"` // ADF or string
	Created time.Time         `json:"created"`
	Updated time.Time         `json:"updated"`
}

type jiraCommentAuthor struct {
	AccountID string `json:"accountId"`
}

type jiraMyself struct {
	AccountID    string `json:"accountId"`
	EmailAddress string `json:"emailAddress"`
	DisplayName  string `json:"displayName"`
}

// --- Metadata types ---

type ticketMeta struct {
	IssueKey     string   `json:"issue_key"`
	IssueSummary string   `json:"issue_summary"`
	Project      string   `json:"project"`
	Status       string   `json:"status,omitempty"`
	Priority     string   `json:"priority,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	Sprint       string   `json:"sprint,omitempty"`
	FromStatus   string   `json:"from_status,omitempty"`
	ToStatus     string   `json:"to_status,omitempty"`
	URL          string   `json:"url"`
}

type commentMeta struct {
	IssueKey  string `json:"issue_key"`
	CommentID string `json:"comment_id"`
	URL       string `json:"url"`
}

// --- Collection methods ---

func (c *Collector) ensureAccountID(ctx context.Context) error {
	if c.accountID != "" {
		return nil
	}

	var myself jiraMyself
	if err := c.apiGet(ctx, "/rest/api/3/myself", nil, &myself); err != nil {
		return err
	}
	c.accountID = myself.AccountID
	return nil
}

func (c *Collector) searchIssues(ctx context.Context, since time.Time) ([]jiraIssue, error) {
	jql := fmt.Sprintf(
		"assignee = currentUser() AND updated >= \"%s\" ORDER BY updated DESC",
		since.Format("2006-01-02 15:04"),
	)

	var all []jiraIssue
	startAt := 0

	for {
		params := url.Values{
			"jql":        {jql},
			"fields":     {"summary,status,priority,labels,project,updated,created,sprint"},
			"maxResults": {strconv.Itoa(perPage)},
			"startAt":    {strconv.Itoa(startAt)},
		}

		var result jiraSearchResult
		if err := c.apiGet(ctx, "/rest/api/3/search", params, &result); err != nil {
			return nil, err
		}

		all = append(all, result.Issues...)

		if len(all) >= result.Total || len(result.Issues) < perPage {
			break
		}
		startAt += len(result.Issues)
	}

	return all, nil
}

func (c *Collector) collectStatusTransitions(ctx context.Context, issue jiraIssue, since time.Time) ([]models.Activity, error) {
	var changelog jiraChangelog
	params := url.Values{"maxResults": {"100"}}
	if err := c.apiGet(ctx, fmt.Sprintf("/rest/api/3/issue/%s/changelog", issue.Key), params, &changelog); err != nil {
		return nil, err
	}

	var activities []models.Activity

	for _, change := range changelog.Values {
		if change.Created.Before(since) {
			continue
		}
		// Only include our own transitions.
		if change.Author.AccountID != c.accountID {
			continue
		}

		for _, item := range change.Items {
			if item.Field != "status" {
				continue
			}

			meta := ticketMeta{
				IssueKey:     issue.Key,
				IssueSummary: issue.Fields.Summary,
				Project:      issue.Fields.Project.Key,
				Priority:     issue.Fields.Priority.Name,
				Labels:       issue.Fields.Labels,
				FromStatus:   item.FromString,
				ToStatus:     item.ToString,
				URL:          c.issueURL(issue.Key),
			}
			if issue.Fields.Sprint != nil {
				meta.Sprint = issue.Fields.Sprint.Name
			}
			metaJSON, _ := json.Marshal(meta)

			activities = append(activities, models.Activity{
				Source:   models.SourceJira,
				SourceID: fmt.Sprintf("jira:%s:changelog:%s", issue.Key, change.ID),
				Type:     models.TypeTicket,
				Title: fmt.Sprintf("%s: %s → moved to %s",
					issue.Key, issue.Fields.Summary, item.ToString),
				Content: fmt.Sprintf("Moved %s from '%s' to '%s'",
					issue.Key, item.FromString, item.ToString),
				Metadata:  string(metaJSON),
				Timestamp: change.Created,
			})
		}
	}

	return activities, nil
}

func (c *Collector) collectComments(ctx context.Context, issue jiraIssue, since time.Time) ([]models.Activity, error) {
	var result jiraCommentResult
	if err := c.apiGet(ctx, fmt.Sprintf("/rest/api/3/issue/%s/comment", issue.Key), nil, &result); err != nil {
		return nil, err
	}

	var activities []models.Activity

	for _, comment := range result.Comments {
		if comment.Created.Before(since) {
			continue
		}
		// Only include our own comments.
		if comment.Author.AccountID != c.accountID {
			continue
		}

		bodyText := extractCommentText(comment.Body)

		meta := commentMeta{
			IssueKey:  issue.Key,
			CommentID: comment.ID,
			URL:       c.issueURL(issue.Key),
		}
		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:    models.SourceJira,
			SourceID:  fmt.Sprintf("jira:%s:comment:%s", issue.Key, comment.ID),
			Type:      models.TypeTicket,
			Title:     fmt.Sprintf("Commented on %s: %s", issue.Key, issue.Fields.Summary),
			Content:   bodyText,
			Metadata:  string(metaJSON),
			Timestamp: comment.Created,
		})
	}

	return activities, nil
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
		return fmt.Errorf("jira request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jira %s returned %d: %s", path, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

func (c *Collector) issueURL(key string) string {
	// For cloud via API gateway, construct the web URL from the cloud ID.
	// For self-hosted, baseURL is already the web URL.
	if c.isCloud && c.cloudID != "" {
		return fmt.Sprintf("https://id.atlassian.net/browse/%s", key)
	}
	return fmt.Sprintf("%s/browse/%s", c.baseURL, key)
}

// extractCommentText extracts plain text from Jira's ADF (Atlassian Document Format) or plain string.
func extractCommentText(body any) string {
	switch v := body.(type) {
	case string:
		return v
	case map[string]any:
		return extractADFText(v)
	default:
		return ""
	}
}

// extractADFText recursively extracts text from ADF content nodes.
func extractADFText(node map[string]any) string {
	if text, ok := node["text"].(string); ok {
		return text
	}

	content, ok := node["content"].([]any)
	if !ok {
		return ""
	}

	nodeType, _ := node["type"].(string)
	isBlock := nodeType == "doc" || nodeType == "blockquote" || nodeType == "listItem"

	var parts []string
	for _, child := range content {
		if childMap, ok := child.(map[string]any); ok {
			text := extractADFText(childMap)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}

	if isBlock {
		// Block nodes separate children with spaces.
		result := ""
		for _, p := range parts {
			if result != "" {
				result += " "
			}
			result += p
		}
		return result
	}

	// Inline nodes (e.g., text within a paragraph) are concatenated directly.
	result := ""
	for _, p := range parts {
		result += p
	}
	return result
}
