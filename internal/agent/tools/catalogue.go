package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/internal/workitem"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// buildCatalogue returns the v1 set of tools, in display order. Each tool
// is read-only and hits internal/storage (or, for semantic search, the
// optional embedder) — no network calls or collector invocations.
func buildCatalogue(deps Deps) []Tool {
	return []Tool{
		currentTimeTool(deps),
		listActivitiesTool(deps),
		countActivitiesTool(deps),
		searchActivitiesTool(deps),
		semanticSearchActivitiesTool(deps),
		getActivityTool(deps),
		getRelatedActivitiesTool(deps),
		getWorkItemTool(deps),
		listWorkItemsTool(deps),
		whoWorkedOnTool(deps),
		recentDecisionsTool(deps),
		prepMeetingTool(deps),
		logEventTool(deps),
		listSummariesTool(deps),
		getSummaryTool(deps),
		listIdentitiesTool(deps),
		resolvePersonTool(deps),
	}
}

// ─── shared helpers ───────────────────────────────────────────────────────────

// parseDate parses an ISO timestamp or "YYYY-MM-DD" string.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q (use RFC3339 or YYYY-MM-DD)", s)
}

// activitySummary is the shallow row shape returned by list/search tools.
// Full content is omitted; the model can fetch it via get_activity.
// Digest/Tags come from the enrichment pass and WorkItems from work-item
// linking — all empty when those passes haven't covered the row yet.
type activitySummary struct {
	ID         int64     `json:"id"`
	Source     string    `json:"source"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	Timestamp  time.Time `json:"timestamp"`
	IdentityID int64     `json:"identity_id,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	WorkItems  []string  `json:"work_items,omitempty"`
}

func toSummary(a models.Activity) activitySummary {
	return activitySummary{
		ID:         a.ID,
		Source:     string(a.Source),
		Type:       string(a.Type),
		Title:      a.Title,
		Timestamp:  a.Timestamp,
		IdentityID: a.IdentityID,
	}
}

// annotateSummaries fills Digest/Tags/WorkItems from the enrichments and
// work-item tables in two batched queries. Lookup failures degrade to
// plain summaries — annotation is never a reason to fail a tool call.
func annotateSummaries(deps Deps, sums []activitySummary) []activitySummary {
	if len(sums) == 0 {
		return sums
	}
	ids := make([]int64, len(sums))
	for i, s := range sums {
		ids[i] = s.ID
	}
	enrichments, err := deps.DB.GetEnrichmentsByActivityIDs(ids)
	if err != nil {
		enrichments = nil
	}
	refs, err := deps.DB.ListActivityWorkItems(ids)
	if err != nil {
		refs = nil
	}
	for i := range sums {
		if e, ok := enrichments[sums[i].ID]; ok {
			sums[i].Digest = e.Digest
			sums[i].Tags = e.Tags
		}
		for _, r := range refs[sums[i].ID] {
			sums[i].WorkItems = append(sums[i].WorkItems, r.Key)
		}
	}
	return sums
}

func toSummaries(deps Deps, activities []models.Activity) []activitySummary {
	out := make([]activitySummary, len(activities))
	for i, a := range activities {
		out[i] = toSummary(a)
	}
	return annotateSummaries(deps, out)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// All result types are static structs — a marshal failure means a bug.
		panic(fmt.Sprintf("tools: marshal result: %v", err))
	}
	return b
}

// maxToolLimit hard-caps the rows any single tool call will return, so an
// agent asking for limit=10000 doesn't dump the whole DB into the context.
const maxToolLimit = 200

// clampLimit normalises a user-supplied limit: zero/negative → default,
// anything over the hard cap → cap.
func clampLimit(limit, def int) int {
	if limit <= 0 {
		limit = def
	}
	if limit > maxToolLimit {
		limit = maxToolLimit
	}
	return limit
}

// paginationHint returns a string the agent can act on when more rows exist
// beyond the current page. Empty string when there's nothing more.
func paginationHint(hasMore bool, limit, offset int) string {
	if !hasMore {
		return ""
	}
	return fmt.Sprintf("showing %d rows; more available — call again with offset=%d", limit, offset+limit)
}

func parseFilterDates(start, end string) (time.Time, time.Time, error) {
	after, err := parseDate(start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("start: %w", err)
	}
	before, err := parseDate(end)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("end: %w", err)
	}
	return after, before, nil
}

// ─── current_time ─────────────────────────────────────────────────────────────

func currentTimeTool(deps Deps) Tool {
	return Tool{
		Name:        "current_time",
		Description: "Return the current UTC time. Use this to anchor relative date references like 'today', 'yesterday', or 'last week' when the user does not provide an explicit date.",
		Schema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			now := deps.now().UTC()
			return mustJSON(map[string]any{
				"now":      now.Format(time.RFC3339),
				"timezone": "UTC",
			}), nil
		},
	}
}

// ─── list_activities ──────────────────────────────────────────────────────────

type listActivitiesArgs struct {
	Start      string `json:"start"`
	End        string `json:"end"`
	Source     string `json:"source"`
	Type       string `json:"type"`
	IdentityID int64  `json:"identity_id"`
	Tag        string `json:"tag"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
}

func listActivitiesTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"start":{"type":"string","description":"Inclusive start (RFC3339 or YYYY-MM-DD)"},
			"end":{"type":"string","description":"Exclusive end (RFC3339 or YYYY-MM-DD)"},
			"source":{"type":"string","description":"Filter by source: git, slack, calendar, github, gitlab, bitbucket, jira, confluence, linear, manual"},
			"type":{"type":"string","description":"Filter by activity type: commit, message, meeting, ticket, review, pull_request, merge_request, issue, note"},
			"identity_id":{"type":"integer","description":"Filter by identity (person) ID"},
			"tag":{"type":"string","description":"Filter by enrichment tag, e.g. feature, bugfix, refactor, review, testing, docs, deploy, release, incident, planning, discussion, decision, meeting, dependency, security, performance, status-change"},
			"limit":{"type":"integer","description":"Max rows to return (default 50)"},
			"offset":{"type":"integer","description":"Number of leading rows to skip"}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "list_activities",
		Description: "List activities in a time window with optional source/type/person filters. Returns shallow rows (no body); use get_activity to fetch full content.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a listActivitiesArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("list_activities args: %w", err)
			}
			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			limit := clampLimit(a.Limit, 50)
			// Fetch one extra row so we can detect whether more exist.
			fetch := limit + a.Offset + 1
			rows, err := deps.DB.ListActivities(storage.ActivityFilter{
				Source:     models.Source(a.Source),
				Type:       models.ActivityType(a.Type),
				IdentityID: a.IdentityID,
				Tag:        a.Tag,
				After:      after,
				Before:     before,
				Limit:      fetch,
			})
			if err != nil {
				return nil, fmt.Errorf("list activities: %w", err)
			}
			if a.Offset > 0 {
				if a.Offset >= len(rows) {
					rows = nil
				} else {
					rows = rows[a.Offset:]
				}
			}
			hasMore := len(rows) > limit
			if hasMore {
				rows = rows[:limit]
			}
			result := map[string]any{
				"activities": toSummaries(deps, rows),
				"count":      len(rows),
				"has_more":   hasMore,
			}
			if hint := paginationHint(hasMore, limit, a.Offset); hint != "" {
				result["hint"] = hint
			}
			return mustJSON(result), nil
		},
	}
}

// ─── count_activities ─────────────────────────────────────────────────────────

type countActivitiesArgs struct {
	Start      string `json:"start"`
	End        string `json:"end"`
	Source     string `json:"source"`
	Type       string `json:"type"`
	IdentityID int64  `json:"identity_id"`
	GroupBy    string `json:"group_by"`
}

func countActivitiesTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"start":{"type":"string","description":"Inclusive start (RFC3339 or YYYY-MM-DD)"},
			"end":{"type":"string","description":"Exclusive end (RFC3339 or YYYY-MM-DD)"},
			"source":{"type":"string"},
			"type":{"type":"string"},
			"identity_id":{"type":"integer"},
			"group_by":{"type":"string","enum":["","source","type","identity","day","week"],"description":"Optional grouping. Empty means just return the total."}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "count_activities",
		Description: "Count activities in a time window, optionally grouped by source, type, identity, day, or week. The primitive behind 'how many', 'how often', and 'compare X vs Y' questions.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a countActivitiesArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("count_activities args: %w", err)
			}
			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			rows, err := deps.DB.ListActivities(storage.ActivityFilter{
				Source:     models.Source(a.Source),
				Type:       models.ActivityType(a.Type),
				IdentityID: a.IdentityID,
				After:      after,
				Before:     before,
			})
			if err != nil {
				return nil, fmt.Errorf("count activities: %w", err)
			}
			result := map[string]any{"total": len(rows)}
			if a.GroupBy != "" {
				breakdown := map[string]int{}
				for _, r := range rows {
					var key string
					switch a.GroupBy {
					case "source":
						key = string(r.Source)
					case "type":
						key = string(r.Type)
					case "identity":
						if r.IdentityID == 0 {
							key = "0"
						} else {
							key = fmt.Sprintf("%d", r.IdentityID)
						}
					case "day":
						key = r.Timestamp.UTC().Format("2006-01-02")
					case "week":
						y, w := r.Timestamp.UTC().ISOWeek()
						key = fmt.Sprintf("%04d-W%02d", y, w)
					default:
						return nil, fmt.Errorf("count_activities: unknown group_by %q", a.GroupBy)
					}
					breakdown[key]++
				}
				result["group_by"] = a.GroupBy
				result["breakdown"] = breakdown
			}
			return mustJSON(result), nil
		},
	}
}

// ─── search_activities (FTS5) ─────────────────────────────────────────────────

type searchActivitiesArgs struct {
	Query  string `json:"query"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Source string `json:"source"`
	Tag    string `json:"tag"`
	Limit  int    `json:"limit"`
}

func searchActivitiesTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string","description":"Keywords to search for in titles and content"},
			"start":{"type":"string"},
			"end":{"type":"string"},
			"source":{"type":"string"},
			"tag":{"type":"string","description":"Filter matches by enrichment tag (e.g. bugfix, decision, incident)"},
			"limit":{"type":"integer"}
		},
		"required":["query"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "search_activities",
		Description: "Keyword search (FTS5) over activity titles and content. Use this for concrete terms and phrases like 'deploy decision' or 'retry strategy'. Prefer this over semantic_search_activities for keyword-shaped queries.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a searchActivitiesArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("search_activities args: %w", err)
			}
			if strings.TrimSpace(a.Query) == "" {
				return nil, errors.New("search_activities: query is required")
			}
			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			limit := clampLimit(a.Limit, 20)
			// Fetch one extra to detect "more available" without a count query.
			matches, err := deps.DB.SearchFTS(a.Query, storage.ActivityFilter{
				Source: models.Source(a.Source),
				Tag:    a.Tag,
				After:  after,
				Before: before,
			}, limit+1)
			if err != nil {
				return nil, fmt.Errorf("fts search: %w", err)
			}
			hasMore := len(matches) > limit
			if hasMore {
				matches = matches[:limit]
			}
			out := make([]activitySummary, 0, len(matches))
			for _, m := range matches {
				out = append(out, toSummary(m.Activity))
			}
			result := map[string]any{
				"activities": out,
				"count":      len(out),
				"has_more":   hasMore,
			}
			if hasMore {
				result["hint"] = fmt.Sprintf("more matches exist; narrow your query or raise limit (cap=%d)", maxToolLimit)
			}
			return mustJSON(result), nil
		},
	}
}

// ─── semantic_search_activities ───────────────────────────────────────────────

type semanticSearchArgs struct {
	Query string `json:"query"`
	Start string `json:"start"`
	End   string `json:"end"`
	Limit int    `json:"limit"`
}

func semanticSearchActivitiesTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string"},
			"start":{"type":"string"},
			"end":{"type":"string"},
			"limit":{"type":"integer"}
		},
		"required":["query"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "semantic_search_activities",
		Description: "Vector similarity search over activities. Use only when keyword search fails — for fuzzy or paraphrased queries like 'when did I work on the thing about latency'. Returns shallow rows ranked by similarity.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			if deps.Embedder == nil {
				return nil, errors.New("semantic_search_activities: no embedder configured")
			}
			var a semanticSearchArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("semantic_search_activities args: %w", err)
			}
			if strings.TrimSpace(a.Query) == "" {
				return nil, errors.New("semantic_search_activities: query is required")
			}
			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			vec, err := deps.Embedder.Embed(ctx, a.Query)
			if err != nil {
				return nil, fmt.Errorf("embed query: %w", err)
			}
			limit := clampLimit(a.Limit, 10)
			matches, err := deps.DB.SearchSimilar(vec, limit+1, after, before)
			if err != nil {
				return nil, fmt.Errorf("vector search: %w", err)
			}
			hasMore := len(matches) > limit
			if hasMore {
				matches = matches[:limit]
			}
			type scored struct {
				activitySummary
				Score float64 `json:"score"`
			}
			out := make([]scored, 0, len(matches))
			for _, m := range matches {
				out = append(out, scored{activitySummary: toSummary(m.Activity), Score: m.Score})
			}
			result := map[string]any{
				"activities": out,
				"count":      len(out),
				"has_more":   hasMore,
			}
			if hasMore {
				result["hint"] = fmt.Sprintf("more matches exist; narrow your query or raise limit (cap=%d)", maxToolLimit)
			}
			return mustJSON(result), nil
		},
	}
}

// ─── get_activity ─────────────────────────────────────────────────────────────

type getActivityArgs struct {
	ID int64 `json:"id"`
}

func getActivityTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{"id":{"type":"integer"}},
		"required":["id"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "get_activity",
		Description: "Fetch the full activity row (including content and metadata) for a given activity ID. Use after list_/search_ to drill into a specific row.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a getActivityArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("get_activity args: %w", err)
			}
			if a.ID <= 0 {
				return nil, errors.New("get_activity: id is required")
			}
			rows, err := deps.DB.GetActivitiesByIDs([]int64{a.ID})
			if err != nil {
				return nil, fmt.Errorf("get activity: %w", err)
			}
			if len(rows) == 0 {
				return mustJSON(map[string]any{"activity": nil}), nil
			}
			return mustJSON(map[string]any{"activity": rows[0]}), nil
		},
	}
}

// ─── list_summaries / get_summary ────────────────────────────────────────────

type listSummariesArgs struct {
	PeriodType string `json:"period_type"`
	Start      string `json:"start"`
	End        string `json:"end"`
	Limit      int    `json:"limit"`
}

func listSummariesTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"period_type":{"type":"string","enum":["daily","weekly","monthly","quarterly"]},
			"start":{"type":"string"},
			"end":{"type":"string"},
			"limit":{"type":"integer"}
		},
		"required":["period_type"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "list_summaries",
		Description: "List pre-built periodic summaries (daily/weekly/monthly/quarterly). Prefer these over re-summarizing raw activities when answering 'summarize my Q1' or 'what happened last week'.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a listSummariesArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("list_summaries args: %w", err)
			}
			if a.PeriodType == "" {
				return nil, errors.New("list_summaries: period_type is required")
			}
			after, before, err := parseFilterDates(a.Start, a.End)
			if err != nil {
				return nil, err
			}
			var rows []models.Summary
			if !after.IsZero() && !before.IsZero() {
				rows, err = deps.DB.ListSummariesInRange(a.PeriodType, after, before)
			} else {
				rows, err = deps.DB.ListSummaries(a.PeriodType, a.Limit)
			}
			if err != nil {
				return nil, fmt.Errorf("list summaries: %w", err)
			}
			return mustJSON(map[string]any{
				"summaries": rows,
				"count":     len(rows),
			}), nil
		},
	}
}

type getSummaryArgs struct {
	PeriodType  string `json:"period_type"`
	PeriodStart string `json:"period_start"`
}

func getSummaryTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"period_type":{"type":"string","enum":["daily","weekly","monthly","quarterly"]},
			"period_start":{"type":"string","description":"YYYY-MM-DD start date of the period"}
		},
		"required":["period_type","period_start"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "get_summary",
		Description: "Fetch a single pre-built summary by period type and start date. Returns null if no summary exists for that period.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a getSummaryArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("get_summary args: %w", err)
			}
			if a.PeriodType == "" || a.PeriodStart == "" {
				return nil, errors.New("get_summary: period_type and period_start required")
			}
			s, err := deps.DB.GetSummary(a.PeriodType, a.PeriodStart)
			if err != nil {
				// Not-found is reported as nil — distinguish errors by string.
				if strings.Contains(err.Error(), "no rows") || strings.Contains(err.Error(), "sql: no rows") {
					return mustJSON(map[string]any{"summary": nil}), nil
				}
				return nil, fmt.Errorf("get summary: %w", err)
			}
			return mustJSON(map[string]any{"summary": s}), nil
		},
	}
}

// ─── list_identities / resolve_person ────────────────────────────────────────

type listIdentitiesArgs struct {
	Query string `json:"query"`
}

func listIdentitiesTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string","description":"Optional case-insensitive substring filter on name/email"}
		},
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "list_identities",
		Description: "List people known to the system (Git authors, Slack users, etc.), optionally filtered by a name/email substring.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a listIdentitiesArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("list_identities args: %w", err)
			}
			ids, err := deps.DB.ListIdentities()
			if err != nil {
				return nil, fmt.Errorf("list identities: %w", err)
			}
			q := strings.ToLower(strings.TrimSpace(a.Query))
			if q != "" {
				filtered := ids[:0]
				for _, i := range ids {
					if strings.Contains(strings.ToLower(i.Name), q) || strings.Contains(strings.ToLower(i.Email), q) {
						filtered = append(filtered, i)
					}
				}
				ids = filtered
			}
			return mustJSON(map[string]any{
				"identities": ids,
				"count":      len(ids),
			}), nil
		},
	}
}

type resolvePersonArgs struct {
	NameOrEmail string `json:"name_or_email"`
}

func resolvePersonTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"name_or_email":{"type":"string"}
		},
		"required":["name_or_email"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "resolve_person",
		Description: "Resolve a name or email to a single identity (the closest match). Returns the matched identity or null if no candidate is found.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a resolvePersonArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("resolve_person args: %w", err)
			}
			needle := strings.ToLower(strings.TrimSpace(a.NameOrEmail))
			if needle == "" {
				return nil, errors.New("resolve_person: name_or_email required")
			}
			// Try exact email match first.
			if strings.Contains(needle, "@") {
				if hit, err := deps.DB.GetIdentityByEmail(needle); err != nil {
					return nil, fmt.Errorf("get identity by email: %w", err)
				} else if hit != nil {
					return mustJSON(map[string]any{"identity": hit}), nil
				}
			}
			// Fall back to substring scan.
			ids, err := deps.DB.ListIdentities()
			if err != nil {
				return nil, fmt.Errorf("list identities: %w", err)
			}
			var best *models.Identity
			for i := range ids {
				name := strings.ToLower(ids[i].Name)
				email := strings.ToLower(ids[i].Email)
				if name == needle || email == needle {
					best = &ids[i]
					break
				}
				if best == nil && (strings.Contains(name, needle) || strings.Contains(email, needle)) {
					best = &ids[i]
				}
			}
			if best == nil {
				return mustJSON(map[string]any{"identity": nil}), nil
			}
			return mustJSON(map[string]any{"identity": best}), nil
		},
	}
}

// ─── get_related_activities ──────────────────────────────────────────────────

type getRelatedActivitiesArgs struct {
	ID    int64 `json:"id"`
	Limit int   `json:"limit"`
}

// extractIssueKeys pulls a unified list of ticket keys from an activity's
// metadata. The implementation lives in internal/workitem so the linker
// and the tool catalogue share one definition.
func extractIssueKeys(metadata string) []string {
	return workitem.ExtractIssueKeys(metadata)
}

func getRelatedActivitiesTool(deps Deps) Tool {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"id":{"type":"integer","description":"Activity ID to find relatives for"},
			"limit":{"type":"integer","description":"Max related activities to return (default 50)"}
		},
		"required":["id"],
		"additionalProperties":false
	}`)
	return Tool{
		Name:        "get_related_activities",
		Description: "Find activities that share a ticket key with the given activity — e.g. given a Jira ticket, returns the commits / PRs / Linear issues / Confluence pages that reference the same key. Use this to assemble the full timeline around one piece of work.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			var a getRelatedActivitiesArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("get_related_activities args: %w", err)
			}
			if a.ID <= 0 {
				return nil, errors.New("get_related_activities: id is required")
			}
			rows, err := deps.DB.GetActivitiesByIDs([]int64{a.ID})
			if err != nil {
				return nil, fmt.Errorf("load source activity: %w", err)
			}
			if len(rows) == 0 {
				return mustJSON(map[string]any{
					"keys":    []string{},
					"related": []activitySummary{},
				}), nil
			}
			limit := clampLimit(a.Limit, 50)

			// Prefer materialized work-item links: they also cover commits
			// linked through a PR's commit list, which key extraction from
			// this activity's own metadata would miss.
			refs, _ := deps.DB.ListActivityWorkItems([]int64{a.ID})
			if len(refs[a.ID]) > 0 {
				var keys []string
				seen := map[int64]bool{a.ID: true}
				var related []models.Activity
				for _, ref := range refs[a.ID] {
					keys = append(keys, ref.Key)
					timeline, err := deps.DB.ListActivitiesByWorkItem(ref.ID, limit+1)
					if err != nil {
						return nil, fmt.Errorf("work item timeline: %w", err)
					}
					for _, act := range timeline {
						if !seen[act.ID] {
							seen[act.ID] = true
							related = append(related, act)
						}
					}
				}
				hasMore := len(related) > limit
				if hasMore {
					related = related[:limit]
				}
				result := map[string]any{
					"keys":     keys,
					"related":  toSummaries(deps, related),
					"has_more": hasMore,
				}
				if hasMore {
					result["hint"] = fmt.Sprintf("more related activities exist; raise limit (cap=%d)", maxToolLimit)
				}
				return mustJSON(result), nil
			}

			// Fallback for activities the linker hasn't covered: match by
			// ticket keys extracted from this activity's metadata.
			keys := extractIssueKeys(rows[0].Metadata)
			if len(keys) == 0 {
				return mustJSON(map[string]any{
					"keys":    []string{},
					"related": []activitySummary{},
				}), nil
			}
			// Fetch one extra to detect more.
			related, err := deps.DB.FindByIssueKeys(keys, a.ID, limit+1)
			if err != nil {
				return nil, fmt.Errorf("find related: %w", err)
			}
			hasMore := len(related) > limit
			if hasMore {
				related = related[:limit]
			}
			result := map[string]any{
				"keys":     keys,
				"related":  toSummaries(deps, related),
				"has_more": hasMore,
			}
			if hasMore {
				result["hint"] = fmt.Sprintf("more related activities exist; raise limit (cap=%d)", maxToolLimit)
			}
			return mustJSON(result), nil
		},
	}
}
