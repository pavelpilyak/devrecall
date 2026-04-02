package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

const (
	defaultBaseURL = "https://www.googleapis.com"
	maxResults     = 250
)

// Attendee represents a calendar event attendee.
type Attendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"display_name,omitempty"`
	ResponseStatus string `json:"response_status"` // "accepted", "declined", "tentative", "needsAction"
	Organizer      bool   `json:"organizer,omitempty"`
	Self           bool   `json:"self,omitempty"`
}

// MeetingType classifies a calendar event for standup context.
type MeetingType string

const (
	MeetingOneOnOne MeetingType = "1:1"
	MeetingStandup  MeetingType = "standup"
	MeetingCeremony MeetingType = "ceremony"
	MeetingInterview MeetingType = "interview"
	MeetingFocus    MeetingType = "focus"
	MeetingGroup    MeetingType = "group"
)

// eventMeta is stored as JSON in Activity.Metadata.
type eventMeta struct {
	CalendarID     string      `json:"calendar_id"`
	EventID        string      `json:"event_id"`
	Start          string      `json:"start"`
	End            string      `json:"end"`
	DurationMin    int         `json:"duration_minutes"`
	Attendees      []Attendee  `json:"attendees,omitempty"`
	Organizer      string      `json:"organizer,omitempty"`
	IsRecurring    bool        `json:"is_recurring,omitempty"`
	Location       string      `json:"location,omitempty"`
	ConferenceLink string      `json:"conference_link,omitempty"`
	Status         string      `json:"status"`
	ResponseStatus string      `json:"response_status"`
	IsAllDay       bool        `json:"is_all_day,omitempty"`
	MeetingType    MeetingType `json:"meeting_type"`
}

// ClassifyMeeting determines the meeting type based on title and attendee count.
func ClassifyMeeting(title string, attendeeCount int) MeetingType {
	lower := strings.ToLower(title)

	// Title-based patterns take priority over attendee count.
	focusPatterns := []string{"focus", "no meetings", "do not book", "blocked", "focus time"}
	for _, p := range focusPatterns {
		if strings.Contains(lower, p) {
			return MeetingFocus
		}
	}

	interviewPatterns := []string{"interview"}
	for _, p := range interviewPatterns {
		if strings.Contains(lower, p) {
			return MeetingInterview
		}
	}

	standupPatterns := []string{"standup", "stand-up", "daily", "sync", "check-in", "checkin"}
	for _, p := range standupPatterns {
		if strings.Contains(lower, p) {
			return MeetingStandup
		}
	}

	ceremonyPatterns := []string{"sprint", "retro", "retrospective", "planning", "refinement", "grooming", "demo", "showcase"}
	for _, p := range ceremonyPatterns {
		if strings.Contains(lower, p) {
			return MeetingCeremony
		}
	}

	// Attendee-based: 0 attendees means no data (treat as group), 1-2 is a 1:1.
	if attendeeCount >= 1 && attendeeCount <= 2 {
		return MeetingOneOnOne
	}

	return MeetingGroup
}

// Collector gathers meeting activity from Google Calendar.
type Collector struct {
	token   string
	baseURL string
	client  *http.Client
}

// New creates a Google Calendar collector with the given OAuth access token.
func New(token string) *Collector {
	return &Collector{
		token:   token,
		baseURL: defaultBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient creates a collector with a custom HTTP client and base URL (for testing).
func NewWithClient(token, baseURL string, client *http.Client) *Collector {
	return &Collector{
		token:   token,
		baseURL: baseURL,
		client:  client,
	}
}

func (c *Collector) Name() models.Source {
	return models.SourceCalendar
}

// Collect fetches calendar events from the last 24 hours.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectRange(ctx, time.Now().Add(-24*time.Hour), time.Now())
}

// CollectRange fetches events within the given time range from all calendars.
func (c *Collector) CollectRange(ctx context.Context, timeMin, timeMax time.Time) ([]models.Activity, error) {
	events, err := c.fetchEvents(ctx, "primary", timeMin, timeMax, "")
	if err != nil {
		return nil, fmt.Errorf("fetch events: %w", err)
	}
	return c.eventsToActivities(events, "primary"), nil
}

// CollectWithSyncToken performs an incremental sync using a syncToken.
// Returns activities, the next sync token, and any error.
// If the syncToken is invalid (410 Gone), returns ErrSyncTokenExpired.
func (c *Collector) CollectWithSyncToken(ctx context.Context, syncToken string) ([]models.Activity, string, error) {
	events, nextToken, err := c.fetchEventsIncremental(ctx, "primary", syncToken)
	if err != nil {
		return nil, "", err
	}
	return c.eventsToActivities(events, "primary"), nextToken, nil
}

// InitialSync performs a full sync and returns activities plus the sync token for future incremental syncs.
func (c *Collector) InitialSync(ctx context.Context, lookback time.Duration) ([]models.Activity, string, error) {
	timeMin := time.Now().Add(-lookback)
	events, nextToken, err := c.fetchEventsWithToken(ctx, "primary", timeMin, "")
	if err != nil {
		return nil, "", fmt.Errorf("initial sync: %w", err)
	}
	return c.eventsToActivities(events, "primary"), nextToken, nil
}

// Google Calendar API response types.

type eventsResponse struct {
	Items         []calendarEvent `json:"items"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
	NextSyncToken string          `json:"nextSyncToken,omitempty"`
}

type calendarEvent struct {
	ID             string        `json:"id"`
	Status         string        `json:"status"` // "confirmed", "tentative", "cancelled"
	Summary        string        `json:"summary"`
	Description    string        `json:"description,omitempty"`
	Location       string        `json:"location,omitempty"`
	Start          eventDateTime `json:"start"`
	End            eventDateTime `json:"end"`
	Attendees      []apiAttendee `json:"attendees,omitempty"`
	Organizer      apiOrganizer  `json:"organizer,omitempty"`
	RecurringEvent string        `json:"recurringEventId,omitempty"`
	ConferenceData *confData     `json:"conferenceData,omitempty"`
}

type eventDateTime struct {
	DateTime string `json:"dateTime,omitempty"` // RFC3339
	Date     string `json:"date,omitempty"`     // YYYY-MM-DD for all-day events
}

type apiAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Self           bool   `json:"self,omitempty"`
}

type apiOrganizer struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

type confData struct {
	EntryPoints []entryPoint `json:"entryPoints,omitempty"`
}

type entryPoint struct {
	EntryPointType string `json:"entryPointType"` // "video", "phone", etc.
	URI            string `json:"uri"`
}

// fetchEvents fetches events for a time range, handling pagination.
func (c *Collector) fetchEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time, pageToken string) ([]calendarEvent, error) {
	var allEvents []calendarEvent

	for {
		params := url.Values{
			"timeMin":      {timeMin.UTC().Format(time.RFC3339)},
			"timeMax":      {timeMax.UTC().Format(time.RFC3339)},
			"singleEvents": {"true"},
			"orderBy":      {"startTime"},
			"maxResults":   {fmt.Sprintf("%d", maxResults)},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		var resp eventsResponse
		path := fmt.Sprintf("/calendar/v3/calendars/%s/events", url.PathEscape(calendarID))
		if err := c.apiGet(ctx, path, params, &resp); err != nil {
			return nil, err
		}

		allEvents = append(allEvents, resp.Items...)

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	return allEvents, nil
}

// fetchEventsWithToken fetches events and returns the sync token for future incremental syncs.
func (c *Collector) fetchEventsWithToken(ctx context.Context, calendarID string, timeMin time.Time, pageToken string) ([]calendarEvent, string, error) {
	var allEvents []calendarEvent
	var nextSyncToken string

	for {
		params := url.Values{
			"timeMin":      {timeMin.UTC().Format(time.RFC3339)},
			"singleEvents": {"true"},
			"orderBy":      {"startTime"},
			"maxResults":   {fmt.Sprintf("%d", maxResults)},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		var resp eventsResponse
		path := fmt.Sprintf("/calendar/v3/calendars/%s/events", url.PathEscape(calendarID))
		if err := c.apiGet(ctx, path, params, &resp); err != nil {
			return nil, "", err
		}

		allEvents = append(allEvents, resp.Items...)
		nextSyncToken = resp.NextSyncToken

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	return allEvents, nextSyncToken, nil
}

// ErrSyncTokenExpired indicates the sync token is no longer valid (HTTP 410).
var ErrSyncTokenExpired = fmt.Errorf("sync token expired")

// fetchEventsIncremental uses a syncToken to get only changed events.
func (c *Collector) fetchEventsIncremental(ctx context.Context, calendarID, syncToken string) ([]calendarEvent, string, error) {
	var allEvents []calendarEvent
	var nextSyncToken string
	pageToken := ""

	for {
		params := url.Values{
			"syncToken":  {syncToken},
			"maxResults": {fmt.Sprintf("%d", maxResults)},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		var resp eventsResponse
		path := fmt.Sprintf("/calendar/v3/calendars/%s/events", url.PathEscape(calendarID))
		if err := c.apiGet(ctx, path, params, &resp); err != nil {
			return nil, "", err
		}

		allEvents = append(allEvents, resp.Items...)
		nextSyncToken = resp.NextSyncToken

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	return allEvents, nextSyncToken, nil
}

// apiGet makes an authenticated GET request to the Google Calendar API.
func (c *Collector) apiGet(ctx context.Context, path string, params url.Values, dst any) error {
	reqURL := c.baseURL + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("calendar request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		return ErrSyncTokenExpired
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		return fmt.Errorf("rate limited (retry after %s seconds)", retryAfter)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("calendar %s returned %d: %s", path, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

// eventsToActivities converts Google Calendar events to Activity models.
func (c *Collector) eventsToActivities(events []calendarEvent, calendarID string) []models.Activity {
	var activities []models.Activity

	for _, ev := range events {
		if ev.Status == "cancelled" {
			continue
		}

		start, end, isAllDay := parseEventTimes(ev)
		if start.IsZero() {
			continue
		}

		duration := int(end.Sub(start).Minutes())

		var attendees []Attendee
		var selfResponse string
		for _, a := range ev.Attendees {
			attendees = append(attendees, Attendee{
				Email:          a.Email,
				DisplayName:    a.DisplayName,
				ResponseStatus: a.ResponseStatus,
				Organizer:      a.Organizer,
				Self:           a.Self,
			})
			if a.Self {
				selfResponse = a.ResponseStatus
			}
		}

		conferenceLink := ""
		if ev.ConferenceData != nil {
			for _, ep := range ev.ConferenceData.EntryPoints {
				if ep.EntryPointType == "video" {
					conferenceLink = ep.URI
					break
				}
			}
		}

		meta := eventMeta{
			CalendarID:     calendarID,
			EventID:        ev.ID,
			Start:          start.UTC().Format(time.RFC3339),
			End:            end.UTC().Format(time.RFC3339),
			DurationMin:    duration,
			Attendees:      attendees,
			Organizer:      ev.Organizer.Email,
			IsRecurring:    ev.RecurringEvent != "",
			Location:       ev.Location,
			ConferenceLink: conferenceLink,
			Status:         ev.Status,
			ResponseStatus: selfResponse,
			IsAllDay:       isAllDay,
			MeetingType:    ClassifyMeeting(ev.Summary, len(ev.Attendees)),
		}

		metaJSON, _ := json.Marshal(meta)
		sourceID := fmt.Sprintf("calendar:%s:%s", calendarID, ev.ID)

		title := ev.Summary
		if title == "" {
			title = "(No title)"
		}

		activities = append(activities, models.Activity{
			Source:    models.SourceCalendar,
			SourceID:  sourceID,
			Type:      models.TypeMeeting,
			Title:     title,
			Content:   ev.Description,
			Metadata:  string(metaJSON),
			Timestamp: start,
		})
	}

	return activities
}

// parseEventTimes extracts start/end times, handling both timed and all-day events.
func parseEventTimes(ev calendarEvent) (start, end time.Time, isAllDay bool) {
	if ev.Start.DateTime != "" {
		start, _ = time.Parse(time.RFC3339, ev.Start.DateTime)
		end, _ = time.Parse(time.RFC3339, ev.End.DateTime)
		return start, end, false
	}
	if ev.Start.Date != "" {
		start, _ = time.Parse("2006-01-02", ev.Start.Date)
		end, _ = time.Parse("2006-01-02", ev.End.Date)
		return start, end, true
	}
	return time.Time{}, time.Time{}, false
}
