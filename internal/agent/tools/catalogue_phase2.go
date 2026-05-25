package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// ─── log_event ────────────────────────────────────────────────────────────────
// The catalogue's only write tool. Mirrors `devrecall log` — captures a
// manual note (decision, in-person chat, observation) and indexes it like
// any other activity. The agent can call this in the middle of a coding
// session: "we decided to switch to ULIDs" → indexed forever.

type logEventArgs struct {
	Text   string   `json:"text"`
	Tags   []string `json:"tags,omitempty"`
	People []string `json:"people,omitempty"`
}

type logEventResult struct {
	ActivityID int64     `json:"activity_id"`
	Title      string    `json:"title"`
	Timestamp  time.Time `json:"timestamp"`
}

func logEventTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"text":{"type":"string","description":"The note body. First line (truncated to 200 chars) becomes the title."},
			"tags":{"type":"array","items":{"type":"string"},"description":"Optional tags for filtering later"},
			"people":{"type":"array","items":{"type":"string"},"description":"Names/emails of people referenced in the note"}
		},
		"required":["text"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "log_event",
		Description: "Record a manual note — a decision, in-person conversation, observation, or anything else worth remembering. Stored as type=note from source=manual and indexed alongside everything else. Use this when the user dictates context you want recallable later.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a logEventArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("log_event args: %w", err)
			}
			text := strings.TrimSpace(a.Text)
			if text == "" {
				return nil, errors.New("log_event: text is required")
			}

			now := deps.now()
			activity := buildManualNote(text, a.Tags, a.People, now)
			if self, err := deps.DB.GetSelfIdentity(); err == nil && self != nil {
				activity.IdentityID = self.ID
			}
			id, err := deps.DB.InsertActivity(activity)
			if err != nil {
				return nil, fmt.Errorf("insert: %w", err)
			}
			return mustJSON(logEventResult{
				ActivityID: id,
				Title:      activity.Title,
				Timestamp:  activity.Timestamp,
			}), nil
		},
	}
}

// buildManualNote constructs a manual-source activity. Kept here (rather
// than reusing cmd/devrecall.buildManualActivity) so internal/agent/tools
// stays import-free of cmd/devrecall.
func buildManualNote(text string, tags, people []string, ts time.Time) models.Activity {
	title := text
	if idx := strings.IndexByte(title, '\n'); idx >= 0 {
		title = title[:idx]
	}
	if len(title) > 200 {
		title = title[:200]
	}

	meta := map[string]any{}
	if len(tags) > 0 {
		meta["tags"] = tags
	}
	if len(people) > 0 {
		meta["people"] = people
	}
	var metaStr string
	if len(meta) > 0 {
		b, _ := json.Marshal(meta)
		metaStr = string(b)
	}

	return models.Activity{
		Source:    models.SourceManual,
		SourceID:  fmt.Sprintf("manual-%d-%s", ts.UnixNano(), shortHash(text)),
		Type:      models.TypeNote,
		Title:     strings.TrimSpace(title),
		Content:   text,
		Metadata:  metaStr,
		Timestamp: ts,
	}
}

// shortHash returns a stable 8-hex-char FNV-1a digest of s. Not cryptographic,
// just enough to dedupe identical re-logs.
func shortHash(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

// ─── who_worked_on ───────────────────────────────────────────────────────────
// Activities by a person, optionally filtered by keyword and date range.
// Caller can identify the person by id, email, or name — the tool resolves
// the latter two via the identity table.

type whoWorkedOnArgs struct {
	IdentityID  int64  `json:"identity_id,omitempty"`
	NameOrEmail string `json:"name_or_email,omitempty"`
	Query       string `json:"query,omitempty"`
	Start       string `json:"start,omitempty"`
	End         string `json:"end,omitempty"`
	Source      string `json:"source,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

func whoWorkedOnTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"identity_id":{"type":"integer","description":"Identity ID; if unknown, pass name_or_email instead"},
			"name_or_email":{"type":"string","description":"Resolved via identity table when identity_id is not given"},
			"query":{"type":"string","description":"Optional keyword to narrow within this person's activity"},
			"start":{"type":"string"},
			"end":{"type":"string"},
			"source":{"type":"string"},
			"limit":{"type":"integer","description":"Max rows (default 50)"}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "who_worked_on",
		Description: "Activities authored by a specific person, optionally narrowed by a keyword query and date range. Pass identity_id if you have one (from list_identities/resolve_person); otherwise pass name_or_email and the tool resolves it.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a whoWorkedOnArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("who_worked_on args: %w", err)
			}
			identityID := a.IdentityID
			if identityID == 0 {
				if strings.TrimSpace(a.NameOrEmail) == "" {
					return nil, errors.New("who_worked_on: identity_id or name_or_email is required")
				}
				ident, err := resolveIdentity(deps.DB, a.NameOrEmail)
				if err != nil {
					return nil, err
				}
				if ident == nil {
					return mustJSON(map[string]any{
						"identity":   nil,
						"activities": []activitySummary{},
						"count":      0,
					}), nil
				}
				identityID = ident.ID
			}

			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			limit := clampLimit(a.Limit, 50)
			filter := storage.ActivityFilter{
				Source:     models.Source(a.Source),
				IdentityID: identityID,
				After:      after,
				Before:     before,
				Limit:      limit + 1,
			}

			var rows []models.Activity
			if q := strings.TrimSpace(a.Query); q != "" {
				matches, err := deps.DB.SearchFTS(q, filter, limit+1)
				if err != nil {
					return nil, fmt.Errorf("fts: %w", err)
				}
				for _, m := range matches {
					if m.Activity.IdentityID != identityID {
						continue // FTS may not honour identity scoping in older builds; double-check.
					}
					rows = append(rows, m.Activity)
				}
			} else {
				rows, err = deps.DB.ListActivities(filter)
				if err != nil {
					return nil, fmt.Errorf("list: %w", err)
				}
			}

			hasMore := len(rows) > limit
			if hasMore {
				rows = rows[:limit]
			}
			result := map[string]any{
				"identity_id": identityID,
				"activities":  toSummaries(rows),
				"count":       len(rows),
				"has_more":    hasMore,
			}
			if hasMore {
				result["hint"] = "more rows exist; raise limit or paginate"
			}
			return mustJSON(result), nil
		},
	}
}

// resolveIdentity tries exact email first, then substring on name/email.
// Mirrors resolve_person's logic without duplicating the tool wrapper.
func resolveIdentity(db *storage.DB, needle string) (*models.Identity, error) {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return nil, nil
	}
	if strings.Contains(needle, "@") {
		hit, err := db.GetIdentityByEmail(needle)
		if err != nil {
			return nil, fmt.Errorf("get by email: %w", err)
		}
		if hit != nil {
			return hit, nil
		}
	}
	ids, err := db.ListIdentities()
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	var best *models.Identity
	for i := range ids {
		name := strings.ToLower(ids[i].Name)
		email := strings.ToLower(ids[i].Email)
		if name == needle || email == needle {
			return &ids[i], nil
		}
		if best == nil && (strings.Contains(name, needle) || strings.Contains(email, needle)) {
			best = &ids[i]
		}
	}
	return best, nil
}

// ─── recent_decisions ────────────────────────────────────────────────────────
// Surface decision-shaped activity — manual notes plus anything whose title
// or body hits decided/decision/RFC. Useful for "what did we decide about
// X" or just "what decisions did we make this quarter".

type recentDecisionsArgs struct {
	Start  string `json:"start,omitempty"`
	End    string `json:"end,omitempty"`
	Source string `json:"source,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// decisionKeywords are searched separately and unioned. storage.SearchFTS
// wraps each whitespace-separated token in quotes, so we can't use FTS5's
// inline OR operator here — fan out one query per keyword instead.
var decisionKeywords = []string{"decided", "decision", "RFC", "ADR"}

func recentDecisionsTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"start":{"type":"string"},
			"end":{"type":"string"},
			"source":{"type":"string","description":"Optional source filter"},
			"limit":{"type":"integer","description":"Max rows (default 50)"}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "recent_decisions",
		Description: "Find decision-shaped activity in a date range: manual notes (type=note) merged with anything whose title or body hits decided/decision/RFC/ADR. Use this before answering 'what did we decide about X' or composing a quarterly summary.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a recentDecisionsArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("recent_decisions args: %w", err)
			}
			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			limit := clampLimit(a.Limit, 50)
			filter := storage.ActivityFilter{
				Source: models.Source(a.Source),
				After:  after,
				Before: before,
			}

			// Bucket 1: all manual notes in the window.
			notesFilter := filter
			notesFilter.Type = models.TypeNote
			notesFilter.Limit = limit + 1
			notes, err := deps.DB.ListActivities(notesFilter)
			if err != nil {
				return nil, fmt.Errorf("list notes: %w", err)
			}

			// Bucket 2: union of FTS results for each decision-shaped keyword.
			// One query per keyword because sanitizeFTS quotes every token,
			// killing inline OR syntax.
			var matches []storage.FTSMatch
			for _, kw := range decisionKeywords {
				ms, err := deps.DB.SearchFTS(kw, filter, limit+1)
				if err != nil {
					return nil, fmt.Errorf("fts %s: %w", kw, err)
				}
				matches = append(matches, ms...)
			}

			merged := dedupeActivities(notes, matches)
			sort.Slice(merged, func(i, j int) bool {
				return merged[i].Timestamp.After(merged[j].Timestamp)
			})
			hasMore := len(merged) > limit
			if hasMore {
				merged = merged[:limit]
			}
			result := map[string]any{
				"activities": toSummaries(merged),
				"count":      len(merged),
				"has_more":   hasMore,
			}
			if hasMore {
				result["hint"] = "more decisions exist; narrow date range or raise limit"
			}
			return mustJSON(result), nil
		},
	}
}

// dedupeActivities merges two result lists, preferring the first occurrence
// per id. Order is preserved within each input; callers re-sort if needed.
func dedupeActivities(notes []models.Activity, matches []storage.FTSMatch) []models.Activity {
	seen := map[int64]bool{}
	out := make([]models.Activity, 0, len(notes)+len(matches))
	for _, n := range notes {
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	for _, m := range matches {
		if seen[m.Activity.ID] {
			continue
		}
		seen[m.Activity.ID] = true
		out = append(out, m.Activity)
	}
	return out
}

// ─── prep_meeting ────────────────────────────────────────────────────────────
// Given a calendar activity (by ID or by date+title), return the event +
// each attendee's recent activity. Compresses the "what's everyone been up
// to going into this 1:1" lookup into one call.

type prepMeetingArgs struct {
	ActivityID    int64  `json:"activity_id,omitempty"`
	Date          string `json:"date,omitempty"`
	TitleContains string `json:"title_contains,omitempty"`
	LookbackDays  int    `json:"lookback_days,omitempty"`
	PerPersonLimit int   `json:"per_person_limit,omitempty"`
}

type prepMeetingAttendee struct {
	Email          string            `json:"email"`
	DisplayName    string            `json:"display_name,omitempty"`
	IdentityID     int64             `json:"identity_id,omitempty"`
	RecentActivity []activitySummary `json:"recent_activity"`
}

type prepMeetingResult struct {
	MeetingID   int64                 `json:"meeting_id"`
	Title       string                `json:"title"`
	Timestamp   time.Time             `json:"timestamp"`
	URL         string                `json:"url,omitempty"`
	Attendees   []prepMeetingAttendee `json:"attendees"`
}

func prepMeetingTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"activity_id":{"type":"integer","description":"Calendar activity ID, if you already have it (from list_activities source=calendar)"},
			"date":{"type":"string","description":"YYYY-MM-DD when activity_id is unknown — find the meeting that day matching title_contains"},
			"title_contains":{"type":"string","description":"Substring filter on meeting title when looking up by date"},
			"lookback_days":{"type":"integer","description":"How far back to fetch each attendee's recent activity (default 30)"},
			"per_person_limit":{"type":"integer","description":"Max activities per attendee (default 10)"}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "prep_meeting",
		Description: "Brief for a calendar meeting: returns the event details plus each attendee's recent shipped activity. Look up by activity_id if known, otherwise by date+title_contains. Use this to walk into a 1:1 or sync knowing what everyone's been doing.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a prepMeetingArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("prep_meeting args: %w", err)
			}
			lookback := a.LookbackDays
			if lookback <= 0 {
				lookback = 30
			}
			perPerson := clampLimit(a.PerPersonLimit, 10)
			if perPerson > 25 {
				perPerson = 25
			}

			meeting, err := loadMeeting(deps, a)
			if err != nil {
				return nil, err
			}
			if meeting == nil {
				return mustJSON(map[string]any{"meeting": nil}), nil
			}

			attendees := extractAttendees(meeting.Metadata)
			now := deps.now()
			after := now.AddDate(0, 0, -lookback)

			result := prepMeetingResult{
				MeetingID: meeting.ID,
				Title:     meeting.Title,
				Timestamp: meeting.Timestamp,
				URL:       extractURL(meeting.Metadata),
			}
			for _, att := range attendees {
				ident, _ := deps.DB.GetIdentityByEmail(strings.ToLower(att.Email))
				entry := prepMeetingAttendee{
					Email:       att.Email,
					DisplayName: att.DisplayName,
				}
				if ident != nil {
					entry.IdentityID = ident.ID
					rows, err := deps.DB.ListActivities(storage.ActivityFilter{
						IdentityID: ident.ID,
						After:      after,
						Limit:      perPerson,
					})
					if err == nil {
						entry.RecentActivity = toSummaries(rows)
					}
				}
				result.Attendees = append(result.Attendees, entry)
			}
			return mustJSON(result), nil
		},
	}
}

func loadMeeting(deps Deps, a prepMeetingArgs) (*models.Activity, error) {
	if a.ActivityID > 0 {
		rows, err := deps.DB.GetActivitiesByIDs([]int64{a.ActivityID})
		if err != nil {
			return nil, fmt.Errorf("get meeting: %w", err)
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return &rows[0], nil
	}
	if a.Date == "" {
		return nil, errors.New("prep_meeting: activity_id or date is required")
	}
	day, err := parseDate(a.Date)
	if err != nil {
		return nil, err
	}
	rows, err := deps.DB.ListActivities(storage.ActivityFilter{
		Source: models.SourceCalendar,
		After:  day,
		Before: day.AddDate(0, 0, 1),
		Limit:  50,
	})
	if err != nil {
		return nil, fmt.Errorf("list meetings: %w", err)
	}
	needle := strings.ToLower(strings.TrimSpace(a.TitleContains))
	for i := range rows {
		if needle == "" || strings.Contains(strings.ToLower(rows[i].Title), needle) {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// attendeeRef is the shape we care about in calendar metadata. Matches
// internal/collector/calendar.Attendee but inlined here to avoid an import
// cycle (calendar already uses storage's models).
type attendeeRef struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

func extractAttendees(metadata string) []attendeeRef {
	if metadata == "" {
		return nil
	}
	var parsed struct {
		Attendees []attendeeRef `json:"attendees"`
	}
	if err := json.Unmarshal([]byte(metadata), &parsed); err != nil {
		return nil
	}
	out := make([]attendeeRef, 0, len(parsed.Attendees))
	for _, att := range parsed.Attendees {
		if att.Self || att.Email == "" {
			continue
		}
		out = append(out, att)
	}
	return out
}

func extractURL(metadata string) string {
	if metadata == "" {
		return ""
	}
	var parsed struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal([]byte(metadata), &parsed)
	return parsed.URL
}
