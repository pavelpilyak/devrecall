package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// workItemView is the JSON shape work-item tools return.
type workItemView struct {
	ID              int64  `json:"id"`
	Key             string `json:"key"`
	Kind            string `json:"kind"`
	Title           string `json:"title,omitempty"`
	Status          string `json:"status,omitempty"`
	StatusChangedAt string `json:"status_changed_at,omitempty"`
	URL             string `json:"url,omitempty"`
	FirstSeen       string `json:"first_seen"`
	LastSeen        string `json:"last_seen"`
}

func toWorkItemView(w models.WorkItem) workItemView {
	v := workItemView{
		ID:        w.ID,
		Key:       w.Key,
		Kind:      w.Kind,
		Title:     w.Title,
		Status:    w.Status,
		URL:       w.URL,
		FirstSeen: w.FirstSeen.Format(time.RFC3339),
		LastSeen:  w.LastSeen.Format(time.RFC3339),
	}
	if !w.StatusChangedAt.IsZero() {
		v.StatusChangedAt = w.StatusChangedAt.Format(time.RFC3339)
	}
	return v
}

// ─── get_work_item ────────────────────────────────────────────────────────────

type getWorkItemArgs struct {
	Key   string `json:"key"`
	ID    int64  `json:"id"`
	Limit int    `json:"limit"`
}

func getWorkItemTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"key":{"type":"string","description":"Work item key: a ticket key like PROJ-123, or a pr:... key from list_work_items"},
			"id":{"type":"integer","description":"Work item ID (alternative to key)"},
			"limit":{"type":"integer","description":"Max timeline activities to return (default 50)"}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "get_work_item",
		Description: "Fetch a work item (a ticket or PR and everything linked to it) with its full cross-source timeline: the ticket, its commits, PRs, reviews, and discussions in chronological order. The best tool for 'show me everything about PROJ-123'.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a getWorkItemArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("get_work_item args: %w", err)
			}
			var item *models.WorkItem
			var err error
			switch {
			case a.Key != "":
				item, err = deps.DB.GetWorkItemByKey(strings.ToUpper(strings.TrimSpace(a.Key)))
				if err == nil && item == nil {
					// pr:... keys are not uppercased; retry verbatim.
					item, err = deps.DB.GetWorkItemByKey(strings.TrimSpace(a.Key))
				}
			case a.ID > 0:
				item, err = deps.DB.GetWorkItemByID(a.ID)
			default:
				return nil, errors.New("get_work_item: key or id is required")
			}
			if err != nil {
				return nil, fmt.Errorf("get work item: %w", err)
			}
			if item == nil {
				return mustJSON(map[string]any{
					"found": false,
					"hint":  "no work item with that key/id — try list_work_items or search_activities",
				}), nil
			}

			limit := clampLimit(a.Limit, 50)
			timeline, err := deps.DB.ListActivitiesByWorkItem(item.ID, limit+1)
			if err != nil {
				return nil, fmt.Errorf("work item timeline: %w", err)
			}
			hasMore := len(timeline) > limit
			if hasMore {
				timeline = timeline[:limit]
			}
			result := map[string]any{
				"found":     true,
				"work_item": toWorkItemView(*item),
				"timeline":  toSummaries(deps, timeline),
				"has_more":  hasMore,
			}
			if hasMore {
				result["hint"] = fmt.Sprintf("timeline truncated; raise limit (cap=%d)", maxToolLimit)
			}
			return mustJSON(result), nil
		},
	}
}

// ─── list_work_items ──────────────────────────────────────────────────────────

type listWorkItemsArgs struct {
	Status string `json:"status"`
	Query  string `json:"query"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Limit  int    `json:"limit"`
}

func listWorkItemsTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"status":{"type":"string","description":"Filter by status, e.g. Done, In Progress, In Review"},
			"query":{"type":"string","description":"Substring match on key or title"},
			"start":{"type":"string","description":"Only items active on/after this date (RFC3339 or YYYY-MM-DD)"},
			"end":{"type":"string","description":"Only items active before this date"},
			"limit":{"type":"integer","description":"Max items to return (default 25)"}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "list_work_items",
		Description: "List work items (tickets/PRs with their linked activity), most recently active first. The structural answer to 'what was I working on last week' — each item groups a ticket with its commits, PRs, and discussions. Use get_work_item for the full timeline of one item.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a listWorkItemsArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("list_work_items args: %w", err)
			}
			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			limit := clampLimit(a.Limit, 25)
			items, err := deps.DB.ListWorkItems(storage.WorkItemFilter{
				Status: a.Status,
				Query:  a.Query,
				After:  after,
				Before: before,
				Limit:  limit + 1,
			})
			if err != nil {
				return nil, fmt.Errorf("list work items: %w", err)
			}
			hasMore := len(items) > limit
			if hasMore {
				items = items[:limit]
			}
			out := make([]workItemView, len(items))
			for i, w := range items {
				out[i] = toWorkItemView(w)
			}
			result := map[string]any{
				"work_items": out,
				"count":      len(out),
				"has_more":   hasMore,
			}
			if hasMore {
				result["hint"] = fmt.Sprintf("more work items exist; narrow filters or raise limit (cap=%d)", maxToolLimit)
			}
			return mustJSON(result), nil
		},
	}
}
