package slack

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

// newTestServer creates a mock Slack API server with the given handlers.
func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) (*httptest.Server, *Collector) {
	t.Helper()
	mux := http.NewServeMux()
	for path, handler := range handlers {
		mux.HandleFunc(path, handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewWithClient("xoxp-test-token", srv.URL, srv.Client())
	return srv, c
}

// makeMatch is a helper to build searchMatch without repeating the anonymous struct literal.
func makeMatch(ts, text, channelID, channelName string) searchMatch {
	m := searchMatch{TS: ts, Text: text}
	m.Channel.ID = channelID
	m.Channel.Name = channelName
	return m
}

func makeSearchResp(total int, matches []searchMatch) searchResponse {
	var resp searchResponse
	resp.OK = true
	resp.Messages.Total = total
	resp.Messages.Matches = matches
	return resp
}

func TestCollect_BasicMessages(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer xoxp-test-token" {
				t.Error("missing or wrong Authorization header")
			}
			m1 := makeMatch("1711584000.000100", "Let's switch to blue-green deployments", "C1234", "backend")
			m1.Permalink = "https://team.slack.com/archives/C1234/p1711584000000100"
			m2 := makeMatch("1711584060.000200", "Reviewed the PR, LGTM", "C5678", "code-review")
			m2.Permalink = "https://team.slack.com/archives/C5678/p1711584060000200"
			json.NewEncoder(w).Encode(makeSearchResp(2, []searchMatch{m1, m2}))
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 2 {
		t.Fatalf("got %d activities, want 2", len(activities))
	}

	a := activities[0]
	if a.Source != models.SourceSlack {
		t.Errorf("Source = %q, want %q", a.Source, models.SourceSlack)
	}
	if a.Type != models.TypeMessage {
		t.Errorf("Type = %q, want %q", a.Type, models.TypeMessage)
	}
	if a.Title != "Message in #backend" {
		t.Errorf("Title = %q, want %q", a.Title, "Message in #backend")
	}
	if a.Content != "Let's switch to blue-green deployments" {
		t.Errorf("Content = %q", a.Content)
	}
	if a.SourceID != "slack:C1234:1711584000.000100" {
		t.Errorf("SourceID = %q", a.SourceID)
	}

	var meta messageMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.ChannelName != "backend" {
		t.Errorf("metadata.ChannelName = %q", meta.ChannelName)
	}
}

func TestCollect_ThreadParent(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			m := makeMatch("1711584000.000100", "Should we use blue-green?", "C1234", "backend")
			m.ThreadTS = "1711584000.000100" // same as TS = thread parent
			m.ReplyCount = 5
			json.NewEncoder(w).Encode(makeSearchResp(1, []searchMatch{m}))
		},
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("channel") != "C1234" {
				t.Errorf("wrong channel: %s", r.URL.Query().Get("channel"))
			}
			json.NewEncoder(w).Encode(threadResponse{
				OK: true,
				Messages: []threadMessage{
					{User: "U001", Text: "Should we use blue-green?", TS: "1711584000.000100"},
					{User: "U002", Text: "Yes, let's do it", TS: "1711584010.000200"},
					{User: "U003", Text: "+1", TS: "1711584020.000300"},
				},
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Title != "Thread in #backend (2 replies)" {
		t.Errorf("Title = %q", a.Title)
	}

	var meta messageMeta
	json.Unmarshal([]byte(a.Metadata), &meta)
	if meta.ReplyCount != 2 {
		t.Errorf("ReplyCount = %d, want 2", meta.ReplyCount)
	}
	if len(meta.Participants) != 3 {
		t.Errorf("Participants = %d, want 3", len(meta.Participants))
	}
}

func TestCollect_ThreadReplyGrouped(t *testing.T) {
	// When search returns both a parent and a reply from the same thread,
	// they should be grouped into a single thread activity.
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			parent := makeMatch("1711584000.000100", "Should we use blue-green?", "C1234", "backend")
			parent.ThreadTS = "1711584000.000100"
			reply := makeMatch("1711584010.000200", "I agree, let's do it", "C1234", "backend")
			reply.ThreadTS = "1711584000.000100"
			json.NewEncoder(w).Encode(makeSearchResp(2, []searchMatch{parent, reply}))
		},
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(threadResponse{
				OK: true,
				Messages: []threadMessage{
					{User: "U001", Text: "Should we use blue-green?", TS: "1711584000.000100"},
					{User: "U002", Text: "I agree, let's do it", TS: "1711584010.000200"},
				},
			})
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1 (thread grouped)", len(activities))
	}

	a := activities[0]
	if a.SourceID != "slack:C1234:1711584000.000100" {
		t.Errorf("SourceID = %q, want thread parent TS", a.SourceID)
	}
	if a.Title != "Thread in #backend (1 replies)" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.Content != "Should we use blue-green?" {
		t.Errorf("Content = %q, want parent text", a.Content)
	}

	var meta messageMeta
	json.Unmarshal([]byte(a.Metadata), &meta)
	if meta.ReplyCount != 1 {
		t.Errorf("ReplyCount = %d, want 1", meta.ReplyCount)
	}
	if len(meta.ThreadMsgs) != 2 {
		t.Errorf("ThreadMsgs = %d, want 2", len(meta.ThreadMsgs))
	}
}

func TestCollect_Deduplication(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			m := makeMatch("1711584000.000100", "Hello", "C1", "general")
			json.NewEncoder(w).Encode(makeSearchResp(2, []searchMatch{m, m}))
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != 1 {
		t.Errorf("got %d activities, want 1 (deduplication)", len(activities))
	}
}

func TestCollect_APIError(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			var resp searchResponse
			resp.OK = false
			resp.Error = "invalid_auth"
			json.NewEncoder(w).Encode(resp)
		},
	})

	_, err := c.CollectSince(context.Background(), time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error for invalid_auth")
	}
}

func TestCollect_RateLimited(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := c.CollectSince(ctx, time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error on rate limit")
	}
}

func TestCollect_Pagination(t *testing.T) {
	callCount := 0
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			callCount++
			page := r.URL.Query().Get("page")

			var matches []searchMatch
			if page == "1" || page == "" {
				for i := 0; i < pageSize; i++ {
					matches = append(matches, makeMatch(
						fmt.Sprintf("1711584%03d.000000", i),
						fmt.Sprintf("Message %d", i),
						"C1", "general",
					))
				}
			} else {
				matches = []searchMatch{makeMatch("1711585000.000000", "Last message", "C1", "general")}
			}

			json.NewEncoder(w).Encode(makeSearchResp(pageSize+1, matches))
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}

	if len(activities) != pageSize+1 {
		t.Errorf("got %d activities, want %d", len(activities), pageSize+1)
	}
	if callCount != 2 {
		t.Errorf("API called %d times, want 2", callCount)
	}
}

func TestCollect_EmptyResults(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/search.messages": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(makeSearchResp(0, nil))
		},
	})

	activities, err := c.CollectSince(context.Background(), time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectSince: %v", err)
	}
	if len(activities) != 0 {
		t.Errorf("got %d activities, want 0", len(activities))
	}
}

func TestTsToTime(t *testing.T) {
	tests := []struct {
		ts   string
		want int64
	}{
		{"1711584000.000100", 1711584000},
		{"1711584000", 1711584000},
		{"0.000000", 0},
	}
	for _, tt := range tests {
		got := tsToTime(tt.ts)
		if got.Unix() != tt.want {
			t.Errorf("tsToTime(%q) = %d, want %d", tt.ts, got.Unix(), tt.want)
		}
	}
}

func TestGetUserProfile(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("user") != "U04ABC" {
				t.Errorf("wrong user param: %s", r.URL.Query().Get("user"))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"user": map[string]any{
					"id": "U04ABC",
					"profile": map[string]any{
						"email":     "pavel@company.com",
						"real_name": "Pavel Piliak",
					},
				},
			})
		},
	})

	profile, err := c.GetUserProfile(context.Background(), "U04ABC")
	if err != nil {
		t.Fatalf("GetUserProfile: %v", err)
	}
	if profile.Email != "pavel@company.com" {
		t.Errorf("Email = %q", profile.Email)
	}
	if profile.Name != "Pavel Piliak" {
		t.Errorf("Name = %q", profile.Name)
	}
	if profile.UserID != "U04ABC" {
		t.Errorf("UserID = %q", profile.UserID)
	}
}

func TestGetUserProfile_APIError(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "user_not_found",
			})
		},
	})

	_, err := c.GetUserProfile(context.Background(), "U99999")
	if err == nil {
		t.Fatal("expected error for user_not_found")
	}
}

func TestName(t *testing.T) {
	c := New("token")
	if c.Name() != models.SourceSlack {
		t.Errorf("Name() = %q, want %q", c.Name(), models.SourceSlack)
	}
}
