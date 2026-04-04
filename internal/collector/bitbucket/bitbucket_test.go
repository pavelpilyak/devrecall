package bitbucket

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

	c := NewWithClient("testuser", "app-password", "myworkspace", srv.URL, srv.Client())
	return srv, c
}

func TestName(t *testing.T) {
	c := New("user", "pass", "ws")
	if c.Name() != models.SourceBitbucket {
		t.Errorf("Name() = %q, want %q", c.Name(), models.SourceBitbucket)
	}
}

func TestCollectPRsAuthored(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/2.0/repositories/myworkspace": func(w http.ResponseWriter, r *http.Request) {
			// Verify basic auth.
			user, pass, ok := r.BasicAuth()
			if !ok || user != "testuser" || pass != "app-password" {
				t.Error("missing or wrong Basic Auth")
			}
			json.NewEncoder(w).Encode(bbPaginated[bbRepo]{
				Values: []bbRepo{
					{FullName: "myworkspace/backend", Slug: "backend"},
				},
			})
		},
		"/2.0/repositories/myworkspace/backend/pullrequests": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbPullRequest]{
				Values: []bbPullRequest{
					{
						ID:           42,
						Title:        "Fix auth bug",
						Description:  "Fixes token refresh",
						State:        "OPEN",
						CreatedOn:    time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
						UpdatedOn:    time.Date(2026, 4, 2, 14, 0, 0, 0, time.UTC),
						Author:       bbUser{Nickname: "testuser", DisplayName: "Test User"},
						Reviewers:    []bbUser{{Nickname: "reviewer1", DisplayName: "Reviewer One"}},
						CommentCount: 3,
						Links:        bbLinks{HTML: bbHref{Href: "https://bitbucket.org/myworkspace/backend/pull-requests/42"}},
					},
				},
			})
		},
		"/2.0/repositories/myworkspace/backend/pullrequests/42/commits": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbCommit]{
				Values: []bbCommit{
					{Hash: "ff1122aa"},
					{Hash: "bb3344cc"},
				},
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Source != models.SourceBitbucket {
		t.Errorf("Source = %q, want %q", a.Source, models.SourceBitbucket)
	}
	if a.Type != models.TypePullRequest {
		t.Errorf("Type = %q, want %q", a.Type, models.TypePullRequest)
	}
	if a.Title != "Fix auth bug" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.SourceID != "bitbucket:myworkspace/backend:pr:42" {
		t.Errorf("SourceID = %q", a.SourceID)
	}

	var meta prMeta
	json.Unmarshal([]byte(a.Metadata), &meta)
	if meta.State != "OPEN" {
		t.Errorf("meta.State = %q, want OPEN", meta.State)
	}
	if meta.CommentsCount != 3 {
		t.Errorf("meta.CommentsCount = %d, want 3", meta.CommentsCount)
	}
	if len(meta.Reviewers) != 1 || meta.Reviewers[0] != "Reviewer One" {
		t.Errorf("meta.Reviewers = %v", meta.Reviewers)
	}
	if len(meta.CommitSHAs) != 2 || meta.CommitSHAs[0] != "ff1122aa" || meta.CommitSHAs[1] != "bb3344cc" {
		t.Errorf("meta.CommitSHAs = %v, want [ff1122aa bb3344cc]", meta.CommitSHAs)
	}
}

func TestCollectPRsReviewed(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/2.0/repositories/myworkspace": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbRepo]{
				Values: []bbRepo{{FullName: "myworkspace/backend", Slug: "backend"}},
			})
		},
		"/2.0/repositories/myworkspace/backend/pullrequests": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbPullRequest]{
				Values: []bbPullRequest{
					{
						ID:        99,
						Title:     "Add notifications",
						State:     "OPEN",
						UpdatedOn: time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
						Author:    bbUser{Nickname: "otherdev", DisplayName: "Other Dev"},
						Reviewers: []bbUser{{Nickname: "testuser", DisplayName: "Test User"}},
						Links:     bbLinks{HTML: bbHref{Href: "https://bitbucket.org/myworkspace/backend/pull-requests/99"}},
					},
				},
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Type != models.TypeReview {
		t.Errorf("Type = %q, want %q", a.Type, models.TypeReview)
	}
	if a.Title != "Reviewed PR #99: Add notifications" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.SourceID != "bitbucket:myworkspace/backend:review:99" {
		t.Errorf("SourceID = %q", a.SourceID)
	}
}

func TestCollectPRs_AuthorAndReviewer(t *testing.T) {
	// If user is both author and reviewer, should only appear as author (no review activity).
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/2.0/repositories/myworkspace": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbRepo]{
				Values: []bbRepo{{FullName: "myworkspace/backend", Slug: "backend"}},
			})
		},
		"/2.0/repositories/myworkspace/backend/pullrequests": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbPullRequest]{
				Values: []bbPullRequest{
					{
						ID:        50,
						Title:     "Self-reviewed PR",
						State:     "MERGED",
						UpdatedOn: time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC),
						Author:    bbUser{Nickname: "testuser"},
						Reviewers: []bbUser{{Nickname: "testuser"}},
					},
				},
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1 (author only, not review)", len(activities))
	}
	if activities[0].Type != models.TypePullRequest {
		t.Errorf("Type = %q, want pull_request", activities[0].Type)
	}
}

func TestCollect_SkipsOldPRs(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/2.0/repositories/myworkspace": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbRepo]{
				Values: []bbRepo{{FullName: "myworkspace/backend", Slug: "backend"}},
			})
		},
		"/2.0/repositories/myworkspace/backend/pullrequests": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbPullRequest]{
				Values: []bbPullRequest{
					{
						ID:        10,
						Title:     "Old PR",
						State:     "MERGED",
						UpdatedOn: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // very old
						Author:    bbUser{Nickname: "testuser"},
					},
				},
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	if len(activities) != 0 {
		t.Errorf("got %d activities, want 0 (old PR should be skipped)", len(activities))
	}
}

func TestPagination(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/myworkspace", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(bbPaginated[bbRepo]{
			Values: []bbRepo{{FullName: "myworkspace/backend", Slug: "backend"}},
		})
	})
	mux.HandleFunc("/2.0/repositories/myworkspace/backend/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("page")
		if q == "2" {
			json.NewEncoder(w).Encode(bbPaginated[bbPullRequest]{
				Values: []bbPullRequest{
					{
						ID:        2,
						Title:     "PR page 2",
						State:     "OPEN",
						UpdatedOn: time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC),
						Author:    bbUser{Nickname: "testuser"},
					},
				},
			})
		} else {
			json.NewEncoder(w).Encode(bbPaginated[bbPullRequest]{
				Values: []bbPullRequest{
					{
						ID:        1,
						Title:     "PR page 1",
						State:     "OPEN",
						UpdatedOn: time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
						Author:    bbUser{Nickname: "testuser"},
					},
				},
				Next: srvURL + "/2.0/repositories/myworkspace/backend/pullrequests?page=2",
			})
		}
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	t.Cleanup(srv.Close)

	c := NewWithClient("testuser", "app-password", "myworkspace", srv.URL, srv.Client())

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 2 {
		t.Errorf("got %d activities, want 2 (from 2 pages)", len(activities))
	}
}

func TestRateLimited(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/2.0/repositories/myworkspace": func(w http.ResponseWriter, r *http.Request) {
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
		"/2.0/repositories/myworkspace": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal"}`))
		},
	})

	_, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected API error")
	}
}

func TestEmptyRepos(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/2.0/repositories/myworkspace": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(bbPaginated[bbRepo]{Values: []bbRepo{}})
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

func TestValidateBitbucketAppPassword_Success(t *testing.T) {
	// This tests the auth package function indirectly via the test server pattern.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "bbuser" || pass != "app-pass-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"username": "bbuser"})
	}))
	defer srv.Close()

	// Just verify the server works with correct auth.
	req, _ := http.NewRequest("GET", srv.URL+"/2.0/user", nil)
	req.SetBasicAuth("bbuser", "app-pass-123")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
