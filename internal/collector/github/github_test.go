package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) (*httptest.Server, *Collector) {
	t.Helper()
	mux := http.NewServeMux()
	for path, handler := range handlers {
		mux.HandleFunc(path, handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewWithClient("ghp_testtoken", "testuser", srv.URL, srv.Client())
	return srv, c
}

func TestName(t *testing.T) {
	c := New("token", "user")
	if c.Name() != models.SourceGitHub {
		t.Errorf("Name() = %q, want %q", c.Name(), models.SourceGitHub)
	}
}

func TestCollectPRsAuthored(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ghp_testtoken" {
				t.Error("missing or wrong Authorization header")
			}

			q := r.URL.Query().Get("q")
			if q == "" {
				t.Error("missing query parameter")
			}

			json.NewEncoder(w).Encode(searchResult[searchIssueItem]{
				TotalCount: 1,
				Items: []searchIssueItem{
					{
						Number:        42,
						Title:         "Fix auth bug",
						Body:          "Fixes the token refresh",
						State:         "open",
						CreatedAt:     time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
						UpdatedAt:     time.Date(2026, 4, 2, 14, 0, 0, 0, time.UTC),
						HTMLURL:       "https://github.com/octocat/backend/pull/42",
						RepositoryURL: "https://api.github.com/repos/octocat/backend",
						User:          ghUser{Login: "testuser"},
						PullRequest:   &struct{}{}, // marks as PR in search
					},
				},
			})
		},
		"/repos/octocat/backend/pulls/42": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(pullRequest{
				Number:    42,
				Title:     "Fix auth bug",
				Body:      "Fixes the token refresh",
				State:     "open",
				HTMLURL:   "https://github.com/octocat/backend/pull/42",
				Comments:  3,
				Commits:   2,
				Additions: 47,
				Deletions: 12,
				Reviewers: []ghUser{{Login: "reviewer1"}},
			})
		},
		"/repos/octocat/backend/pulls/42/commits": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]ghCommit{
				{SHA: "abc123def456"},
				{SHA: "789012fedcba"},
			})
		},
	})

	activities, err := c.collectPRsAuthored(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collectPRsAuthored: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Source != models.SourceGitHub {
		t.Errorf("Source = %q, want %q", a.Source, models.SourceGitHub)
	}
	if a.Type != models.TypePullRequest {
		t.Errorf("Type = %q, want %q", a.Type, models.TypePullRequest)
	}
	if a.Title != "Fix auth bug" {
		t.Errorf("Title = %q, want %q", a.Title, "Fix auth bug")
	}
	if a.SourceID != "github:octocat/backend:pr:42" {
		t.Errorf("SourceID = %q, want %q", a.SourceID, "github:octocat/backend:pr:42")
	}

	var meta prMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.Additions != 47 {
		t.Errorf("meta.Additions = %d, want 47", meta.Additions)
	}
	if meta.CommitsCount != 2 {
		t.Errorf("meta.CommitsCount = %d, want 2", meta.CommitsCount)
	}
	if len(meta.Reviewers) != 1 || meta.Reviewers[0] != "reviewer1" {
		t.Errorf("meta.Reviewers = %v, want [reviewer1]", meta.Reviewers)
	}
	if len(meta.CommitSHAs) != 2 || meta.CommitSHAs[0] != "abc123def456" || meta.CommitSHAs[1] != "789012fedcba" {
		t.Errorf("meta.CommitSHAs = %v, want [abc123def456 789012fedcba]", meta.CommitSHAs)
	}
}

func TestCollectPRsReviewed(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult[searchIssueItem]{
				TotalCount: 1,
				Items: []searchIssueItem{
					{
						Number:        99,
						Title:         "Add notifications",
						State:         "open",
						UpdatedAt:     time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
						HTMLURL:       "https://github.com/octocat/backend/pull/99",
						RepositoryURL: "https://api.github.com/repos/octocat/backend",
						User:          ghUser{Login: "otherdev"}, // authored by someone else
					},
				},
			})
		},
		"/repos/octocat/backend/pulls/99/reviews": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]ghReview{
				{
					ID:          501,
					User:        ghUser{Login: "testuser"},
					State:       "APPROVED",
					SubmittedAt: time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC),
					HTMLURL:     "https://github.com/octocat/backend/pull/99#pullrequestreview-501",
				},
				{
					ID:          502,
					User:        ghUser{Login: "someone_else"},
					State:       "COMMENTED",
					SubmittedAt: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
				},
			})
		},
	})

	activities, err := c.collectPRsReviewed(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collectPRsReviewed: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Type != models.TypeReview {
		t.Errorf("Type = %q, want %q", a.Type, models.TypeReview)
	}
	if a.SourceID != "github:octocat/backend:review:501" {
		t.Errorf("SourceID = %q, want %q", a.SourceID, "github:octocat/backend:review:501")
	}
	if a.Title != "Approved PR #99: Add notifications" {
		t.Errorf("Title = %q, want %q", a.Title, "Approved PR #99: Add notifications")
	}

	var meta reviewMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.State != "APPROVED" {
		t.Errorf("meta.State = %q, want APPROVED", meta.State)
	}
}

func TestCollectPRsReviewed_SkipsSelfAuthored(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult[searchIssueItem]{
				TotalCount: 1,
				Items: []searchIssueItem{
					{
						Number:        50,
						Title:         "My own PR",
						State:         "open",
						UpdatedAt:     time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
						RepositoryURL: "https://api.github.com/repos/octocat/backend",
						User:          ghUser{Login: "testuser"}, // self-authored
					},
				},
			})
		},
	})

	activities, err := c.collectPRsReviewed(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collectPRsReviewed: %v", err)
	}
	if len(activities) != 0 {
		t.Errorf("got %d activities, want 0 (should skip self-authored)", len(activities))
	}
}

func TestCollectIssues(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult[searchIssueItem]{
				TotalCount: 2,
				Items: []searchIssueItem{
					{
						Number:        10,
						Title:         "Login page broken",
						Body:          "Users can't log in",
						State:         "open",
						UpdatedAt:     time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC),
						HTMLURL:       "https://github.com/octocat/frontend/issues/10",
						RepositoryURL: "https://api.github.com/repos/octocat/frontend",
						User:          ghUser{Login: "testuser"},
						Labels:        []ghLabel{{Name: "bug"}, {Name: "urgent"}},
						Assignee:      &ghUser{Login: "testuser"},
						PullRequest:   nil, // actual issue
					},
					{
						Number:        11,
						Title:         "Some PR sneaking in",
						State:         "open",
						UpdatedAt:     time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC),
						RepositoryURL: "https://api.github.com/repos/octocat/frontend",
						User:          ghUser{Login: "testuser"},
						PullRequest:   &struct{}{}, // this is actually a PR, should be filtered
					},
				},
			})
		},
	})

	activities, err := c.collectIssues(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collectIssues: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1 (PRs should be filtered)", len(activities))
	}

	a := activities[0]
	if a.Type != models.TypeIssue {
		t.Errorf("Type = %q, want %q", a.Type, models.TypeIssue)
	}
	if a.Title != "Login page broken" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.SourceID != "github:octocat/frontend:issue:10" {
		t.Errorf("SourceID = %q", a.SourceID)
	}

	var meta issueMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if len(meta.Labels) != 2 || meta.Labels[0] != "bug" {
		t.Errorf("meta.Labels = %v", meta.Labels)
	}
	if meta.Assignee != "testuser" {
		t.Errorf("meta.Assignee = %q, want testuser", meta.Assignee)
	}
}

func TestCollectSince_Pagination(t *testing.T) {
	callCount := 0
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			callCount++
			q := r.URL.Query().Get("q")

			// Only respond to the PR-authored query with pagination.
			if callCount <= 2 {
				page := r.URL.Query().Get("page")
				if page == "" || page == "1" {
					items := make([]searchIssueItem, perPage)
					for i := range items {
						items[i] = searchIssueItem{
							Number:        i + 1,
							Title:         "PR " + string(rune('A'+i)),
							State:         "open",
							UpdatedAt:     time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
							RepositoryURL: "https://api.github.com/repos/octocat/repo",
							User:          ghUser{Login: "testuser"},
							PullRequest:   &struct{}{},
						}
					}
					json.NewEncoder(w).Encode(searchResult[searchIssueItem]{
						TotalCount: perPage + 5,
						Items:      items,
					})
				} else {
					// Page 2: 5 remaining items.
					items := make([]searchIssueItem, 5)
					for i := range items {
						items[i] = searchIssueItem{
							Number:        perPage + i + 1,
							Title:         "PR extra",
							State:         "open",
							UpdatedAt:     time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
							RepositoryURL: "https://api.github.com/repos/octocat/repo",
							User:          ghUser{Login: "testuser"},
							PullRequest:   &struct{}{},
						}
					}
					json.NewEncoder(w).Encode(searchResult[searchIssueItem]{
						TotalCount: perPage + 5,
						Items:      items,
					})
				}
				return
			}

			// For review and issue queries, return empty.
			_ = q
			json.NewEncoder(w).Encode(searchResult[searchIssueItem]{TotalCount: 0})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	// Should have fetched all 105 PR items across 2 pages.
	prCount := 0
	for _, a := range activities {
		if a.Type == models.TypePullRequest {
			prCount++
		}
	}
	if prCount != perPage+5 {
		t.Errorf("got %d PRs, want %d", prCount, perPage+5)
	}
}

func TestRateLimited(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})

	_, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestAPIError(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"internal error"}`))
		},
	})

	_, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected API error")
	}
}

func TestEmptyResults(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult[searchIssueItem]{TotalCount: 0})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	if len(activities) != 0 {
		t.Errorf("got %d activities, want 0", len(activities))
	}
}

func TestRepoFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://api.github.com/repos/octocat/hello-world", "octocat/hello-world"},
		{"https://api.github.com/repos/org/repo", "org/repo"},
		{"invalid-url", "invalid-url"},
	}

	for _, tt := range tests {
		got := repoFromURL(tt.url)
		if got != tt.want {
			t.Errorf("repoFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestMergedPRState(t *testing.T) {
	mergedAt := "2026-04-02T15:00:00Z"
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search/issues": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult[searchIssueItem]{
				TotalCount: 1,
				Items: []searchIssueItem{
					{
						Number:        77,
						Title:         "Merged PR",
						State:         "closed",
						UpdatedAt:     time.Date(2026, 4, 2, 15, 0, 0, 0, time.UTC),
						RepositoryURL: "https://api.github.com/repos/octocat/repo",
						User:          ghUser{Login: "testuser"},
						PullRequest:   &struct{}{},
					},
				},
			})
		},
		"/repos/octocat/repo/pulls/77": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(pullRequest{
				Number:   77,
				Title:    "Merged PR",
				State:    "closed",
				MergedAt: &mergedAt,
				HTMLURL:  "https://github.com/octocat/repo/pull/77",
			})
		},
	})

	activities, err := c.collectPRsAuthored(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collectPRsAuthored: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	var meta prMeta
	json.Unmarshal([]byte(activities[0].Metadata), &meta)
	if meta.State != "merged" {
		t.Errorf("meta.State = %q, want merged", meta.State)
	}
}
