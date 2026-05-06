package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/pavelpilyak/devrecall/internal/agent"
	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/chat/freshness"
	"github.com/pavelpilyak/devrecall/internal/collector/git"
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/privacy"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/internal/summarizer"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

const defaultPort = 3725 // "DRCL" on phone keypad

// Server is the localhost-only HTTP API for desktop app and integrations.
type Server struct {
	port int
	srv  *http.Server
	db   *storage.DB

	// cfg is read concurrently by every handler and replaced atomically by
	// WatchConfig and the LLM-config writer. Always read via Cfg() and write
	// via the copy-on-write pattern under cfgMu.
	cfg   atomic.Pointer[config.Config]
	cfgMu sync.Mutex

	tokenStore auth.TokenStore
	dataDir    string // override for ~/.devrecall (used in tests)

	// agentLoopFactory builds the agent loop used by the chat-stream handler.
	// Tests inject a fake provider through this hook; in production it's
	// constructed from cfg + tokenStore on first call (see chatLoop).
	agentLoopFactory func() (*agent.Loop, error)

	// freshnessFactory builds the (Checker, syncers) pair used by the
	// chat-stream handler. Tests inject deterministic syncers through
	// this hook; in production it's BuildFreshnessChecker / BuildFreshnessSyncers.
	freshnessFactory func() (*freshness.Checker, map[string]freshness.Syncer)
}

// NewServer creates a local API server on the given port (0 = default 3725).
func NewServer(port int, db *storage.DB, cfg *config.Config, tokenStore auth.TokenStore) *Server {
	if port == 0 {
		port = defaultPort
	}
	s := &Server{
		port:       port,
		db:         db,
		tokenStore: tokenStore,
	}
	s.cfg.Store(cfg)
	return s
}

// Cfg returns the current config snapshot. Callers must treat it as
// read-only — to mutate, take cfgMu, copy, save, then Store the copy.
func (s *Server) Cfg() *config.Config {
	return s.cfg.Load()
}

// WatchConfig blocks until ctx is cancelled, reloading the config from disk
// whenever the file changes. Editor-style atomic writes (temp file + rename)
// are picked up because we watch the parent directory rather than the file
// inode itself. Events are debounced so a multi-stage write produces at most
// one reload.
func (s *Server) WatchConfig(ctx context.Context) {
	cur := s.Cfg()
	if cur == nil {
		return
	}
	path := cur.Path()
	if path == "" {
		return
	}
	path = filepath.Clean(path)
	dir := filepath.Dir(path)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("config watcher: %v", err)
		return
	}
	defer w.Close()

	if err := w.Add(dir); err != nil {
		log.Printf("config watcher add %s: %v", dir, err)
		return
	}

	const debounce = 150 * time.Millisecond
	var timer *time.Timer
	fire := make(chan struct{}, 1)
	schedule := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, func() {
			select {
			case fire <- struct{}{}:
			default:
			}
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) != path {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			schedule()
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("config watcher error: %v", err)
		case <-fire:
			next, err := config.Load()
			if err != nil {
				log.Printf("config reload: %v", err)
				continue
			}
			s.cfgMu.Lock()
			s.cfg.Store(next)
			s.cfgMu.Unlock()
		}
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
	mux.HandleFunc("POST /api/llm/config", s.handleLLMConfig)
	mux.HandleFunc("POST /api/llm/key", s.handleLLMKey)
	mux.HandleFunc("POST /api/llm/test", s.handleLLMTest)
	mux.HandleFunc("POST /api/log", s.handleLog)
	mux.HandleFunc("GET /api/brag", s.handleBrag)
	mux.HandleFunc("GET /api/perf-review", s.handlePerfReview)
}

// --- Handlers ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Cfg()
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
		{"git", cfg.Git.Enabled},
		{"slack", cfg.Slack.Enabled},
		{"calendar", cfg.Calendar.Enabled},
		{"github", cfg.GitHub.Enabled},
		{"gitlab", cfg.GitLab.Enabled},
		{"bitbucket", cfg.Bitbucket.Enabled},
		{"jira", cfg.Jira.Enabled},
		{"confluence", cfg.Confluence.Enabled},
		{"linear", cfg.Linear.Enabled},
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
		"llm": map[string]any{
			"provider": cfg.LLM.Provider,
			"model":    cfg.LLM.Model,
		},
		"config_path": cfg.Path(),
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

	activities = privacy.Apply(activities, s.Cfg().Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	var sum summarizer.Summarizer
	if p, llmErr := llm.FromConfig(s.Cfg(), s.tokenStore); llmErr == nil {
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

	activities = privacy.Apply(activities, s.Cfg().Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	var sum summarizer.Summarizer
	if p, llmErr := llm.FromConfig(s.Cfg(), s.tokenStore); llmErr == nil {
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

func (s *Server) handleBrag(w http.ResponseWriter, r *http.Request) {
	after, before, err := s.parsePeriodParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	activities, err := s.db.ListActivities(storage.ActivityFilter{After: after, Before: before})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "querying activities: "+err.Error())
		return
	}

	activities = privacy.Apply(activities, s.Cfg().Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	var childSummaries []models.Summary
	for _, pt := range []string{"daily", "weekly", "monthly"} {
		sums, _ := s.db.ListSummariesInRange(pt, after, before)
		childSummaries = append(childSummaries, sums...)
	}

	var sum summarizer.Summarizer
	if p, llmErr := llm.FromConfig(s.Cfg(), s.tokenStore); llmErr == nil {
		sum = summarizer.NewLLMSummarizer(p).WithPromptLoader(s.promptLoader())
	} else {
		sum = summarizer.NewTemplateSummarizer()
	}

	report, err := sum.BragDoc(activities, childSummaries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generating brag doc: "+err.Error())
		return
	}

	filename := fmt.Sprintf("brag-%s-to-%s.md",
		after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))
	filePath, _ := s.saveReport(report, filename)

	writeJSON(w, http.StatusOK, map[string]any{
		"period_start":   after.Format("2006-01-02"),
		"period_end":     before.AddDate(0, 0, -1).Format("2006-01-02"),
		"report":         report,
		"activity_count": len(activities),
		"file_path":      filePath,
	})
}

func (s *Server) handlePerfReview(w http.ResponseWriter, r *http.Request) {
	after, before, err := s.parsePeriodParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	activities, err := s.db.ListActivities(storage.ActivityFilter{After: after, Before: before})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "querying activities: "+err.Error())
		return
	}

	activities = privacy.Apply(activities, s.Cfg().Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	var childSummaries []models.Summary
	for _, pt := range []string{"daily", "weekly", "monthly"} {
		sums, _ := s.db.ListSummariesInRange(pt, after, before)
		childSummaries = append(childSummaries, sums...)
	}

	var sum summarizer.Summarizer
	if p, llmErr := llm.FromConfig(s.Cfg(), s.tokenStore); llmErr == nil {
		sum = summarizer.NewLLMSummarizer(p).WithPromptLoader(s.promptLoader())
	} else {
		sum = summarizer.NewTemplateSummarizer()
	}

	report, err := sum.PerfReview(activities, childSummaries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generating perf review: "+err.Error())
		return
	}

	filename := fmt.Sprintf("perf-review-%s-to-%s.md",
		after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))
	filePath, _ := s.saveReport(report, filename)

	writeJSON(w, http.StatusOK, map[string]any{
		"period_start":   after.Format("2006-01-02"),
		"period_end":     before.AddDate(0, 0, -1).Format("2006-01-02"),
		"report":         report,
		"activity_count": len(activities),
		"file_path":      filePath,
	})
}

// parsePeriodParam extracts after/before from ?period= or ?after=&before= query params.
func (s *Server) parsePeriodParam(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()

	if after := q.Get("after"); after != "" {
		a, err := time.Parse("2006-01-02", after)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid after date (expected YYYY-MM-DD)")
		}
		b := time.Now().UTC()
		if before := q.Get("before"); before != "" {
			b, err = time.Parse("2006-01-02", before)
			if err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("invalid before date (expected YYYY-MM-DD)")
			}
			b = b.AddDate(0, 0, 1) // inclusive
		}
		return a, b, nil
	}

	// Default: last month.
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	return first, first.AddDate(0, 1, 0), nil
}

// saveReport writes a report to ~/.devrecall/reports/ and returns the full path.
func (s *Server) saveReport(text, filename string) (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	reportsDir := filepath.Join(dir, "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(reportsDir, filename)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return path, nil
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
		// Add a day so the "before" date is inclusive of the whole day.
		filter.Before = t.AddDate(0, 0, 1)
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

	activities = privacy.Apply(activities, s.Cfg().Privacy)

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

// handleChat answers a chat query using the agent loop. It is the
// buffered cousin of handleChatStream — same prompt, same tool catalogue,
// same freshness step — but the response is returned as a single JSON
// blob (with the trace) instead of a Server-Sent Event stream.
//
// Clients that want incremental rendering should use POST /api/chat/stream.
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

	loop, err := s.chatLoop()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	// Pre-agent freshness sync. Buffered handler can't stream lifecycle
	// events to the client mid-flight, so we just drain them and report
	// the syncs that ran in the JSON response.
	freshEvents := s.runChatFreshnessBuffered(r.Context(), false)

	messages := make([]llm.Message, 0, 2+len(req.History))
	messages = append(messages, llm.Message{Role: "system", Content: chatStreamSystemPrompt})
	messages = append(messages, req.History...)
	messages = append(messages, llm.Message{Role: "user", Content: req.Message})

	result, err := loop.Run(r.Context(), messages)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":     "agent: " + err.Error(),
			"trace":     result.Trace,
			"freshness": freshEvents,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"response":      result.Content,
		"steps":         result.Steps,
		"trace":         result.Trace,
		"freshness":     freshEvents,
		// sources_count kept for backwards-compat with existing clients.
		"sources_count": len(result.Trace),
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
	cfg := s.Cfg()
	if !cfg.Git.Enabled {
		return 0
	}
	repos := cfg.Git.Repos
	if len(cfg.Git.ScanPaths) > 0 {
		repos = mergeUnique(repos, git.DiscoverRepos(cfg.Git.ScanPaths))
	}
	emails := mergeUnique(cfg.Git.Emails, git.DetectEmails(repos))

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

func (s *Server) handleLLMConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		BaseURL  string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch req.Provider {
	case "ollama", "openai", "anthropic":
	default:
		writeError(w, http.StatusBadRequest, "provider must be one of: ollama, openai, anthropic")
		return
	}

	// Copy-on-write so concurrent readers see either the old or the new
	// config in full, never a half-mutated one. cfgMu also serializes against
	// WatchConfig in case the file is being externally edited at the same time.
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	next := *s.Cfg()
	next.LLM.Provider = req.Provider
	next.LLM.Model = strings.TrimSpace(req.Model)
	next.LLM.BaseURL = strings.TrimSpace(req.BaseURL)
	if err := next.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	s.cfg.Store(&next)
	writeJSON(w, http.StatusOK, map[string]any{"message": "LLM config saved"})
}

func (s *Server) handleLLMKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Provider != "openai" && req.Provider != "anthropic" {
		writeError(w, http.StatusBadRequest, "key only required for openai or anthropic")
		return
	}
	if strings.TrimSpace(req.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "api_key is required")
		return
	}
	if err := s.tokenStore.Save("llm", req.Provider, llm.APIKeyToken{APIKey: strings.TrimSpace(req.APIKey)}); err != nil {
		writeError(w, http.StatusInternalServerError, "saving key: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "API key saved"})
}

func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	// Optional overrides let the Settings UI test form values without
	// persisting them. When fields are absent, we fall back to the saved
	// config so other callers (CLI, scripts) can just POST an empty body.
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		BaseURL  string `json:"base_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	cfg := *s.Cfg()
	if req.Provider != "" {
		switch req.Provider {
		case "ollama", "openai", "anthropic":
		default:
			writeError(w, http.StatusBadRequest, "provider must be one of: ollama, openai, anthropic")
			return
		}
		cfg.LLM = config.LLMConfig{
			Provider: req.Provider,
			Model:    strings.TrimSpace(req.Model),
			BaseURL:  strings.TrimSpace(req.BaseURL),
		}
	}

	provider, err := llm.FromConfig(&cfg, s.tokenStore)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	resp, err := provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: "ping"},
	}, llm.ChatOpts{MaxTokens: 8})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "LLM responded",
		"provider": provider.Name(),
		"sample":   resp,
	})
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

func (s *Server) promptLoader() *summarizer.PromptLoader {
	dir, err := config.Dir()
	if err != nil {
		return summarizer.NewPromptLoader("")
	}
	return summarizer.NewPromptLoader(dir + "/prompts")
}

// corsMiddleware adds CORS headers for local-only access (Tauri webview, dev
// server on localhost:5173, or curl). The API binds to 127.0.0.1 only, so a
// permissive `*` is safe — there's no remote origin that can reach it.
//
// We previously echoed back r.Header.Get("Origin"), but Tauri 2's webview on
// macOS sometimes doesn't send Origin for tauri://localhost → http://127.0.0.1
// fetches, leaving the header empty and tripping the browser's CORS check.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
