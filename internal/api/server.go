package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/pavelpiliak/devrecall/internal/auth"
	"github.com/pavelpiliak/devrecall/internal/config"
	"github.com/pavelpiliak/devrecall/internal/embedding"
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
		Handler: mux,
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
	mux.HandleFunc("POST /api/sync", s.handleSync)
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

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"sources": result,
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
	// Trigger a lightweight sync — just report what would be synced.
	// Full sync with all collectors is complex and long-running;
	// the POST /api/sync endpoint acknowledges the request and returns source status.
	counts, err := s.db.CountActivitiesBySource()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "counting activities: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":    "sync acknowledged",
		"activities": counts,
	})
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
