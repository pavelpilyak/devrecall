package mcp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
)

// Resources are URI-addressed views over the DevRecall index. We don't
// enumerate them (resources/list returns empty) — clients use the templates
// returned by resources/templates/list to construct URIs on demand.
//
// All three resources return JSON in the contents text payload. Clients can
// pretty-print or pass through to the model as-is.

// resourceTemplates lists the URI shapes we accept on resources/read.
var resourceTemplates = []ResourceTemplate{
	{
		URITemplate: "standup://{date}",
		Name:        "Standup for a day",
		Description: "All activity from the given YYYY-MM-DD date, ordered by timestamp ASC.",
		MimeType:    "application/json",
	},
	{
		URITemplate: "activity://{id}",
		Name:        "Single activity",
		Description: "Full activity row (title, content body, metadata) by numeric ID.",
		MimeType:    "application/json",
	},
	{
		URITemplate: "timeline://{period}",
		Name:        "Timeline for a period",
		Description: "Activities in a period. Accepts: YYYY-MM-DD, YYYY-MM, YYYY, last-week, last-month, last-quarter.",
		MimeType:    "application/json",
	},
}

// readResource dispatches by URI scheme. The DB lookup happens here rather
// than via the tool registry so we can hand back a tighter JSON shape than
// the tool wrappers (which add agent-facing affordances like has_more hints).
func readResource(uri string, db *storage.DB) (ReadResourceResult, *RPCError) {
	scheme, rest, ok := splitURI(uri)
	if !ok {
		return ReadResourceResult{}, &RPCError{Code: ErrInvalidParams, Message: "resources/read: malformed URI " + uri}
	}
	switch scheme {
	case "standup":
		return readStandup(uri, rest, db)
	case "activity":
		return readActivity(uri, rest, db)
	case "timeline":
		return readTimeline(uri, rest, db)
	default:
		return ReadResourceResult{}, &RPCError{Code: ErrInvalidParams, Message: "resources/read: unknown scheme " + scheme}
	}
}

func splitURI(uri string) (scheme, rest string, ok bool) {
	i := strings.Index(uri, "://")
	if i < 0 {
		return "", "", false
	}
	return uri[:i], uri[i+3:], true
}

func readStandup(uri, datePart string, db *storage.DB) (ReadResourceResult, *RPCError) {
	day, err := time.Parse("2006-01-02", strings.TrimSpace(datePart))
	if err != nil {
		return ReadResourceResult{}, &RPCError{Code: ErrInvalidParams, Message: "standup:// expects YYYY-MM-DD, got " + datePart}
	}
	rows, err := db.ListActivities(storage.ActivityFilter{
		After:  day,
		Before: day.AddDate(0, 0, 1),
		Limit:  500,
	})
	if err != nil {
		return ReadResourceResult{}, &RPCError{Code: ErrInternal, Message: err.Error()}
	}
	// Reverse to chronological — ListActivities returns DESC.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	payload := map[string]any{
		"date":       day.Format("2006-01-02"),
		"count":      len(rows),
		"activities": rows,
	}
	return jsonResource(uri, payload), nil
}

func readActivity(uri, idPart string, db *storage.DB) (ReadResourceResult, *RPCError) {
	id, err := strconv.ParseInt(strings.TrimSpace(idPart), 10, 64)
	if err != nil || id <= 0 {
		return ReadResourceResult{}, &RPCError{Code: ErrInvalidParams, Message: "activity:// expects a positive integer ID, got " + idPart}
	}
	rows, err := db.GetActivitiesByIDs([]int64{id})
	if err != nil {
		return ReadResourceResult{}, &RPCError{Code: ErrInternal, Message: err.Error()}
	}
	if len(rows) == 0 {
		return jsonResource(uri, map[string]any{"activity": nil}), nil
	}
	return jsonResource(uri, map[string]any{"activity": rows[0]}), nil
}

func readTimeline(uri, periodPart string, db *storage.DB) (ReadResourceResult, *RPCError) {
	periodPart = strings.TrimSpace(periodPart)
	after, before, err := parsePeriod(periodPart)
	if err != nil {
		return ReadResourceResult{}, &RPCError{Code: ErrInvalidParams, Message: err.Error()}
	}
	rows, err := db.ListActivities(storage.ActivityFilter{
		After:  after,
		Before: before,
		Limit:  1000,
	})
	if err != nil {
		return ReadResourceResult{}, &RPCError{Code: ErrInternal, Message: err.Error()}
	}
	payload := map[string]any{
		"period":     periodPart,
		"after":      after.Format(time.RFC3339),
		"before":     before.Format(time.RFC3339),
		"count":      len(rows),
		"activities": rows,
	}
	return jsonResource(uri, payload), nil
}

// parsePeriod accepts the same shapes the CLI `timeline` command uses, kept
// in sync with cmd/devrecall.parsePeriodArg. Implemented locally so this
// package doesn't depend on cmd/devrecall.
func parsePeriod(s string) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	switch strings.ToLower(s) {
	case "last-week":
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return end.AddDate(0, 0, -7), end, nil
	case "last-month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
		return first, first.AddDate(0, 1, 0), nil
	case "this-month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return first, first.AddDate(0, 1, 0), nil
	case "last-quarter":
		q := (int(now.Month())-1)/3 - 1
		year := now.Year()
		if q < 0 {
			q += 4
			year--
		}
		start := time.Date(year, time.Month(q*3+1), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 3, 0), nil
	}
	// Try YYYY-MM-DD, YYYY-MM, YYYY.
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, t.AddDate(0, 0, 1), nil
	}
	if t, err := time.Parse("2006-01", s); err == nil {
		return t, t.AddDate(0, 1, 0), nil
	}
	if t, err := time.Parse("2006", s); err == nil {
		return t, t.AddDate(1, 0, 0), nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("timeline:// — unrecognised period %q (try YYYY-MM-DD, YYYY-MM, YYYY, last-week, last-month, last-quarter)", s)
}

func jsonResource(uri string, payload any) ReadResourceResult {
	b, _ := json.Marshal(payload)
	return ReadResourceResult{
		Contents: []ResourceContent{{
			URI:      uri,
			MimeType: "application/json",
			Text:     string(b),
		}},
	}
}

