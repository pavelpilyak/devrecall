package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/pavelpilyak/devrecall/internal/collector/ratelimit"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

const (
	defaultBaseURL = "https://api.bitbucket.org"
)

// Collector fetches PRs authored and PRs reviewed from the Bitbucket API.
//
// username is the Basic Auth principal (email for scoped API tokens, nickname for legacy
// app passwords). userUUID is the Bitbucket account UUID used to match the authenticated
// user against PR author/reviewer payloads — it is the only identifier that works across
// both auth modes.
type Collector struct {
	username    string
	appPassword string
	userUUID    string
	workspace   string
	baseURL     string
	client      *http.Client
}

// New creates a Bitbucket collector with the default API base URL.
func New(username, appPassword, userUUID, workspace string) *Collector {
	return &Collector{
		username:    username,
		appPassword: appPassword,
		userUUID:    userUUID,
		workspace:   workspace,
		baseURL:     defaultBaseURL,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient creates a collector with a custom HTTP client and base URL (for testing).
func NewWithClient(username, appPassword, userUUID, workspace, baseURL string, client *http.Client) *Collector {
	return &Collector{
		username:    username,
		appPassword: appPassword,
		userUUID:    userUUID,
		workspace:   workspace,
		baseURL:     baseURL,
		client:      client,
	}
}

func (c *Collector) Name() models.Source {
	return models.SourceBitbucket
}

// Collect fetches Bitbucket activity from the last 24 hours.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().Add(-24*time.Hour))
}

// CollectSince fetches PRs authored and PRs reviewed since the given time.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	repos, err := c.getActiveRepos(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("fetching repos: %w", err)
	}

	var all []models.Activity

	for _, repo := range repos {
		prs, err := c.collectPRs(ctx, repo, since)
		if err != nil {
			return nil, fmt.Errorf("collecting PRs for %s: %w", repo.FullName, err)
		}
		all = append(all, prs...)
	}

	return all, nil
}

// --- API types ---

type bbPaginated[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"` // URL for next page, empty if last
}

type bbRepo struct {
	FullName string `json:"full_name"` // "workspace/repo-slug"
	Slug     string `json:"slug"`
}

type bbPullRequest struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"` // OPEN, MERGED, DECLINED, SUPERSEDED
	CreatedOn   time.Time `json:"created_on"`
	UpdatedOn   time.Time `json:"updated_on"`
	Author      bbUser    `json:"author"`
	Reviewers   []bbUser  `json:"reviewers"`
	CommentCount int      `json:"comment_count"`
	Links       bbLinks   `json:"links"`
}

type bbUser struct {
	DisplayName string `json:"display_name"`
	UUID        string `json:"uuid"`
	Nickname    string `json:"nickname"`
}

type bbLinks struct {
	HTML bbHref `json:"html"`
}

type bbHref struct {
	Href string `json:"href"`
}

type bbParticipant struct {
	User     bbUser `json:"user"`
	Role     string `json:"role"` // PARTICIPANT, REVIEWER
	Approved bool   `json:"approved"`
}

// --- Metadata types ---

type bbCommit struct {
	Hash string `json:"hash"`
}

type prMeta struct {
	Repo          string   `json:"repo"`
	PRNumber      int      `json:"pr_number"`
	State         string   `json:"state"`
	Reviewers     []string `json:"reviewers,omitempty"`
	CommentsCount int      `json:"comments_count"`
	CommitSHAs    []string `json:"commit_shas,omitempty"`
	URL           string   `json:"url"`
}

type reviewMeta struct {
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
	PRTitle  string `json:"pr_title"`
	Approved bool   `json:"approved"`
	URL      string `json:"url"`
}

// --- Collection methods ---

func (c *Collector) getActiveRepos(ctx context.Context, since time.Time) ([]bbRepo, error) {
	// Bitbucket doesn't have a last_activity_after filter, so we fetch recent repos sorted by updated_on.
	path := fmt.Sprintf("/2.0/repositories/%s", url.PathEscape(c.workspace))
	params := url.Values{
		"role": {"member"},
		"sort": {"-updated_on"},
	}

	var all []bbRepo
	nextURL := ""

	for {
		var page bbPaginated[bbRepo]
		if nextURL != "" {
			if err := c.apiGetURL(ctx, nextURL, &page); err != nil {
				return nil, err
			}
		} else {
			if err := c.apiGet(ctx, path, params, &page); err != nil {
				return nil, err
			}
		}

		for _, repo := range page.Values {
			all = append(all, repo)
		}

		if page.Next == "" || len(all) >= 100 {
			break
		}
		nextURL = page.Next
	}

	return all, nil
}

func (c *Collector) collectPRs(ctx context.Context, repo bbRepo, since time.Time) ([]models.Activity, error) {
	path := fmt.Sprintf("/2.0/repositories/%s/pullrequests", repo.FullName)
	params := url.Values{
		"state": {"OPEN", "MERGED"},
	}

	var allPRs []bbPullRequest
	nextURL := ""

	for {
		var page bbPaginated[bbPullRequest]
		if nextURL != "" {
			if err := c.apiGetURL(ctx, nextURL, &page); err != nil {
				return nil, err
			}
		} else {
			if err := c.apiGet(ctx, path, params, &page); err != nil {
				return nil, err
			}
		}

		for _, pr := range page.Values {
			if pr.UpdatedOn.Before(since) {
				// PRs are sorted by updated_on desc; stop when we pass the since threshold.
				return c.prsToActivities(ctx, allPRs, repo), nil
			}
			allPRs = append(allPRs, pr)
		}

		if page.Next == "" {
			break
		}
		nextURL = page.Next
	}

	return c.prsToActivities(ctx, allPRs, repo), nil
}

func (c *Collector) prsToActivities(ctx context.Context, prs []bbPullRequest, repo bbRepo) []models.Activity {
	var activities []models.Activity

	for _, pr := range prs {
		isAuthor := c.matchesUser(pr.Author)
		isReviewer := false
		for _, r := range pr.Reviewers {
			if c.matchesUser(r) {
				isReviewer = true
				break
			}
		}

		if isAuthor {
			var reviewerNames []string
			for _, r := range pr.Reviewers {
				reviewerNames = append(reviewerNames, r.DisplayName)
			}

			// Fetch commit SHAs for PR↔commit linking.
			var commitSHAs []string
			if commits, err := c.getPRCommits(ctx, repo.FullName, pr.ID); err == nil {
				for _, commit := range commits {
					commitSHAs = append(commitSHAs, commit.Hash)
				}
			}

			meta := prMeta{
				Repo:          repo.FullName,
				PRNumber:      pr.ID,
				State:         pr.State,
				Reviewers:     reviewerNames,
				CommentsCount: pr.CommentCount,
				CommitSHAs:    commitSHAs,
				URL:           pr.Links.HTML.Href,
			}
			metaJSON, _ := json.Marshal(meta)

			activities = append(activities, models.Activity{
				Source:    models.SourceBitbucket,
				SourceID:  fmt.Sprintf("bitbucket:%s:pr:%d", repo.FullName, pr.ID),
				Type:      models.TypePullRequest,
				Title:     pr.Title,
				Content:   pr.Description,
				Metadata:  string(metaJSON),
				Timestamp: pr.UpdatedOn,
			})
		}

		if isReviewer && !isAuthor {
			meta := reviewMeta{
				Repo:     repo.FullName,
				PRNumber: pr.ID,
				PRTitle:  pr.Title,
				URL:      pr.Links.HTML.Href,
			}
			metaJSON, _ := json.Marshal(meta)

			title := fmt.Sprintf("Reviewed PR #%d: %s", pr.ID, pr.Title)

			activities = append(activities, models.Activity{
				Source:    models.SourceBitbucket,
				SourceID:  fmt.Sprintf("bitbucket:%s:review:%d", repo.FullName, pr.ID),
				Type:      models.TypeReview,
				Title:     title,
				Metadata:  string(metaJSON),
				Timestamp: pr.UpdatedOn,
			})
		}
	}

	return activities
}

// matchesUser reports whether the given PR participant is the authenticated user.
// Matches UUID first (stable across auth modes), then falls back to nickname for
// legacy app-password deployments where UUID may be missing.
func (c *Collector) matchesUser(u bbUser) bool {
	if c.userUUID != "" && u.UUID == c.userUUID {
		return true
	}
	if c.userUUID == "" && c.username != "" && u.Nickname == c.username {
		return true
	}
	return false
}

func (c *Collector) getPRCommits(ctx context.Context, repoFullName string, prID int) ([]bbCommit, error) {
	path := fmt.Sprintf("/2.0/repositories/%s/pullrequests/%d/commits", repoFullName, prID)
	var page bbPaginated[bbCommit]
	if err := c.apiGet(ctx, path, nil, &page); err != nil {
		return nil, err
	}
	return page.Values, nil
}

// --- API helpers ---

func (c *Collector) apiGet(ctx context.Context, path string, params url.Values, dst any) error {
	reqURL := c.baseURL + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}
	return c.apiGetURL(ctx, reqURL, dst)
}

func (c *Collector) apiGetURL(ctx context.Context, reqURL string, dst any) error {
	resp, err := ratelimit.Do(ctx, c.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(c.username, c.appPassword)
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("bitbucket request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bitbucket returned %d: %s", resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}
