package github

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
	"github.com/pavelpilyak/devrecall/internal/collector/ticketlink"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

const (
	defaultBaseURL = "https://api.github.com"
	perPage        = 100
)

// Collector fetches PRs, reviews, and issues from the GitHub API.
type Collector struct {
	token    string
	username string
	baseURL  string
	client   *http.Client
}

// New creates a GitHub collector with the default API base URL.
func New(token, username string) *Collector {
	return &Collector{
		token:    token,
		username: username,
		baseURL:  defaultBaseURL,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient creates a collector with a custom HTTP client and base URL (for testing).
func NewWithClient(token, username, baseURL string, client *http.Client) *Collector {
	return &Collector{
		token:    token,
		username: username,
		baseURL:  baseURL,
		client:   client,
	}
}

func (c *Collector) Name() models.Source {
	return models.SourceGitHub
}

// Collect fetches GitHub activity from the last 24 hours.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().Add(-24*time.Hour))
}

// CollectSince fetches PRs authored, PRs reviewed, review comments, and issues since the given time.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	var all []models.Activity

	prs, err := c.collectPRsAuthored(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("collecting authored PRs: %w", err)
	}
	all = append(all, prs...)

	reviews, err := c.collectPRsReviewed(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("collecting PR reviews: %w", err)
	}
	all = append(all, reviews...)

	issues, err := c.collectIssues(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("collecting issues: %w", err)
	}
	all = append(all, issues...)

	return all, nil
}

// --- PRs authored ---

type pullRequest struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	MergedAt  *string   `json:"merged_at"`
	HTMLURL   string    `json:"html_url"`
	User      ghUser    `json:"user"`
	Base      ghRef     `json:"base"`
	Head      ghRef     `json:"head"`
	Comments  int       `json:"comments"`
	Commits   int       `json:"commits"`
	Additions int       `json:"additions"`
	Deletions int       `json:"deletions"`
	Reviewers []ghUser  `json:"requested_reviewers"`
}

type ghUser struct {
	Login string `json:"login"`
}

type ghRef struct {
	Repo ghRepo `json:"repo"`
}

type ghRepo struct {
	FullName string `json:"full_name"`
}

type searchResult[T any] struct {
	TotalCount int  `json:"total_count"`
	Items      []T  `json:"items"`
}

type prMeta struct {
	Repo          string   `json:"repo"`
	PRNumber      int      `json:"pr_number"`
	State         string   `json:"state"`
	Reviewers     []string `json:"reviewers,omitempty"`
	CommentsCount int      `json:"comments_count"`
	CommitsCount  int      `json:"commits_count"`
	Additions     int      `json:"additions"`
	Deletions     int      `json:"deletions"`
	CommitSHAs    []string `json:"commit_shas,omitempty"`
	IssueKeys     []string `json:"issue_keys,omitempty"`
	URL           string   `json:"url"`
}

type ghCommit struct {
	SHA string `json:"sha"`
}

func (c *Collector) collectPRsAuthored(ctx context.Context, since time.Time) ([]models.Activity, error) {
	query := fmt.Sprintf("type:pr author:%s updated:>=%s", c.username, since.Format("2006-01-02"))
	items, err := c.searchIssues(ctx, query)
	if err != nil {
		return nil, err
	}

	var activities []models.Activity
	for _, item := range items {
		repo := repoFromURL(item.RepositoryURL)

		// Fetch full PR details for additions/deletions/commits.
		pr, err := c.getPR(ctx, repo, item.Number)
		if err != nil {
			// Non-fatal: use search data only.
			pr = &pullRequest{
				Number:  item.Number,
				Title:   item.Title,
				State:   item.State,
				HTMLURL: item.HTMLURL,
			}
		}

		state := pr.State
		if pr.MergedAt != nil {
			state = "merged"
		}

		var reviewerNames []string
		for _, r := range pr.Reviewers {
			reviewerNames = append(reviewerNames, r.Login)
		}

		// Fetch commit SHAs for PR↔commit linking.
		var commitSHAs []string
		if commits, err := c.getPRCommits(ctx, repo, pr.Number); err == nil {
			for _, commit := range commits {
				commitSHAs = append(commitSHAs, commit.SHA)
			}
		}

		meta := prMeta{
			Repo:          repo,
			PRNumber:      pr.Number,
			State:         state,
			Reviewers:     reviewerNames,
			CommentsCount: pr.Comments,
			CommitsCount:  pr.Commits,
			Additions:     pr.Additions,
			Deletions:     pr.Deletions,
			CommitSHAs:    commitSHAs,
			IssueKeys:     ticketlink.ExtractFromMessage(pr.Title + "\n" + pr.Body),
			URL:           pr.HTMLURL,
		}
		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:    models.SourceGitHub,
			SourceID:  fmt.Sprintf("github:%s:pr:%d", repo, pr.Number),
			Type:      models.TypePullRequest,
			Title:     pr.Title,
			Content:   pr.Body,
			Metadata:  string(metaJSON),
			Timestamp: item.UpdatedAt,
		})
	}

	return activities, nil
}

// --- PR reviews ---

type reviewMeta struct {
	Repo      string   `json:"repo"`
	PRNumber  int      `json:"pr_number"`
	PRTitle   string   `json:"pr_title"`
	State     string   `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED
	IssueKeys []string `json:"issue_keys,omitempty"`
	URL       string   `json:"url"`
}

type ghReview struct {
	ID          int       `json:"id"`
	User        ghUser    `json:"user"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
	HTMLURL     string    `json:"html_url"`
}

func (c *Collector) collectPRsReviewed(ctx context.Context, since time.Time) ([]models.Activity, error) {
	// Search for PRs where user is a reviewer (reviewed-by).
	query := fmt.Sprintf("type:pr reviewed-by:%s updated:>=%s", c.username, since.Format("2006-01-02"))
	items, err := c.searchIssues(ctx, query)
	if err != nil {
		return nil, err
	}

	var activities []models.Activity
	seen := make(map[string]bool)

	for _, item := range items {
		// Skip PRs authored by self (already captured).
		if item.User.Login == c.username {
			continue
		}

		repo := repoFromURL(item.RepositoryURL)

		// Fetch reviews to find our specific review.
		reviews, err := c.getReviews(ctx, repo, item.Number)
		if err != nil {
			continue
		}

		for _, review := range reviews {
			if review.User.Login != c.username {
				continue
			}
			if review.SubmittedAt.Before(since) {
				continue
			}

			key := fmt.Sprintf("github:%s:review:%d", repo, review.ID)
			if seen[key] {
				continue
			}
			seen[key] = true

			meta := reviewMeta{
				Repo:      repo,
				PRNumber:  item.Number,
				PRTitle:   item.Title,
				State:     review.State,
				IssueKeys: ticketlink.ExtractFromMessage(item.Title + "\n" + item.Body),
				URL:       review.HTMLURL,
			}
			metaJSON, _ := json.Marshal(meta)

			title := fmt.Sprintf("Reviewed PR #%d: %s", item.Number, item.Title)
			if review.State == "APPROVED" {
				title = fmt.Sprintf("Approved PR #%d: %s", item.Number, item.Title)
			} else if review.State == "CHANGES_REQUESTED" {
				title = fmt.Sprintf("Requested changes on PR #%d: %s", item.Number, item.Title)
			}

			activities = append(activities, models.Activity{
				Source:    models.SourceGitHub,
				SourceID:  key,
				Type:      models.TypeReview,
				Title:     title,
				Metadata:  string(metaJSON),
				Timestamp: review.SubmittedAt,
			})
		}
	}

	return activities, nil
}

// --- Issues ---

type searchIssueItem struct {
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	State         string    `json:"state"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	HTMLURL       string    `json:"html_url"`
	RepositoryURL string    `json:"repository_url"`
	User          ghUser    `json:"user"`
	Labels        []ghLabel `json:"labels"`
	Assignee      *ghUser   `json:"assignee"`
	PullRequest   *struct{} `json:"pull_request"` // non-nil if this is a PR
}

type ghLabel struct {
	Name string `json:"name"`
}

type issueMeta struct {
	Repo      string   `json:"repo"`
	Number    int      `json:"number"`
	State     string   `json:"state"`
	Labels    []string `json:"labels,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	IssueKeys []string `json:"issue_keys,omitempty"`
	URL       string   `json:"url"`
}

func (c *Collector) collectIssues(ctx context.Context, since time.Time) ([]models.Activity, error) {
	// Issues authored or assigned to user.
	query := fmt.Sprintf("type:issue involves:%s updated:>=%s", c.username, since.Format("2006-01-02"))
	items, err := c.searchIssues(ctx, query)
	if err != nil {
		return nil, err
	}

	var activities []models.Activity
	for _, item := range items {
		// Skip PRs that appear in issue search results.
		if item.PullRequest != nil {
			continue
		}

		repo := repoFromURL(item.RepositoryURL)

		var labels []string
		for _, l := range item.Labels {
			labels = append(labels, l.Name)
		}

		assignee := ""
		if item.Assignee != nil {
			assignee = item.Assignee.Login
		}

		meta := issueMeta{
			Repo:      repo,
			Number:    item.Number,
			State:     item.State,
			Labels:    labels,
			Assignee:  assignee,
			IssueKeys: ticketlink.ExtractFromMessage(item.Title + "\n" + item.Body),
			URL:       item.HTMLURL,
		}
		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:    models.SourceGitHub,
			SourceID:  fmt.Sprintf("github:%s:issue:%d", repo, item.Number),
			Type:      models.TypeIssue,
			Title:     item.Title,
			Content:   item.Body,
			Metadata:  string(metaJSON),
			Timestamp: item.UpdatedAt,
		})
	}

	return activities, nil
}

// --- API helpers ---

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
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("github request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github %s returned %d: %s", path, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

func (c *Collector) searchIssues(ctx context.Context, query string) ([]searchIssueItem, error) {
	var all []searchIssueItem
	page := 1

	for {
		params := url.Values{
			"q":        {query},
			"per_page": {strconv.Itoa(perPage)},
			"page":     {strconv.Itoa(page)},
			"sort":     {"updated"},
			"order":    {"desc"},
		}

		var result searchResult[searchIssueItem]
		if err := c.apiGet(ctx, "/search/issues", params, &result); err != nil {
			return nil, err
		}

		all = append(all, result.Items...)

		if len(all) >= result.TotalCount || len(result.Items) < perPage {
			break
		}
		page++
	}

	return all, nil
}

func (c *Collector) getPR(ctx context.Context, repo string, number int) (*pullRequest, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	var pr pullRequest
	if err := c.apiGet(ctx, path, nil, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (c *Collector) getPRCommits(ctx context.Context, repo string, prNumber int) ([]ghCommit, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d/commits", repo, prNumber)
	params := url.Values{"per_page": {strconv.Itoa(perPage)}}
	var commits []ghCommit
	if err := c.apiGet(ctx, path, params, &commits); err != nil {
		return nil, err
	}
	return commits, nil
}

func (c *Collector) getReviews(ctx context.Context, repo string, prNumber int) ([]ghReview, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, prNumber)
	var reviews []ghReview
	if err := c.apiGet(ctx, path, nil, &reviews); err != nil {
		return nil, err
	}
	return reviews, nil
}

// repoFromURL extracts "owner/repo" from a GitHub API repository URL.
// e.g., "https://api.github.com/repos/octocat/hello-world" → "octocat/hello-world"
func repoFromURL(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil {
		return repoURL
	}
	// Path is "/repos/owner/repo"
	parts := splitPath(u.Path)
	if len(parts) >= 3 && parts[0] == "repos" {
		return parts[1] + "/" + parts[2]
	}
	return repoURL
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range split(path, '/') {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func split(s string, sep byte) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}
