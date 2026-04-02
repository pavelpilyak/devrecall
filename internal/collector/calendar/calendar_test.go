package calendar

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

	c := NewWithClient("ya29.test-token", srv.URL, srv.Client())
	return srv, c
}

func makeEvent(id, summary string, start, end time.Time, attendees []apiAttendee) calendarEvent {
	return calendarEvent{
		ID:      id,
		Status:  "confirmed",
		Summary: summary,
		Start:   eventDateTime{DateTime: start.Format(time.RFC3339)},
		End:     eventDateTime{DateTime: end.Format(time.RFC3339)},
		Attendees: attendees,
		Organizer: apiOrganizer{Email: "organizer@example.com"},
	}
}

func makeAllDayEvent(id, summary, startDate, endDate string) calendarEvent {
	return calendarEvent{
		ID:      id,
		Status:  "confirmed",
		Summary: summary,
		Start:   eventDateTime{Date: startDate},
		End:     eventDateTime{Date: endDate},
	}
}

func eventsHandler(events []calendarEvent, syncToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ya29.test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		resp := eventsResponse{Items: events, NextSyncToken: syncToken}
		json.NewEncoder(w).Encode(resp)
	}
}

func TestCollect_BasicEvents(t *testing.T) {
	start := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 27, 10, 30, 0, 0, time.UTC)

	events := []calendarEvent{
		makeEvent("evt1", "1:1 with Sarah", start, end, []apiAttendee{
			{Email: "me@example.com", Self: true, ResponseStatus: "accepted"},
			{Email: "sarah@example.com", DisplayName: "Sarah", ResponseStatus: "accepted"},
		}),
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler(events, ""),
	})

	activities, err := c.CollectRange(ctx(), start.Add(-time.Hour), end.Add(time.Hour))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Source != models.SourceCalendar {
		t.Errorf("Source = %q, want %q", a.Source, models.SourceCalendar)
	}
	if a.Type != models.TypeMeeting {
		t.Errorf("Type = %q, want %q", a.Type, models.TypeMeeting)
	}
	if a.Title != "1:1 with Sarah" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.SourceID != "calendar:primary:evt1" {
		t.Errorf("SourceID = %q", a.SourceID)
	}

	var meta eventMeta
	if err := json.Unmarshal([]byte(a.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.DurationMin != 30 {
		t.Errorf("DurationMin = %d, want 30", meta.DurationMin)
	}
	if len(meta.Attendees) != 2 {
		t.Errorf("Attendees = %d, want 2", len(meta.Attendees))
	}
	if meta.ResponseStatus != "accepted" {
		t.Errorf("ResponseStatus = %q, want accepted", meta.ResponseStatus)
	}
}

func TestCollect_MultipleEvents(t *testing.T) {
	base := time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC)
	events := []calendarEvent{
		makeEvent("evt1", "Standup", base.Add(9*time.Hour), base.Add(9*time.Hour+15*time.Minute), nil),
		makeEvent("evt2", "Sprint Planning", base.Add(10*time.Hour), base.Add(11*time.Hour), nil),
		makeEvent("evt3", "1:1 with Manager", base.Add(14*time.Hour), base.Add(14*time.Hour+30*time.Minute), nil),
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler(events, ""),
	})

	activities, err := c.CollectRange(ctx(), base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	if len(activities) != 3 {
		t.Fatalf("got %d activities, want 3", len(activities))
	}
}

func TestCollect_AllDayEvent(t *testing.T) {
	events := []calendarEvent{
		makeAllDayEvent("evt-allday", "Company Offsite", "2026-03-27", "2026-03-28"),
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler(events, ""),
	})

	activities, err := c.CollectRange(ctx(),
		time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	var meta eventMeta
	json.Unmarshal([]byte(activities[0].Metadata), &meta)
	if !meta.IsAllDay {
		t.Error("expected IsAllDay to be true")
	}
}

func TestCollect_CancelledEventsSkipped(t *testing.T) {
	start := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	events := []calendarEvent{
		makeEvent("evt1", "Active Meeting", start, start.Add(time.Hour), nil),
		{ID: "evt2", Status: "cancelled", Summary: "Cancelled Meeting",
			Start: eventDateTime{DateTime: start.Format(time.RFC3339)},
			End:   eventDateTime{DateTime: start.Add(time.Hour).Format(time.RFC3339)}},
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler(events, ""),
	})

	activities, err := c.CollectRange(ctx(), start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1 (cancelled should be skipped)", len(activities))
	}
	if activities[0].Title != "Active Meeting" {
		t.Errorf("Title = %q", activities[0].Title)
	}
}

func TestCollect_NoTitleEvent(t *testing.T) {
	start := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	events := []calendarEvent{
		makeEvent("evt1", "", start, start.Add(time.Hour), nil),
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler(events, ""),
	})

	activities, err := c.CollectRange(ctx(), start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	if activities[0].Title != "(No title)" {
		t.Errorf("Title = %q, want '(No title)'", activities[0].Title)
	}
}

func TestCollect_EmptyResults(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler(nil, ""),
	})

	activities, err := c.CollectRange(ctx(),
		time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}
	if len(activities) != 0 {
		t.Errorf("got %d activities, want 0", len(activities))
	}
}

func TestCollect_ConferenceLink(t *testing.T) {
	start := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	ev := makeEvent("evt1", "Video Call", start, start.Add(time.Hour), nil)
	ev.ConferenceData = &confData{
		EntryPoints: []entryPoint{
			{EntryPointType: "video", URI: "https://meet.google.com/abc-def-ghi"},
		},
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler([]calendarEvent{ev}, ""),
	})

	activities, err := c.CollectRange(ctx(), start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	var meta eventMeta
	json.Unmarshal([]byte(activities[0].Metadata), &meta)
	if meta.ConferenceLink != "https://meet.google.com/abc-def-ghi" {
		t.Errorf("ConferenceLink = %q", meta.ConferenceLink)
	}
}

func TestCollect_RecurringEvent(t *testing.T) {
	start := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	ev := makeEvent("evt1", "Weekly 1:1", start, start.Add(30*time.Minute), nil)
	ev.RecurringEvent = "recurring-base-id"

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler([]calendarEvent{ev}, ""),
	})

	activities, err := c.CollectRange(ctx(), start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	var meta eventMeta
	json.Unmarshal([]byte(activities[0].Metadata), &meta)
	if !meta.IsRecurring {
		t.Error("expected IsRecurring to be true")
	}
}

func TestInitialSync_ReturnsSyncToken(t *testing.T) {
	start := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	events := []calendarEvent{
		makeEvent("evt1", "Meeting", start, start.Add(time.Hour), nil),
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": eventsHandler(events, "sync-token-abc123"),
	})

	activities, token, err := c.InitialSync(ctx(), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("InitialSync: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}
	if token != "sync-token-abc123" {
		t.Errorf("syncToken = %q, want %q", token, "sync-token-abc123")
	}
}

func TestCollectWithSyncToken_IncrementalSync(t *testing.T) {
	start := time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC)
	events := []calendarEvent{
		makeEvent("evt-new", "New Meeting", start, start.Add(time.Hour), nil),
	}

	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": func(w http.ResponseWriter, r *http.Request) {
			syncToken := r.URL.Query().Get("syncToken")
			if syncToken != "old-sync-token" {
				t.Errorf("syncToken = %q, want %q", syncToken, "old-sync-token")
			}
			resp := eventsResponse{Items: events, NextSyncToken: "new-sync-token"}
			json.NewEncoder(w).Encode(resp)
		},
	})

	activities, newToken, err := c.CollectWithSyncToken(ctx(), "old-sync-token")
	if err != nil {
		t.Fatalf("CollectWithSyncToken: %v", err)
	}

	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}
	if newToken != "new-sync-token" {
		t.Errorf("newToken = %q, want %q", newToken, "new-sync-token")
	}
}

func TestCollectWithSyncToken_Expired(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusGone)
		},
	})

	_, _, err := c.CollectWithSyncToken(ctx(), "expired-token")
	if err != ErrSyncTokenExpired {
		t.Errorf("err = %v, want ErrSyncTokenExpired", err)
	}
}

func TestCollect_Pagination(t *testing.T) {
	start := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	page1Events := []calendarEvent{
		makeEvent("evt1", "Meeting 1", start, start.Add(time.Hour), nil),
	}
	page2Events := []calendarEvent{
		makeEvent("evt2", "Meeting 2", start.Add(2*time.Hour), start.Add(3*time.Hour), nil),
	}

	callCount := 0
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": func(w http.ResponseWriter, r *http.Request) {
			callCount++
			pageToken := r.URL.Query().Get("pageToken")
			var resp eventsResponse
			if pageToken == "" {
				resp = eventsResponse{Items: page1Events, NextPageToken: "page2"}
			} else {
				resp = eventsResponse{Items: page2Events}
			}
			json.NewEncoder(w).Encode(resp)
		},
	})

	activities, err := c.CollectRange(ctx(), start.Add(-time.Hour), start.Add(4*time.Hour))
	if err != nil {
		t.Fatalf("CollectRange: %v", err)
	}

	if len(activities) != 2 {
		t.Fatalf("got %d activities, want 2", len(activities))
	}
	if callCount != 2 {
		t.Errorf("API called %d times, want 2", callCount)
	}
}

func TestCollect_APIError(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error": {"message": "invalid_token"}}`, http.StatusUnauthorized)
		},
	})

	_, err := c.CollectRange(ctx(),
		time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestCollect_RateLimited(t *testing.T) {
	_, c := newTestServer(t, map[string]http.HandlerFunc{
		"/calendar/v3/calendars/primary/events": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})

	_, err := c.CollectRange(ctx(),
		time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error on rate limit")
	}
}

func TestName(t *testing.T) {
	c := New("token")
	if c.Name() != models.SourceCalendar {
		t.Errorf("Name() = %q, want %q", c.Name(), models.SourceCalendar)
	}
}

func TestParseEventTimes_Timed(t *testing.T) {
	ev := calendarEvent{
		Start: eventDateTime{DateTime: "2026-03-27T10:00:00Z"},
		End:   eventDateTime{DateTime: "2026-03-27T11:00:00Z"},
	}

	start, end, isAllDay := parseEventTimes(ev)
	if isAllDay {
		t.Error("expected not all-day")
	}
	if start.Hour() != 10 || end.Hour() != 11 {
		t.Errorf("start=%v, end=%v", start, end)
	}
}

func TestParseEventTimes_AllDay(t *testing.T) {
	ev := calendarEvent{
		Start: eventDateTime{Date: "2026-03-27"},
		End:   eventDateTime{Date: "2026-03-28"},
	}

	start, end, isAllDay := parseEventTimes(ev)
	if !isAllDay {
		t.Error("expected all-day")
	}
	if start.Day() != 27 || end.Day() != 28 {
		t.Errorf("start=%v, end=%v", start, end)
	}
}

func ctx() context.Context {
	return context.Background()
}
