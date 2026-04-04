package jira

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

	c := NewWithClient("test-token", srv.URL, true, srv.Client())
	c.accountID = "user-123" // pre-set to skip /myself call in most tests
	return srv, c
}

func TestName(t *testing.T) {
	c := New("token", "cloud-id")
	if c.Name() != models.SourceJira {
		t.Errorf("Name() = %q, want %q", c.Name(), models.SourceJira)
	}
}

func TestCollectSince_IssueWithStatusTransition(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraSearchResult{
				Total: 1,
				Issues: []jiraIssue{
					{
						ID:  "10001",
						Key: "PROJ-123",
						Fields: jiraFields{
							Summary:  "Fix payment retry logic",
							Status:   jiraStatus{Name: "In Review"},
							Priority: jiraPriority{Name: "High"},
							Labels:   []string{"backend", "payments"},
							Project:  jiraProject{Key: "PROJ", Name: "Project"},
							Updated:  time.Date(2026, 4, 4, 14, 0, 0, 0, time.UTC),
							Sprint:   &jiraSprint{ID: 42, Name: "Sprint 42", State: "active"},
						},
					},
				},
			})
		},
		"/rest/api/3/issue/PROJ-123/changelog": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraChangelog{
				Total: 1,
				Values: []jiraChangeItem{
					{
						ID:      "10456",
						Author:  jiraChangeAuthor{AccountID: "user-123"},
						Created: time.Date(2026, 4, 4, 14, 0, 0, 0, time.UTC),
						Items: []jiraChangeDetail{
							{
								Field:      "status",
								FromString: "In Progress",
								ToString:   "In Review",
							},
						},
					},
				},
			})
		},
		"/rest/api/3/issue/PROJ-123/comment": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraCommentResult{Total: 0})
		},
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
	if a.Source != models.SourceJira {
		t.Errorf("Source = %q, want %q", a.Source, models.SourceJira)
	}
	if a.Type != models.TypeTicket {
		t.Errorf("Type = %q, want %q", a.Type, models.TypeTicket)
	}
	if a.SourceID != "jira:PROJ-123:changelog:10456" {
		t.Errorf("SourceID = %q, want %q", a.SourceID, "jira:PROJ-123:changelog:10456")
	}
	if a.Title != "PROJ-123: Fix payment retry logic → moved to In Review" {
		t.Errorf("Title = %q", a.Title)
	}

	var meta ticketMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.FromStatus != "In Progress" {
		t.Errorf("meta.FromStatus = %q, want %q", meta.FromStatus, "In Progress")
	}
	if meta.ToStatus != "In Review" {
		t.Errorf("meta.ToStatus = %q, want %q", meta.ToStatus, "In Review")
	}
	if meta.Sprint != "Sprint 42" {
		t.Errorf("meta.Sprint = %q, want %q", meta.Sprint, "Sprint 42")
	}
	if meta.Priority != "High" {
		t.Errorf("meta.Priority = %q, want %q", meta.Priority, "High")
	}
	if len(meta.Labels) != 2 {
		t.Errorf("meta.Labels = %v, want [backend payments]", meta.Labels)
	}
}

func TestCollectSince_Comment(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraSearchResult{
				Total: 1,
				Issues: []jiraIssue{
					{
						ID:  "10001",
						Key: "PROJ-456",
						Fields: jiraFields{
							Summary:  "Update error messages",
							Status:   jiraStatus{Name: "Done"},
							Priority: jiraPriority{Name: "Medium"},
							Project:  jiraProject{Key: "PROJ"},
							Updated:  time.Date(2026, 4, 4, 16, 0, 0, 0, time.UTC),
						},
					},
				},
			})
		},
		"/rest/api/3/issue/PROJ-456/changelog": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraChangelog{Total: 0})
		},
		"/rest/api/3/issue/PROJ-456/comment": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraCommentResult{
				Total: 1,
				Comments: []jiraComment{
					{
						ID:      "10789",
						Author:  jiraCommentAuthor{AccountID: "user-123"},
						Body:    "Updated the retry backoff to use exponential delay.",
						Created: time.Date(2026, 4, 4, 15, 30, 0, 0, time.UTC),
					},
				},
			})
		},
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
	if a.SourceID != "jira:PROJ-456:comment:10789" {
		t.Errorf("SourceID = %q", a.SourceID)
	}
	if a.Title != "Commented on PROJ-456: Update error messages" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.Content != "Updated the retry backoff to use exponential delay." {
		t.Errorf("Content = %q", a.Content)
	}

	var meta commentMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.IssueKey != "PROJ-456" {
		t.Errorf("meta.IssueKey = %q", meta.IssueKey)
	}
	if meta.CommentID != "10789" {
		t.Errorf("meta.CommentID = %q", meta.CommentID)
	}
}

func TestCollectSince_IssueWithNoActivity(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraSearchResult{
				Total: 1,
				Issues: []jiraIssue{
					{
						ID:  "10001",
						Key: "PROJ-789",
						Fields: jiraFields{
							Summary:  "Just updated issue",
							Status:   jiraStatus{Name: "Open"},
							Priority: jiraPriority{Name: "Low"},
							Project:  jiraProject{Key: "PROJ"},
							Updated:  time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC),
						},
					},
				},
			})
		},
		"/rest/api/3/issue/PROJ-789/changelog": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraChangelog{Total: 0})
		},
		"/rest/api/3/issue/PROJ-789/comment": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraCommentResult{Total: 0})
		},
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	// Should emit a bare ticket activity when no transitions or comments.
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.SourceID != "jira:PROJ-789" {
		t.Errorf("SourceID = %q, want %q", a.SourceID, "jira:PROJ-789")
	}
	if a.Title != "PROJ-789: Just updated issue" {
		t.Errorf("Title = %q", a.Title)
	}
}

func TestCollectSince_FiltersOtherUsersActivity(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraSearchResult{
				Total: 1,
				Issues: []jiraIssue{
					{
						ID:  "10001",
						Key: "PROJ-100",
						Fields: jiraFields{
							Summary:  "Some issue",
							Status:   jiraStatus{Name: "Done"},
							Priority: jiraPriority{Name: "Medium"},
							Project:  jiraProject{Key: "PROJ"},
							Updated:  time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC),
						},
					},
				},
			})
		},
		"/rest/api/3/issue/PROJ-100/changelog": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraChangelog{
				Total: 1,
				Values: []jiraChangeItem{
					{
						ID:      "999",
						Author:  jiraChangeAuthor{AccountID: "other-user"},
						Created: time.Date(2026, 4, 4, 11, 0, 0, 0, time.UTC),
						Items:   []jiraChangeDetail{{Field: "status", FromString: "Open", ToString: "Done"}},
					},
				},
			})
		},
		"/rest/api/3/issue/PROJ-100/comment": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraCommentResult{
				Total: 1,
				Comments: []jiraComment{
					{
						ID:      "888",
						Author:  jiraCommentAuthor{AccountID: "other-user"},
						Body:    "Someone else's comment",
						Created: time.Date(2026, 4, 4, 11, 30, 0, 0, time.UTC),
					},
				},
			})
		},
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	// Other user's transitions and comments should be filtered out,
	// leaving only the bare ticket activity.
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}
	if activities[0].SourceID != "jira:PROJ-100" {
		t.Errorf("SourceID = %q, want bare ticket", activities[0].SourceID)
	}
}

func TestCollectSince_Pagination(t *testing.T) {
	callCount := 0
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			callCount++
			startAt := r.URL.Query().Get("startAt")

			if startAt == "" || startAt == "0" {
				// First page: return perPage items.
				issues := make([]jiraIssue, perPage)
				for i := range issues {
					issues[i] = jiraIssue{
						ID:  "1000" + startAt,
						Key: "PROJ-" + r.URL.Query().Get("startAt") + "-" + string(rune('A'+i)),
						Fields: jiraFields{
							Summary:  "Issue",
							Status:   jiraStatus{Name: "Open"},
							Priority: jiraPriority{Name: "Medium"},
							Project:  jiraProject{Key: "PROJ"},
							Updated:  time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC),
						},
					}
				}
				json.NewEncoder(w).Encode(jiraSearchResult{
					Total:  perPage + 3,
					Issues: issues,
				})
			} else {
				// Second page: 3 remaining items.
				issues := make([]jiraIssue, 3)
				for i := range issues {
					issues[i] = jiraIssue{
						ID:  "2000",
						Key: "PROJ-EXTRA-" + string(rune('A'+i)),
						Fields: jiraFields{
							Summary:  "Extra issue",
							Status:   jiraStatus{Name: "Open"},
							Priority: jiraPriority{Name: "Low"},
							Project:  jiraProject{Key: "PROJ"},
							Updated:  time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC),
						},
					}
				}
				json.NewEncoder(w).Encode(jiraSearchResult{
					Total:  perPage + 3,
					Issues: issues,
				})
			}
		},
		// Return empty for all changelog and comment requests.
		"/rest/api/3/issue/": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"total": 0, "values": []any{}, "comments": []any{}})
		},
	})

	since := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	// Should have all perPage+3 issues as bare ticket activities.
	if len(activities) != perPage+3 {
		t.Errorf("got %d activities, want %d", len(activities), perPage+3)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 search API calls, got %d", callCount)
	}
}

func TestCollectSince_EmptyResults(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraSearchResult{Total: 0})
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

func TestCollectSince_RateLimited(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/myself": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(jiraMyself{AccountID: "user-123"})
		},
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})

	// Clear accountID to test full flow including /myself.
	c.accountID = ""

	_, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestCollectSince_APIError(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/search": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"internal error"}`))
		},
	})

	_, err := c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))
	if err == nil {
		t.Error("expected API error")
	}
}

func TestEnsureAccountID(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/3/myself": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Error("missing or wrong Authorization header")
			}
			json.NewEncoder(w).Encode(jiraMyself{
				AccountID:    "acc-456",
				EmailAddress: "dev@example.com",
				DisplayName:  "Dev User",
			})
		},
	})
	c.accountID = "" // reset

	err := c.ensureAccountID(context.Background())
	if err != nil {
		t.Fatalf("ensureAccountID: %v", err)
	}
	if c.accountID != "acc-456" {
		t.Errorf("accountID = %q, want %q", c.accountID, "acc-456")
	}

	// Second call should not hit API (cached).
	c.baseURL = "http://invalid" // would fail if called
	err = c.ensureAccountID(context.Background())
	if err != nil {
		t.Fatalf("second ensureAccountID should use cache: %v", err)
	}
}

func TestBasicAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(jiraMyself{AccountID: "user-123"})
	}))
	defer srv.Close()

	c := NewWithClient("api-token", srv.URL, false, srv.Client())
	c.email = "dev@example.com"
	c.accountID = ""

	c.ensureAccountID(context.Background())

	if gotAuth == "" {
		t.Error("expected Authorization header")
	}
	if gotAuth == "Bearer api-token" {
		t.Error("should use Basic auth, not Bearer")
	}
}

func TestExtractCommentText(t *testing.T) {
	tests := []struct {
		name string
		body any
		want string
	}{
		{
			name: "plain string",
			body: "Simple comment text",
			want: "Simple comment text",
		},
		{
			name: "ADF with text",
			body: map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{"type": "text", "text": "Hello"},
							map[string]any{"type": "text", "text": " world"},
						},
					},
				},
			},
			want: "Hello world",
		},
		{
			name: "nil body",
			body: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCommentText(tt.body)
			if got != tt.want {
				t.Errorf("extractCommentText() = %q, want %q", got, tt.want)
			}
		})
	}
}
