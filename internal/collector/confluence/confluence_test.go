package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
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
	if c.Name() != models.SourceConfluence {
		t.Errorf("Name() = %q, want %q", c.Name(), models.SourceConfluence)
	}
}

func TestCollectSince_PageCreatedByUser(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/content/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult{
				Results: []contentResult{
					{
						ID:     "12345",
						Type:   "page",
						Title:  "RFC: New API Contract",
						Status: "current",
						Space:  contentSpace{Key: "ENG", Name: "Engineering"},
						History: contentHistory{
							CreatedDate: "2026-04-10T10:00:00.000Z",
							CreatedBy:   contentUser{AccountID: "user-123", DisplayName: "Me"},
							LastUpdated: lastUpdated{
								When: "2026-04-10T14:30:00.000Z",
								By:   contentUser{AccountID: "user-123", DisplayName: "Me"},
							},
						},
						Links: contentLinks{
							WebUI: "/spaces/ENG/pages/12345/RFC+New+API+Contract",
							Base:  "https://mycompany.atlassian.net/wiki",
						},
					},
				},
				Size: 1,
			})
		},
	})

	since := time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(activities))
	}

	a := activities[0]
	if a.Source != models.SourceConfluence {
		t.Errorf("source = %q, want %q", a.Source, models.SourceConfluence)
	}
	if a.Type != models.TypeDocument {
		t.Errorf("type = %q, want %q", a.Type, models.TypeDocument)
	}
	if a.SourceID != "confluence:12345:created" {
		t.Errorf("source_id = %q, want %q", a.SourceID, "confluence:12345:created")
	}
	if a.Title != "Created page in ENG: RFC: New API Contract" {
		t.Errorf("title = %q", a.Title)
	}

	var meta pageMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("metadata parse: %v", err)
	}
	if meta.Action != "created" {
		t.Errorf("action = %q, want %q", meta.Action, "created")
	}
	if meta.SpaceKey != "ENG" {
		t.Errorf("space_key = %q, want %q", meta.SpaceKey, "ENG")
	}
	if meta.URL != "https://mycompany.atlassian.net/wiki/spaces/ENG/pages/12345/RFC+New+API+Contract" {
		t.Errorf("url = %q", meta.URL)
	}
}

func TestCollectSince_PageUpdatedByUser(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/content/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult{
				Results: []contentResult{
					{
						ID:     "67890",
						Type:   "page",
						Title:  "Architecture Overview",
						Status: "current",
						Space:  contentSpace{Key: "ARCH", Name: "Architecture"},
						History: contentHistory{
							CreatedDate: "2026-01-15T10:00:00.000Z",
							CreatedBy:   contentUser{AccountID: "other-user", DisplayName: "Alice"},
							LastUpdated: lastUpdated{
								When: "2026-04-11T09:00:00.000Z",
								By:   contentUser{AccountID: "user-123", DisplayName: "Me"},
							},
						},
					},
				},
				Size: 1,
			})
		},
	})

	since := time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)
	activities, err := c.CollectSince(context.Background(), since)
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(activities))
	}

	a := activities[0]
	if a.SourceID != "confluence:67890:updated" {
		t.Errorf("source_id = %q, want %q", a.SourceID, "confluence:67890:updated")
	}

	var meta pageMeta
	json.Unmarshal([]byte(a.Metadata), &meta)
	if meta.Action != "updated" {
		t.Errorf("action = %q, want %q", meta.Action, "updated")
	}
}

func TestCollectSince_FiltersOtherUsersPages(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/content/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult{
				Results: []contentResult{
					{
						ID:    "11111",
						Type:  "page",
						Title: "Someone else's page",
						History: contentHistory{
							CreatedBy:   contentUser{AccountID: "other-user"},
							LastUpdated: lastUpdated{By: contentUser{AccountID: "other-user"}},
						},
					},
					{
						ID:    "22222",
						Type:  "page",
						Title: "My page",
						Space: contentSpace{Key: "DEV"},
						History: contentHistory{
							CreatedDate: "2026-04-10T10:00:00.000Z",
							CreatedBy:   contentUser{AccountID: "user-123"},
							LastUpdated: lastUpdated{
								When: "2026-04-10T10:00:00.000Z",
								By:   contentUser{AccountID: "user-123"},
							},
						},
					},
				},
				Size: 2,
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("expected 1 activity (filtered other user), got %d", len(activities))
	}
	if activities[0].SourceID != "confluence:22222:created" {
		t.Errorf("expected user's own page, got %q", activities[0].SourceID)
	}
}

func TestCollectSince_BlogPost(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/content/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult{
				Results: []contentResult{
					{
						ID:    "33333",
						Type:  "blogpost",
						Title: "Q1 Engineering Update",
						Space: contentSpace{Key: "BLOG", Name: "Blog"},
						History: contentHistory{
							CreatedDate: "2026-04-10T10:00:00.000Z",
							CreatedBy:   contentUser{AccountID: "user-123"},
							LastUpdated: lastUpdated{
								When: "2026-04-10T10:00:00.000Z",
								By:   contentUser{AccountID: "user-123"},
							},
						},
					},
				},
				Size: 1,
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(activities))
	}

	var meta pageMeta
	json.Unmarshal([]byte(activities[0].Metadata), &meta)
	if meta.PageType != "blogpost" {
		t.Errorf("page_type = %q, want %q", meta.PageType, "blogpost")
	}
}

func TestCollectSince_EmptyResults(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/content/search": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(searchResult{Results: nil, Size: 0})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	if len(activities) != 0 {
		t.Errorf("expected 0 activities, got %d", len(activities))
	}
}

func TestCollectSince_Pagination(t *testing.T) {
	callCount := 0
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/content/search": func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				// First page: return full page of results.
				results := make([]contentResult, perPage)
				for i := range results {
					results[i] = contentResult{
						ID:    fmt.Sprintf("page-%d", i),
						Type:  "page",
						Title: fmt.Sprintf("Page %d", i),
						Space: contentSpace{Key: "ENG"},
						History: contentHistory{
							CreatedDate: "2026-04-10T10:00:00.000Z",
							CreatedBy:   contentUser{AccountID: "user-123"},
							LastUpdated: lastUpdated{
								When: "2026-04-10T10:00:00.000Z",
								By:   contentUser{AccountID: "user-123"},
							},
						},
					}
				}
				json.NewEncoder(w).Encode(searchResult{Results: results, Size: perPage})
			} else {
				// Second page: fewer results (end of pagination).
				json.NewEncoder(w).Encode(searchResult{
					Results: []contentResult{
						{
							ID:    "page-last",
							Type:  "page",
							Title: "Last Page",
							Space: contentSpace{Key: "ENG"},
							History: contentHistory{
								CreatedDate: "2026-04-10T10:00:00.000Z",
								CreatedBy:   contentUser{AccountID: "user-123"},
								LastUpdated: lastUpdated{
									When: "2026-04-10T10:00:00.000Z",
									By:   contentUser{AccountID: "user-123"},
								},
							},
						},
					},
					Size: 1,
				})
			}
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls (pagination), got %d", callCount)
	}
	if len(activities) != perPage+1 {
		t.Errorf("expected %d activities, got %d", perPage+1, len(activities))
	}
}

func TestCollectSince_APIError(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/content/search": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		},
	})

	_, err := c.CollectSince(context.Background(), time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestEnsureAccountID(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/user/current": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(myselfResult{AccountID: "user-abc"})
		},
	})
	c.accountID = "" // clear pre-set value

	if err := c.ensureAccountID(context.Background()); err != nil {
		t.Fatalf("ensureAccountID: %v", err)
	}
	if c.accountID != "user-abc" {
		t.Errorf("accountID = %q, want %q", c.accountID, "user-abc")
	}

	// Second call should be cached (no API hit).
	c.accountID = "cached"
	if err := c.ensureAccountID(context.Background()); err != nil {
		t.Fatalf("ensureAccountID (cached): %v", err)
	}
	if c.accountID != "cached" {
		t.Errorf("accountID should be cached, got %q", c.accountID)
	}
}

func TestBasicAuth(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(searchResult{Size: 0})
	}))
	defer srv.Close()

	c := NewWithAPIToken("user@example.com", "api-token", srv.URL)
	c.accountID = "user-123"
	c.client = srv.Client()

	c.CollectSince(context.Background(), time.Now().Add(-24*time.Hour))

	if authHeader == "" {
		t.Fatal("expected Authorization header")
	}
	// Basic Auth should NOT be "Bearer".
	if len(authHeader) > 6 && authHeader[:6] == "Bearer" {
		t.Errorf("expected Basic auth, got Bearer")
	}
	if len(authHeader) < 6 || authHeader[:5] != "Basic" {
		t.Errorf("expected Basic auth header, got %q", authHeader)
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"created", "Created"},
		{"updated", "Updated"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := capitalize(tt.in); got != tt.want {
			t.Errorf("capitalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
