package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pavelpiliak/devrecall/internal/collector/ratelimit"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

const (
	defaultGraphQLURL = "https://api.linear.app/graphql"
	pageSize          = 50
)

// Collector fetches Linear issues, state transitions, and comments via GraphQL.
type Collector struct {
	token      string
	graphqlURL string
	userID     string // cached after first viewer query
	client     *http.Client
}

// New creates a Linear collector with the default GraphQL endpoint.
func New(token string) *Collector {
	return &Collector{
		token:      token,
		graphqlURL: defaultGraphQLURL,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient creates a collector with a custom HTTP client and URL (for testing).
func NewWithClient(token, graphqlURL string, client *http.Client) *Collector {
	return &Collector{
		token:      token,
		graphqlURL: graphqlURL,
		client:     client,
	}
}

func (c *Collector) Name() models.Source {
	return models.SourceLinear
}

// Collect fetches Linear activity from the last 24 hours.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().Add(-24*time.Hour))
}

// CollectSince fetches assigned issues updated since the given time,
// including state transitions and comments inline via GraphQL.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	if err := c.ensureUserID(ctx); err != nil {
		return nil, fmt.Errorf("resolving user ID: %w", err)
	}

	issues, err := c.fetchIssues(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("fetching issues: %w", err)
	}

	var all []models.Activity

	for _, issue := range issues {
		transitions := c.extractTransitions(issue, since)
		all = append(all, transitions...)

		comments := c.extractComments(issue, since)
		all = append(all, comments...)

		// If no transitions or comments, emit a bare ticket activity.
		if len(transitions) == 0 && len(comments) == 0 {
			meta := issueMeta{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				IssueTitle:      issue.Title,
				Status:          issue.State.Name,
				Priority:        issue.Priority,
				Labels:          labelNames(issue.Labels.Nodes),
				URL:             issue.URL,
			}
			if issue.Project != nil {
				meta.Project = issue.Project.Name
			}
			if issue.Cycle != nil {
				meta.Cycle = issue.Cycle.Name
			}
			metaJSON, _ := json.Marshal(meta)

			all = append(all, models.Activity{
				Source:    models.SourceLinear,
				SourceID:  fmt.Sprintf("linear:%s", issue.ID),
				Type:      models.TypeTicket,
				Title:     fmt.Sprintf("%s: %s", issue.Identifier, issue.Title),
				Metadata:  string(metaJSON),
				Timestamp: issue.UpdatedAt,
			})
		}
	}

	return all, nil
}

// --- GraphQL types ---

type gqlRequest struct {
	Query     string `json:"query"`
	Variables any    `json:"variables,omitempty"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
}

type viewerResponse struct {
	Viewer struct {
		ID string `json:"id"`
	} `json:"viewer"`
}

type issuesResponse struct {
	Issues struct {
		Nodes    []linearIssue `json:"nodes"`
		PageInfo pageInfo      `json:"pageInfo"`
	} `json:"issues"`
}

type linearIssue struct {
	ID         string      `json:"id"`
	Identifier string      `json:"identifier"` // e.g., "ENG-123"
	Title      string      `json:"title"`
	URL        string      `json:"url"`
	State      linearState `json:"state"`
	Priority   int         `json:"priority"`
	CreatedAt  time.Time   `json:"createdAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
	Project    *struct {
		Name string `json:"name"`
	} `json:"project"`
	Cycle *struct {
		Name   string `json:"name"`
		Number int    `json:"number"`
	} `json:"cycle"`
	Labels struct {
		Nodes []linearLabel `json:"nodes"`
	} `json:"labels"`
	History struct {
		Nodes []linearHistoryEntry `json:"nodes"`
	} `json:"history"`
	Comments struct {
		Nodes []linearComment `json:"nodes"`
	} `json:"comments"`
}

type linearState struct {
	Name string `json:"name"`
}

type linearLabel struct {
	Name string `json:"name"`
}

type linearHistoryEntry struct {
	ID        string       `json:"id"`
	CreatedAt time.Time    `json:"createdAt"`
	FromState *linearState `json:"fromState"`
	ToState   *linearState `json:"toState"`
	Actor     *struct {
		ID string `json:"id"`
	} `json:"actor"`
}

type linearComment struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	User      struct {
		ID string `json:"id"`
	} `json:"user"`
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// --- Metadata types ---

type issueMeta struct {
	IssueID         string   `json:"issue_id"`
	IssueIdentifier string   `json:"issue_identifier"`
	IssueTitle      string   `json:"issue_title"`
	Project         string   `json:"project,omitempty"`
	Status          string   `json:"status,omitempty"`
	FromStatus      string   `json:"from_status,omitempty"`
	ToStatus        string   `json:"to_status,omitempty"`
	Priority        int      `json:"priority"`
	Labels          []string `json:"labels,omitempty"`
	Cycle           string   `json:"cycle,omitempty"`
	URL             string   `json:"url"`
}

type commentMeta struct {
	IssueIdentifier string `json:"issue_identifier"`
	CommentID       string `json:"comment_id"`
	URL             string `json:"url"`
}

// --- Collection methods ---

func (c *Collector) ensureUserID(ctx context.Context) error {
	if c.userID != "" {
		return nil
	}

	var data viewerResponse
	if err := c.graphql(ctx, `{ viewer { id } }`, nil, &data); err != nil {
		return err
	}
	c.userID = data.Viewer.ID
	return nil
}

const issuesQuery = `
query($since: DateTimeOrDuration!, $first: Int!, $after: String) {
  issues(
    filter: {
      assignee: { isMe: { eq: true } }
      updatedAt: { gte: $since }
    }
    orderBy: updatedAt
    first: $first
    after: $after
  ) {
    nodes {
      id
      identifier
      title
      url
      state { name }
      priority
      createdAt
      updatedAt
      project { name }
      cycle { name number }
      labels { nodes { name } }
      history(first: 20) {
        nodes {
          id
          createdAt
          fromState { name }
          toState { name }
          actor { id }
        }
      }
      comments(first: 20) {
        nodes {
          id
          body
          createdAt
          user { id }
        }
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
`

func (c *Collector) fetchIssues(ctx context.Context, since time.Time) ([]linearIssue, error) {
	var all []linearIssue
	var cursor *string

	for {
		vars := map[string]any{
			"since": since.Format(time.RFC3339),
			"first": pageSize,
		}
		if cursor != nil {
			vars["after"] = *cursor
		}

		var data issuesResponse
		if err := c.graphql(ctx, issuesQuery, vars, &data); err != nil {
			return nil, err
		}

		all = append(all, data.Issues.Nodes...)

		if !data.Issues.PageInfo.HasNextPage || len(data.Issues.Nodes) < pageSize {
			break
		}
		cursor = &data.Issues.PageInfo.EndCursor
	}

	return all, nil
}

func (c *Collector) extractTransitions(issue linearIssue, since time.Time) []models.Activity {
	var activities []models.Activity

	for _, entry := range issue.History.Nodes {
		if entry.CreatedAt.Before(since) {
			continue
		}
		// Only state transitions (both fromState and toState present).
		if entry.FromState == nil || entry.ToState == nil {
			continue
		}
		// Only our own transitions.
		if entry.Actor == nil || entry.Actor.ID != c.userID {
			continue
		}

		meta := issueMeta{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			IssueTitle:      issue.Title,
			Priority:        issue.Priority,
			Labels:          labelNames(issue.Labels.Nodes),
			FromStatus:      entry.FromState.Name,
			ToStatus:        entry.ToState.Name,
			URL:             issue.URL,
		}
		if issue.Project != nil {
			meta.Project = issue.Project.Name
		}
		if issue.Cycle != nil {
			meta.Cycle = issue.Cycle.Name
		}
		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:   models.SourceLinear,
			SourceID: fmt.Sprintf("linear:%s:history:%s", issue.ID, entry.ID),
			Type:     models.TypeTicket,
			Title: fmt.Sprintf("%s: %s → moved to %s",
				issue.Identifier, issue.Title, entry.ToState.Name),
			Content: fmt.Sprintf("Moved %s from '%s' to '%s'",
				issue.Identifier, entry.FromState.Name, entry.ToState.Name),
			Metadata:  string(metaJSON),
			Timestamp: entry.CreatedAt,
		})
	}

	return activities
}

func (c *Collector) extractComments(issue linearIssue, since time.Time) []models.Activity {
	var activities []models.Activity

	for _, comment := range issue.Comments.Nodes {
		if comment.CreatedAt.Before(since) {
			continue
		}
		// Only our own comments.
		if comment.User.ID != c.userID {
			continue
		}

		meta := commentMeta{
			IssueIdentifier: issue.Identifier,
			CommentID:       comment.ID,
			URL:             issue.URL,
		}
		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:    models.SourceLinear,
			SourceID:  fmt.Sprintf("linear:%s:comment:%s", issue.ID, comment.ID),
			Type:      models.TypeTicket,
			Title:     fmt.Sprintf("Commented on %s: %s", issue.Identifier, issue.Title),
			Content:   comment.Body,
			Metadata:  string(metaJSON),
			Timestamp: comment.CreatedAt,
		})
	}

	return activities
}

// --- GraphQL helper ---

func (c *Collector) graphql(ctx context.Context, query string, variables any, dst any) error {
	reqBody := gqlRequest{Query: query, Variables: variables}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	resp, err := ratelimit.Do(ctx, c.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", c.token)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("linear request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("linear returned %d: %s", resp.StatusCode, body)
	}

	var gqlResp gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("linear GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	return json.Unmarshal(gqlResp.Data, dst)
}

func labelNames(labels []linearLabel) []string {
	if len(labels) == 0 {
		return nil
	}
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return names
}
