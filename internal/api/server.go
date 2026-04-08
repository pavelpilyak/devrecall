package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pavelpiliak/devrecall/internal/agent"
	"github.com/pavelpiliak/devrecall/internal/auth"
	"github.com/pavelpiliak/devrecall/internal/collector/git"
	"github.com/pavelpiliak/devrecall/internal/config"
	"github.com/pavelpiliak/devrecall/internal/embedding"
	"github.com/pavelpiliak/devrecall/internal/license"
	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/internal/privacy"
	"github.com/pavelpiliak/devrecall/internal/rag"
	"github.com/pavelpiliak/devrecall/internal/storage"
	"github.com/pavelpiliak/devrecall/internal/summarizer"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

const defaultPort = 9147 // "DRCL" on phone keypad

// Server is the localhost-only HTTP API for desktop app and integrations.
type Server struct {
	port       int
	srv        *http.Server
	db         *storage.DB
	cfg        *config.Config
	tokenStore auth.TokenStore
	dataDir    string // override for ~/.devrecall (used in tests)

	// agentLoopFactory builds the agent loop used by the chat-stream handler.
	// Tests inject a fake provider through this hook; in production it's
	// constructed from cfg + tokenStore on first call (see chatLoop).
	agentLoopFactory func() (*agent.Loop, error)
}

// NewServer creates a local API server on the given port (0 = default 9147).
func NewServer(port int, db *storage.DB, cfg *config.Config, tokenStore auth.TokenStore) *Server {
	if port == 0 {
		port = defaultPort
	}
	return &Server{
		port:       port,
		db:         db,
		cfg:        cfg,
		tokenStore: tokenStore,
	}
}

// Start begins serving on localhost only. It blocks until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.srv = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", s.port),
		Handler: corsMiddleware(mux),
	}

	go func() {
		<-ctx.Done()
		s.srv.Close()
	}()

	if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Port returns the configured port number.
func (s *Server) Port() int {
	return s.port
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/standup", s.handleStandup)
	mux.HandleFunc("GET /api/week", s.handleWeek)
	mux.HandleFunc("GET /api/activities", s.handleActivities)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("POST /api/sync", s.handleSync)
	mux.HandleFunc("POST /api/activate", s.handleActivate)
	mux.HandleFunc("POST /api/log", s.handleLog)
}

// --- Handlers ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	counts, err := s.db.CountActivitiesBySource()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "counting activities: "+err.Error())
		return
	}

	type sourceStatus struct {
		Name      string  `json:"name"`
		Enabled   bool    `json:"enabled"`
		SyncedAt  *string `json:"synced_at,omitempty"`
		Count     int     `json:"count"`
	}

	sources := []struct {
		name    string
		enabled bool
	}{
		{"git", s.cfg.Git.Enabled},
		{"slack", s.cfg.Slack.Enabled},
		{"calendar", s.cfg.Calendar.Enabled},
		{"github", s.cfg.GitHub.Enabled},
		{"gitlab", s.cfg.GitLab.Enabled},
		{"bitbucket", s.cfg.Bitbucket.Enabled},
		{"jira", s.cfg.Jira.Enabled},
		{"linear", s.cfg.Linear.Enabled},
	}

	result := make([]sourceStatus, 0, len(sources))
	for _, src := range sources {
		ss := sourceStatus{
			Name:    src.name,
			Enabled: src.enabled,
			Count:   counts[src.name],
		}
		if state, err := s.db.GetSyncState(src.name); err == nil && state != nil {
			t := state.SyncedAt.Format(time.RFC3339)
			ss.SyncedAt = &t
		}
		result = append(result, ss)
	}

	licInfo := s.getLicenseInfo()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"sources": result,
		"license": licInfo,
	})
}

func (s *Server) handleStandup(w http.ResponseWriter, r *http.Request) {
	// Parse optional ?date=YYYY-MM-DD (default: yesterday).
	targetDate := time.Now().AddDate(0, 0, -1)
	if d := r.URL.Query().Get("date"); d != "" {
		parsed, err := time.Parse("2006-01-02", d)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid date (expected YYYY-MM-DD)")
			return
		}
		targetDate = parsed
	}

	dayStart := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.AddDate(0, 0, 1)

	activities, err := s.db.ListActivities(storage.ActivityFilter{
		After:  dayStart,
		Before: dayEnd,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "querying activities: "+err.Error())
		return
	}

	activities = privacy.Apply(activities, s.cfg.Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	var sum summarizer.Summarizer
	if p, llmErr := llm.FromConfig(s.cfg, s.tokenStore); llmErr == nil {
		sum = summarizer.NewLLMSummarizer(p).WithPromptLoader(s.promptLoader())
	} else {
		sum = summarizer.NewTemplateSummarizer()
	}

	report, err := sum.Standup(activities)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generating standup: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"date":           targetDate.Format("2006-01-02"),
		"report":         report,
		"activity_count": len(activities),
	})
}

func (s *Server) handleWeek(w http.ResponseWriter, r *http.Request) {
	weeksBack := 0
	if wb := r.URL.Query().Get("weeks_back"); wb != "" {
		n, err := strconv.Atoi(wb)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid weeks_back (expected non-negative integer)")
			return
		}
		weeksBack = n
	}

	now := time.Now()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := now.AddDate(0, 0, -(weekday - 1))
	monday = time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
	monday = monday.AddDate(0, 0, -7*weeksBack)
	sunday := monday.AddDate(0, 0, 7)

	activities, err := s.db.ListActivities(storage.ActivityFilter{
		After:  monday,
		Before: sunday,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "querying activities: "+err.Error())
		return
	}

	activities = privacy.Apply(activities, s.cfg.Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	var sum summarizer.Summarizer
	if p, llmErr := llm.FromConfig(s.cfg, s.tokenStore); llmErr == nil {
		sum = summarizer.NewLLMSummarizer(p).WithPromptLoader(s.promptLoader())
	} else {
		sum = summarizer.NewTemplateSummarizer()
	}

	report, err := sum.WeeklySummary(activities)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generating weekly summary: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"week_start":     monday.Format("2006-01-02"),
		"week_end":       sunday.AddDate(0, 0, -1).Format("2006-01-02"),
		"report":         report,
		"activity_count": len(activities),
	})
}

func (s *Server) handleActivities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := storage.ActivityFilter{}

	if src := q.Get("source"); src != "" {
		filter.Source = models.Source(src)
	}
	if typ := q.Get("type"); typ != "" {
		filter.Type = models.ActivityType(typ)
	}
	if after := q.Get("after"); after != "" {
		t, err := time.Parse("2006-01-02", after)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid after date (expected YYYY-MM-DD)")
			return
		}
		filter.After = t
	}
	if before := q.Get("before"); before != "" {
		t, err := time.Parse("2006-01-02", before)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before date (expected YYYY-MM-DD)")
			return
		}
		filter.Before = t
	}

	filter.Limit = 50
	if lim := q.Get("limit"); lim != "" {
		n, err := strconv.Atoi(lim)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "invalid limit (expected 1-500)")
			return
		}
		filter.Limit = n
	}

	activities, err := s.db.ListActivities(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "querying activities: "+err.Error())
		return
	}

	activities = privacy.Apply(activities, s.cfg.Privacy)

	writeJSON(w, http.StatusOK, map[string]any{
		"activities": activities,
		"count":      len(activities),
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "missing required parameter: q")
		return
	}

	filter := storage.ActivityFilter{}
	if src := r.URL.Query().Get("source"); src != "" {
		filter.Source = models.Source(src)
	}

	limit := 20
	if lim := r.URL.Query().Get("limit"); lim != "" {
		n, err := strconv.Atoi(lim)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "invalid limit (expected 1-100)")
			return
		}
		limit = n
	}

	results, err := s.db.SearchFTS(query, filter, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed: "+err.Error())
		return
	}

	items := make([]map[string]any, 0, len(results))
	for _, r := range results {
		items = append(items, map[string]any{
			"activity": r.Activity,
			"rank":     r.Rank,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"query":   query,
		"results": items,
		"count":   len(items),
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string        `json:"message"`
		History []llm.Message `json:"history,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "missing required field: message")
		return
	}

	llmProvider, err := llm.FromConfig(s.cfg, s.tokenStore)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "LLM not configured")
		return
	}

	embedder, err := embedding.FromConfig(s.cfg, s.tokenStore)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "embedding provider not configured")
		return
	}

	retriever := rag.NewHybridRetriever(s.db, embedder)
	results, err := retriever.Retrieve(r.Context(), req.Message, 10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "retrieval failed: "+err.Error())
		return
	}

	// Supplement with date-filtered activities if query mentions relative dates.
	if start, end, ok := parseDateRange(req.Message); ok {
		dateActivities, _ := s.db.ListActivities(storage.ActivityFilter{
			After:  start,
			Before: end,
			Limit:  20,
		})
		// If no data for the requested period, auto-sync and retry.
		if len(dateActivities) == 0 && len(results) == 0 {
			if n := s.syncGit(r.Context()); n > 0 {
				dateActivities, _ = s.db.ListActivities(storage.ActivityFilter{
					After:  start,
					Before: end,
					Limit:  20,
				})
			}
		}
		// Merge date-filtered activities into RAG results (avoid duplicates).
		existingIDs := make(map[string]bool)
		for _, r := range results {
			existingIDs[r.Activity.SourceID] = true
		}
		for _, a := range dateActivities {
			if !existingIDs[a.SourceID] {
				results = append(results, rag.Result{Activity: a, Score: 1.0})
			}
		}
	}

	// Build context from retrieved activities.
	contextStr := formatRAGContext(results)

	userMsg := req.Message
	if contextStr != "" {
		userMsg = fmt.Sprintf("Context from work history:\n%s\n\nUser question: %s", contextStr, req.Message)
	}

	messages := make([]llm.Message, 0, 2+len(req.History))
	messages = append(messages, llm.Message{Role: "system", Content: chatSystemPrompt})
	messages = append(messages, req.History...)
	messages = append(messages, llm.Message{Role: "user", Content: userMsg})

	response, err := llmProvider.Chat(r.Context(), messages, llm.ChatOpts{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LLM failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"response":         response,
		"sources_count":    len(results),
	})
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	synced := s.syncGit(r.Context())

	counts, err := s.db.CountActivitiesBySource()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "counting activities: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":    fmt.Sprintf("sync complete — %d new activities", synced),
		"activities": counts,
	})
}

// syncGit runs git collector and inserts new activities. Returns count of new activities.
func (s *Server) syncGit(ctx context.Context) int {
	if !s.cfg.Git.Enabled {
		return 0
	}
	repos := s.cfg.Git.Repos
	if len(s.cfg.Git.ScanPaths) > 0 {
		repos = mergeUnique(repos, git.DiscoverRepos(s.cfg.Git.ScanPaths))
	}
	emails := mergeUnique(s.cfg.Git.Emails, git.DetectEmails(repos))

	if len(repos) == 0 || len(emails) == 0 {
		return 0
	}
	collector := git.New(repos, emails)
	activities, err := collector.Collect(ctx)
	if err != nil || len(activities) == 0 {
		return 0
	}
	inserted, _ := s.db.InsertActivities(activities)
	return inserted
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	result := append([]string{}, a...)
	for _, s := range b {
		if !seen[s] {
			result = append(result, s)
			seen[s] = true
		}
	}
	return result
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text   string   `json:"text"`
		At     string   `json:"at,omitempty"`
		Tags   []string `json:"tags,omitempty"`
		People []string `json:"people,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "missing required field: text")
		return
	}

	ts := time.Now()
	if req.At != "" {
		parsed, err := parseLogTimestamp(req.At, ts.Location())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ts = parsed
	}

	title := text
	if idx := strings.IndexByte(title, '\n'); idx >= 0 {
		title = title[:idx]
	}
	if len(title) > 200 {
		title = title[:200]
	}

	metadata := map[string]any{}
	if len(req.Tags) > 0 {
		metadata["tags"] = req.Tags
	}
	if len(req.People) > 0 {
		metadata["people"] = req.People
	}
	var metaStr string
	if len(metadata) > 0 {
		b, _ := json.Marshal(metadata)
		metaStr = string(b)
	}

	activity := models.Activity{
		Source:    models.SourceManual,
		SourceID:  fmt.Sprintf("manual-%d", ts.UnixNano()),
		Type:      models.TypeNote,
		Title:     title,
		Content:   text,
		Metadata:  metaStr,
		Timestamp: ts,
	}
	if self, err := s.db.GetSelfIdentity(); err == nil && self != nil {
		activity.IdentityID = self.ID
	}

	id, err := s.db.InsertActivity(activity)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to log: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        id,
		"timestamp": activity.Timestamp,
		"title":     activity.Title,
	})
}

func parseLogTimestamp(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q (use YYYY-MM-DD or YYYY-MM-DD HH:MM)", s)
}

func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "missing required field: key")
		return
	}

	dir := s.configDir()

	lic, err := license.Activate(dir, req.Key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("%s plan activated", lic.Plan),
		"license": license.GetInfo(dir),
	})
}

func (s *Server) getLicenseInfo() license.Info {
	dir := s.configDir()
	return license.GetInfo(dir)
}

func (s *Server) configDir() string {
	if s.dataDir != "" {
		return s.dataDir
	}
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return dir
}

// --- Helpers ---

const chatSystemPrompt = `You are DevRecall, a developer work memory assistant. You answer questions about the user's work history based on retrieved activity context.

Rules:
- Answer based ONLY on the provided context. If context doesn't contain enough information, say so.
- Be concise and specific — cite dates, repo names, ticket IDs, and people when available.
- Use natural language, not bullet dumps (unless the user asks for a list).
- If the user asks a follow-up, use conversation history to understand what they're referring to.
- Never make up activities, commits, or people that aren't in the context.`

func formatRAGContext(results []rag.Result) string {
	if len(results) == 0 {
		return ""
	}
	var b []byte
	for i, r := range results {
		a := r.Activity
		b = append(b, fmt.Sprintf("[%d] %s | %s | %s | %s\n",
			i+1, a.Timestamp.Format("2006-01-02 15:04"), a.Source, a.Type, a.Title)...)
		if a.Content != "" {
			content := a.Content
			if len(content) > 300 {
				content = content[:300] + "..."
			}
			b = append(b, "    "+content+"\n"...)
		}
	}
	return string(b)
}

func (s *Server) promptLoader() *summarizer.PromptLoader {
	dir, err := config.Dir()
	if err != nil {
		return summarizer.NewPromptLoader("")
	}
	return summarizer.NewPromptLoader(dir + "/prompts")
}

// corsMiddleware adds CORS headers for local development (Tauri dev server on localhost:5173).
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// parseDateRange detects relative date references in a query and returns
// the corresponding time range. Supports "yesterday", "today", "this week",
// "last week", "last N days".
func parseDateRange(query string) (start, end time.Time, ok bool) {
	q := strings.ToLower(query)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch {
	case strings.Contains(q, "yesterday"):
		start = today.AddDate(0, 0, -1)
		end = today
		ok = true
	case strings.Contains(q, "today"):
		start = today
		end = today.AddDate(0, 0, 1)
		ok = true
	case strings.Contains(q, "this week"):
		weekday := int(today.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday = 7
		}
		start = today.AddDate(0, 0, -(weekday - 1)) // Monday
		end = today.AddDate(0, 0, 1)
		ok = true
	case strings.Contains(q, "last week"):
		weekday := int(today.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		thisMonday := today.AddDate(0, 0, -(weekday - 1))
		start = thisMonday.AddDate(0, 0, -7)
		end = thisMonday
		ok = true
	default:
		// "last N days" / "past N days"
		for _, prefix := range []string{"last ", "past "} {
			if idx := strings.Index(q, prefix); idx >= 0 {
				rest := q[idx+len(prefix):]
				if spaceIdx := strings.Index(rest, " day"); spaceIdx > 0 {
					if n, err := strconv.Atoi(strings.TrimSpace(rest[:spaceIdx])); err == nil && n > 0 {
						start = today.AddDate(0, 0, -n)
						end = today.AddDate(0, 0, 1)
						ok = true
						return
					}
				}
			}
		}
	}
	return
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
