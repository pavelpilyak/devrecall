package gitlab

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
	defaultBaseURL = "https://gitlab.com"
	perPage        = 100
)

// Collector fetches merge requests, approvals, and issues from the GitLab API.
type Collector struct {
	token    string
	username string
	baseURL  string
	client   *http.Client
	userID   int // populated on first API call
}

// New creates a GitLab collector with the default API base URL.
func New(token, username, baseURL string) *Collector {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Collector{
		token:    token,
		username: username,
		baseURL:  baseURL,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient creates a collector with a custom HTTP client (for testing).
func NewWithClient(token, username, baseURL string, client *http.Client) *Collector {
	return &Collector{
		token:    token,
		username: username,
		baseURL:  baseURL,
		client:   client,
	}
}

func (c *Collector) Name() models.Source {
	return models.SourceGitLab
}

// Collect fetches GitLab activity from the last 24 hours.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().Add(-24*time.Hour))
}

// CollectSince fetches MRs authored, MRs reviewed/approved, and issues since the given time.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	// Resolve user ID if not cached.
	if c.userID == 0 {
		id, err := c.fetchUserID(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetching user ID: %w", err)
		}
		c.userID = id
	}

	// Get projects with recent activity to scope our queries.
	projects, err := c.getActiveProjects(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("fetching projects: %w", err)
	}

	var all []models.Activity

	for _, proj := range projects {
		mrs, err := c.collectMRsAuthored(ctx, proj, since)
		if err != nil {
			return nil, fmt.Errorf("collecting MRs for %s: %w", proj.PathWithNamespace, err)
		}
		all = append(all, mrs...)

		reviewed, err := c.collectMRsReviewed(ctx, proj, since)
		if err != nil {
			return nil, fmt.Errorf("collecting MR reviews for %s: %w", proj.PathWithNamespace, err)
		}
		all = append(all, reviewed...)

		issues, err := c.collectIssues(ctx, proj, since)
		if err != nil {
			return nil, fmt.Errorf("collecting issues for %s: %w", proj.PathWithNamespace, err)
		}
		all = append(all, issues...)
	}

	return all, nil
}

// --- API types ---

type glProject struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

type glMergeRequest struct {
	IID          int       `json:"iid"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	State        string    `json:"state"` // opened, closed, merged
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MergedAt     *string   `json:"merged_at"`
	WebURL       string    `json:"web_url"`
	Author       glUser    `json:"author"`
	UserNotesCount int    `json:"user_notes_count"`
	Reviewers    []glUser  `json:"reviewers"`
}

type glUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

type glApproval struct {
	User      glUser    `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

type glApprovalResponse struct {
	ApprovedBy []glApproval `json:"approved_by"`
}

type glIssue struct {
	IID       int       `json:"iid"`
	Title     string    `json:"title"`
	Description string  `json:"description"`
	State     string    `json:"state"` // opened, closed
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	WebURL    string    `json:"web_url"`
	Author    glUser    `json:"author"`
	Assignee  *glUser   `json:"assignee"`
	Labels    []string  `json:"labels"`
}

// --- Metadata types ---

type glCommit struct {
	ID string `json:"id"` // GitLab uses "id" for SHA
}

type mrMeta struct {
	Project       string   `json:"project"`
	MRNumber      int      `json:"mr_number"`
	State         string   `json:"state"`
	Reviewers     []string `json:"reviewers,omitempty"`
	CommentsCount int      `json:"comments_count"`
	CommitSHAs    []string `json:"commit_shas,omitempty"`
	IssueKeys     []string `json:"issue_keys,omitempty"`
	URL           string   `json:"url"`
}

type approvalMeta struct {
	Project   string   `json:"project"`
	MRNumber  int      `json:"mr_number"`
	MRTitle   string   `json:"mr_title"`
	IssueKeys []string `json:"issue_keys,omitempty"`
	URL       string   `json:"url"`
}

type issueMeta struct {
	Project   string   `json:"project"`
	Number    int      `json:"number"`
	State     string   `json:"state"`
	Labels    []string `json:"labels,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	IssueKeys []string `json:"issue_keys,omitempty"`
	URL       string   `json:"url"`
}

// --- Collection methods ---

func (c *Collector) fetchUserID(ctx context.Context) (int, error) {
	var user struct {
		ID int `json:"id"`
	}
	if err := c.apiGet(ctx, "/api/v4/user", nil, &user); err != nil {
		return 0, err
	}
	return user.ID, nil
}

func (c *Collector) getActiveProjects(ctx context.Context, since time.Time) ([]glProject, error) {
	var all []glProject
	page := 1

	for {
		params := url.Values{
			"membership":                {"true"},
			"order_by":                  {"last_activity_at"},
			"sort":                      {"desc"},
			"per_page":                  {strconv.Itoa(perPage)},
			"page":                      {strconv.Itoa(page)},
			"last_activity_after":       {since.UTC().Format(time.RFC3339)},
		}

		var projects []glProject
		if err := c.apiGet(ctx, "/api/v4/projects", params, &projects); err != nil {
			return nil, err
		}

		all = append(all, projects...)

		if len(projects) < perPage {
			break
		}
		page++
	}

	return all, nil
}

func (c *Collector) collectMRsAuthored(ctx context.Context, proj glProject, since time.Time) ([]models.Activity, error) {
	params := url.Values{
		"author_id":  {strconv.Itoa(c.userID)},
		"state":      {"all"},
		"updated_after": {since.UTC().Format(time.RFC3339)},
		"per_page":   {strconv.Itoa(perPage)},
	}

	path := fmt.Sprintf("/api/v4/projects/%d/merge_requests", proj.ID)
	var mrs []glMergeRequest
	if err := c.apiGet(ctx, path, params, &mrs); err != nil {
		return nil, err
	}

	var activities []models.Activity
	for _, mr := range mrs {
		var reviewerNames []string
		for _, r := range mr.Reviewers {
			reviewerNames = append(reviewerNames, r.Username)
		}

		// Fetch commit SHAs for PR↔commit linking.
		var commitSHAs []string
		if commits, err := c.getMRCommits(ctx, proj.ID, mr.IID); err == nil {
			for _, commit := range commits {
				commitSHAs = append(commitSHAs, commit.ID)
			}
		}

		meta := mrMeta{
			Project:       proj.PathWithNamespace,
			MRNumber:      mr.IID,
			State:         mr.State,
			Reviewers:     reviewerNames,
			CommentsCount: mr.UserNotesCount,
			CommitSHAs:    commitSHAs,
			IssueKeys:     ticketlink.ExtractFromMessage(mr.Title + "\n" + mr.Description),
			URL:           mr.WebURL,
		}
		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:    models.SourceGitLab,
			SourceID:  fmt.Sprintf("gitlab:%s:mr:%d", proj.PathWithNamespace, mr.IID),
			Type:      models.TypeMergeRequest,
			Title:     mr.Title,
			Content:   mr.Description,
			Metadata:  string(metaJSON),
			Timestamp: mr.UpdatedAt,
		})
	}

	return activities, nil
}

func (c *Collector) collectMRsReviewed(ctx context.Context, proj glProject, since time.Time) ([]models.Activity, error) {
	// Fetch MRs where user is a reviewer.
	params := url.Values{
		"reviewer_id":   {strconv.Itoa(c.userID)},
		"state":         {"all"},
		"updated_after": {since.UTC().Format(time.RFC3339)},
		"per_page":      {strconv.Itoa(perPage)},
	}

	path := fmt.Sprintf("/api/v4/projects/%d/merge_requests", proj.ID)
	var mrs []glMergeRequest
	if err := c.apiGet(ctx, path, params, &mrs); err != nil {
		return nil, err
	}

	var activities []models.Activity
	for _, mr := range mrs {
		// Skip MRs authored by self (already captured in collectMRsAuthored).
		if mr.Author.Username == c.username {
			continue
		}

		// Check approval status for this MR.
		approvalPath := fmt.Sprintf("/api/v4/projects/%d/merge_requests/%d/approvals", proj.ID, mr.IID)
		var approvals glApprovalResponse
		hasApproval := false
		if err := c.apiGet(ctx, approvalPath, nil, &approvals); err == nil {
			for _, a := range approvals.ApprovedBy {
				if a.User.Username == c.username {
					hasApproval = true
					break
				}
			}
		}

		title := fmt.Sprintf("Reviewed MR !%d: %s", mr.IID, mr.Title)
		if hasApproval {
			title = fmt.Sprintf("Approved MR !%d: %s", mr.IID, mr.Title)
		}

		meta := approvalMeta{
			Project:   proj.PathWithNamespace,
			MRNumber:  mr.IID,
			MRTitle:   mr.Title,
			IssueKeys: ticketlink.ExtractFromMessage(mr.Title + "\n" + mr.Description),
			URL:       mr.WebURL,
		}
		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:    models.SourceGitLab,
			SourceID:  fmt.Sprintf("gitlab:%s:review:%d", proj.PathWithNamespace, mr.IID),
			Type:      models.TypeReview,
			Title:     title,
			Metadata:  string(metaJSON),
			Timestamp: mr.UpdatedAt,
		})
	}

	return activities, nil
}

func (c *Collector) getMRCommits(ctx context.Context, projectID, mrIID int) ([]glCommit, error) {
	path := fmt.Sprintf("/api/v4/projects/%d/merge_requests/%d/commits", projectID, mrIID)
	var commits []glCommit
	if err := c.apiGet(ctx, path, nil, &commits); err != nil {
		return nil, err
	}
	return commits, nil
}

func (c *Collector) collectIssues(ctx context.Context, proj glProject, since time.Time) ([]models.Activity, error) {
	// Fetch issues assigned to or authored by the user.
	var all []models.Activity
	seen := make(map[int]bool)

	for _, scope := range []string{"assigned_to_me", "created_by_me"} {
		params := url.Values{
			"scope":         {scope},
			"updated_after": {since.UTC().Format(time.RFC3339)},
			"per_page":      {strconv.Itoa(perPage)},
		}

		path := fmt.Sprintf("/api/v4/projects/%d/issues", proj.ID)
		var issues []glIssue
		if err := c.apiGet(ctx, path, params, &issues); err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if seen[issue.IID] {
				continue
			}
			seen[issue.IID] = true

			assignee := ""
			if issue.Assignee != nil {
				assignee = issue.Assignee.Username
			}

			meta := issueMeta{
				Project:   proj.PathWithNamespace,
				Number:    issue.IID,
				State:     issue.State,
				Labels:    issue.Labels,
				Assignee:  assignee,
				IssueKeys: ticketlink.ExtractFromMessage(issue.Title + "\n" + issue.Description),
				URL:       issue.WebURL,
			}
			metaJSON, _ := json.Marshal(meta)

			all = append(all, models.Activity{
				Source:    models.SourceGitLab,
				SourceID:  fmt.Sprintf("gitlab:%s:issue:%d", proj.PathWithNamespace, issue.IID),
				Type:      models.TypeIssue,
				Title:     issue.Title,
				Content:   issue.Description,
				Metadata:  string(metaJSON),
				Timestamp: issue.UpdatedAt,
			})
		}
	}

	return all, nil
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
		req.Header.Set("PRIVATE-TOKEN", c.token)
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("gitlab request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab %s returned %d: %s", path, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}
