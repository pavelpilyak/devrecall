package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

// gqlHandler creates a test server that dispatches based on the GraphQL query content.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Collector) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewWithClient("lin_test_token", srv.URL, srv.Client())
	c.userID = "user-self" // pre-set to skip viewer call
	return srv, c
}

func respondJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func TestName(t *testing.T) {
	c := New("token")
	if c.Name() != models.SourceLinear {
		t.Errorf("Name() = %q, want %q", c.Name(), models.SourceLinear)
	}
}

func TestCollectSince_StateTransition(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		json.NewDecoder(r.Body).Decode(&req)

		respondJSON(w, gqlResponse{
			Data: mustJSON(issuesResponse{
				Issues: struct {
					Nodes    []linearIssue `json:"nodes"`
					PageInfo pageInfo      `json:"pageInfo"`
				}{
					Nodes: []linearIssue{
						{
							ID:         "issue-abc",
							Identifier: "ENG-123",
							Title:      "Fix notification bug",
							URL:        "https://linear.app/team/issue/ENG-123",
							State:      linearState{Name: "Done"},
							Priority:   2,
							UpdatedAt:  time.Date(2026, 4, 4, 16, 0, 0, 0, time.UTC),
							Project:    &struct{ Name string `json:"name"` }{Name: "Notifications"},
							Cycle: &struct {
								Name   string `json:"name"`
								Number int    `json:"number"`
							}{Name: "Cycle 12", Number: 12},
							Labels: struct {
								Nodes []linearLabel `json:"nodes"`
							}{Nodes: []linearLabel{{Name: "frontend"}, {Name: "ux"}}},
							History: struct {
								Nodes []linearHistoryEntry `json:"nodes"`
							}{
								Nodes: []linearHistoryEntry{
									{
										ID:        "hist-1",
										CreatedAt: time.Date(2026, 4, 4, 15, 0, 0, 0, time.UTC),
										FromState: &linearState{Name: "In Review"},
										ToState:   &linearState{Name: "Done"},
										Actor:     &struct{ ID string `json:"id"` }{ID: "user-self"},
									},
								},
							},
							Comments: struct {
								Nodes []linearComment `json:"nodes"`
							}{},
						},
					},
					PageInfo: pageInfo{HasNextPage: false},
				},
			}),
		})
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Source != models.SourceLinear {
		t.Errorf("Source = %q, want %q", a.Source, models.SourceLinear)
	}
	if a.Type != models.TypeTicket {
		t.Errorf("Type = %q, want %q", a.Type, models.TypeTicket)
	}
	if a.SourceID != "linear:issue-abc:history:hist-1" {
		t.Errorf("SourceID = %q", a.SourceID)
	}
	if a.Title != "ENG-123: Fix notification bug → moved to Done" {
		t.Errorf("Title = %q", a.Title)
	}

	var meta issueMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.FromStatus != "In Review" {
		t.Errorf("meta.FromStatus = %q", meta.FromStatus)
	}
	if meta.ToStatus != "Done" {
		t.Errorf("meta.ToStatus = %q", meta.ToStatus)
	}
	if meta.Project != "Notifications" {
		t.Errorf("meta.Project = %q", meta.Project)
	}
	if meta.Cycle != "Cycle 12" {
		t.Errorf("meta.Cycle = %q", meta.Cycle)
	}
	if len(meta.Labels) != 2 || meta.Labels[0] != "frontend" {
		t.Errorf("meta.Labels = %v", meta.Labels)
	}
}

func TestCollectSince_Comment(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, gqlResponse{
			Data: mustJSON(issuesResponse{
				Issues: struct {
					Nodes    []linearIssue `json:"nodes"`
					PageInfo pageInfo      `json:"pageInfo"`
				}{
					Nodes: []linearIssue{
						{
							ID:         "issue-def",
							Identifier: "ENG-456",
							Title:      "Update onboarding flow",
							URL:        "https://linear.app/team/issue/ENG-456",
							State:      linearState{Name: "In Progress"},
							Priority:   1,
							UpdatedAt:  time.Date(2026, 4, 4, 14, 0, 0, 0, time.UTC),
							History: struct {
								Nodes []linearHistoryEntry `json:"nodes"`
							}{},
							Comments: struct {
								Nodes []linearComment `json:"nodes"`
							}{
								Nodes: []linearComment{
									{
										ID:        "comment-1",
										Body:      "Added the new welcome screen.",
										CreatedAt: time.Date(2026, 4, 4, 13, 30, 0, 0, time.UTC),
										User:      struct{ ID string `json:"id"` }{ID: "user-self"},
									},
								},
							},
						},
					},
					PageInfo: pageInfo{HasNextPage: false},
				},
			}),
		})
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.SourceID != "linear:issue-def:comment:comment-1" {
		t.Errorf("SourceID = %q", a.SourceID)
	}
	if a.Title != "Commented on ENG-456: Update onboarding flow" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.Content != "Added the new welcome screen." {
		t.Errorf("Content = %q", a.Content)
	}
}

func TestCollectSince_BareTicket(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, gqlResponse{
			Data: mustJSON(issuesResponse{
				Issues: struct {
					Nodes    []linearIssue `json:"nodes"`
					PageInfo pageInfo      `json:"pageInfo"`
				}{
					Nodes: []linearIssue{
						{
							ID:         "issue-bare",
							Identifier: "ENG-789",
							Title:      "Just an updated issue",
							URL:        "https://linear.app/team/issue/ENG-789",
							State:      linearState{Name: "Todo"},
							Priority:   3,
							UpdatedAt:  time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC),
							History: struct {
								Nodes []linearHistoryEntry `json:"nodes"`
							}{},
							Comments: struct {
								Nodes []linearComment `json:"nodes"`
							}{},
						},
					},
					PageInfo: pageInfo{HasNextPage: false},
				},
			}),
		})
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.SourceID != "linear:issue-bare" {
		t.Errorf("SourceID = %q, want bare ticket", a.SourceID)
	}
	if a.Title != "ENG-789: Just an updated issue" {
		t.Errorf("Title = %q", a.Title)
	}
}

func TestCollectSince_FiltersOtherUsers(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, gqlResponse{
			Data: mustJSON(issuesResponse{
				Issues: struct {
					Nodes    []linearIssue `json:"nodes"`
					PageInfo pageInfo      `json:"pageInfo"`
				}{
					Nodes: []linearIssue{
						{
							ID:         "issue-other",
							Identifier: "ENG-100",
							Title:      "Other user activity",
							URL:        "https://linear.app/team/issue/ENG-100",
							State:      linearState{Name: "Done"},
							UpdatedAt:  time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC),
							History: struct {
								Nodes []linearHistoryEntry `json:"nodes"`
							}{
								Nodes: []linearHistoryEntry{
									{
										ID:        "hist-other",
										CreatedAt: time.Date(2026, 4, 4, 11, 0, 0, 0, time.UTC),
										FromState: &linearState{Name: "Open"},
										ToState:   &linearState{Name: "Done"},
										Actor:     &struct{ ID string `json:"id"` }{ID: "other-user"},
									},
								},
							},
							Comments: struct {
								Nodes []linearComment `json:"nodes"`
							}{
								Nodes: []linearComment{
									{
										ID:        "comment-other",
										Body:      "Someone else wrote this",
										CreatedAt: time.Date(2026, 4, 4, 11, 30, 0, 0, time.UTC),
										User:      struct{ ID string `json:"id"` }{ID: "other-user"},
									},
								},
							},
						},
					},
					PageInfo: pageInfo{HasNextPage: false},
				},
			}),
		})
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	// Other user's activity filtered out → bare ticket only.
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}
	if activities[0].SourceID != "linear:issue-other" {
		t.Errorf("SourceID = %q, want bare ticket", activities[0].SourceID)
	}
}

func TestCollectSince_Pagination(t *testing.T) {
	callCount := 0
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++

		var req gqlRequest
		json.NewDecoder(r.Body).Decode(&req)

		vars, _ := req.Variables.(map[string]any)
		_, hasAfter := vars["after"]

		if !hasAfter {
			// First page: return pageSize items.
			nodes := make([]linearIssue, pageSize)
			for i := range nodes {
				nodes[i] = linearIssue{
					ID:         fmt.Sprintf("issue-%d", i),
					Identifier: fmt.Sprintf("ENG-%d", i),
					Title:      "Paginated issue",
					State:      linearState{Name: "Todo"},
					UpdatedAt:  time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC),
					History: struct {
						Nodes []linearHistoryEntry `json:"nodes"`
					}{},
					Comments: struct {
						Nodes []linearComment `json:"nodes"`
					}{},
				}
			}
			respondJSON(w, gqlResponse{
				Data: mustJSON(issuesResponse{
					Issues: struct {
						Nodes    []linearIssue `json:"nodes"`
						PageInfo pageInfo      `json:"pageInfo"`
					}{
						Nodes:    nodes,
						PageInfo: pageInfo{HasNextPage: true, EndCursor: "cursor-1"},
					},
				}),
			})
		} else {
			// Second page: 3 remaining items.
			nodes := make([]linearIssue, 3)
			for i := range nodes {
				nodes[i] = linearIssue{
					ID:         fmt.Sprintf("issue-extra-%d", i),
					Identifier: fmt.Sprintf("ENG-EXTRA-%d", i),
					Title:      "Extra issue",
					State:      linearState{Name: "Todo"},
					UpdatedAt:  time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC),
					History: struct {
						Nodes []linearHistoryEntry `json:"nodes"`
					}{},
					Comments: struct {
						Nodes []linearComment `json:"nodes"`
					}{},
				}
			}
			respondJSON(w, gqlResponse{
				Data: mustJSON(issuesResponse{
					Issues: struct {
						Nodes    []linearIssue `json:"nodes"`
						PageInfo pageInfo      `json:"pageInfo"`
					}{
						Nodes:    nodes,
						PageInfo: pageInfo{HasNextPage: false},
					},
				}),
			})
		}
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != pageSize+3 {
		t.Errorf("got %d activities, want %d", len(activities), pageSize+3)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 GraphQL calls, got %d", callCount)
	}
}

func TestCollectSince_EmptyResults(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, gqlResponse{
			Data: mustJSON(issuesResponse{
				Issues: struct {
					Nodes    []linearIssue `json:"nodes"`
					PageInfo pageInfo      `json:"pageInfo"`
				}{
					PageInfo: pageInfo{HasNextPage: false},
				},
			}),
		})
	})

	activities, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	if len(activities) != 0 {
		t.Errorf("got %d activities, want 0", len(activities))
	}
}

func TestCollectSince_RateLimited(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call: viewer query succeeds.
			respondJSON(w, gqlResponse{
				Data: mustJSON(viewerResponse{Viewer: struct{ ID string `json:"id"` }{ID: "user-self"}}),
			})
			return
		}
		// Second call: rate limited.
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewWithClient("token", srv.URL, srv.Client())
	c.userID = "" // force viewer call

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := c.CollectSince(ctx, time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestCollectSince_GraphQLError(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, gqlResponse{
			Errors: []gqlError{{Message: "Query complexity exceeded"}},
		})
	})

	_, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected GraphQL error")
	}
}

func TestCollectSince_HTTPError(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})

	_, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected HTTP error")
	}
}

func TestEnsureUserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_test_token" {
			t.Error("missing or wrong Authorization header")
		}
		respondJSON(w, gqlResponse{
			Data: mustJSON(viewerResponse{Viewer: struct{ ID string `json:"id"` }{ID: "viewer-123"}}),
		})
	}))
	defer srv.Close()

	c := NewWithClient("lin_test_token", srv.URL, srv.Client())

	if err := c.ensureUserID(context.Background()); err != nil {
		t.Fatalf("ensureUserID: %v", err)
	}
	if c.userID != "viewer-123" {
		t.Errorf("userID = %q, want %q", c.userID, "viewer-123")
	}

	// Second call should use cache.
	c.graphqlURL = "http://invalid"
	if err := c.ensureUserID(context.Background()); err != nil {
		t.Fatalf("second ensureUserID should use cache: %v", err)
	}
}

func TestLabelNames(t *testing.T) {
	labels := []linearLabel{{Name: "bug"}, {Name: "frontend"}}
	names := labelNames(labels)
	if len(names) != 2 || names[0] != "bug" || names[1] != "frontend" {
		t.Errorf("labelNames = %v", names)
	}

	if labelNames(nil) != nil {
		t.Error("labelNames(nil) should return nil")
	}
}

// mustJSON marshals v to json.RawMessage.
func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

