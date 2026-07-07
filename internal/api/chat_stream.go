package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/pavelpilyak/devrecall/internal/agent"
	agenttools "github.com/pavelpilyak/devrecall/internal/agent/tools"
	"github.com/pavelpilyak/devrecall/internal/chat/freshness"
	"github.com/pavelpilyak/devrecall/internal/embedding"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/workitem"
)

const chatStreamSystemPrompt = `You are DevRecall, a developer work-memory assistant. You answer questions about the user's work history by calling the read-only tools provided to you.

Tools available:
- current_time: get the user's current local time. Call this before any date-relative query so you can convert "yesterday"/"last week"/etc. to absolute dates.
- list_activities / count_activities: enumerate or count activities with filters (start, end, source, type, identity_id, tag, group_by).
- search_activities: FTS5 keyword search over titles and content (optional tag filter).
- semantic_search_activities: vector search by meaning (only when keyword search fails).
- get_activity: fetch the full body of a single activity by id.
- get_work_item / list_work_items: work items group a ticket with its commits, PRs, and discussions. get_work_item returns one item's full cross-source timeline; list_work_items answers "what was I working on".
- list_summaries / get_summary: read pre-computed standup/weekly/monthly/quarterly summaries.
- list_identities / resolve_person: look up people the user has worked with.

Rules:
- Always call current_time before making date-based queries; do not assume what "today" is.
- For questions about one ticket or piece of work ("everything about PROJ-123", "status of the auth fix"), prefer get_work_item — it returns the linked ticket + commits + PRs in one call.
- For "what was I working on <period>", prefer list_work_items over raw activity listing.
- Activity rows may carry a digest (one-line factual summary) and tags — use them before fetching full bodies.
- Prefer count_activities + list_activities over dumping all rows. Only fetch full bodies you need with get_activity.
- Answer based ONLY on tool results. If the tools return nothing, say so plainly — never invent commits, PRs, or people.
- Be concise: cite dates, repo names, ticket IDs, and people that appear in the tool output.
- Use conversation history to resolve follow-ups.`

// chatStreamRequest is the JSON body of POST /api/chat/stream.
type chatStreamRequest struct {
	Message string        `json:"message"`
	History []llm.Message `json:"history,omitempty"`
}

// handleChatStream serves the agent loop as Server-Sent Events.
//
// The body is the same as POST /api/chat. The response is text/event-stream
// with these event names:
//
//	thinking      {"step":N}
//	token         {"text":"..."}
//	tool_call     {"step":N,"name":"...","args":{...}}
//	tool_result   {"step":N,"name":"...","result":{...},"error":"...","duration_ms":N}
//	done          {"content":"final assistant text","step":N}
//	error         {"error":"..."}
//
// The connection is closed after a terminal `done` or `error` event.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req chatStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "missing required field: message")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported by this server")
		return
	}

	loop, err := s.chatLoop()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Pre-agent freshness sync: keep stale local sources up to date
	// before the agent loop starts. Stream each lifecycle event as a
	// `freshness` SSE frame so the UI can show "Syncing slack…".
	s.runChatFreshness(r, w, flusher, false)

	messages := make([]llm.Message, 0, 2+len(req.History))
	messages = append(messages, llm.Message{Role: "system", Content: chatStreamSystemPrompt})
	messages = append(messages, req.History...)
	messages = append(messages, llm.Message{Role: "user", Content: req.Message})

	events := loop.RunStream(r.Context(), messages)
	for ev := range events {
		writeSSE(w, flusher, ev)
	}
}

// chatLoop returns the agent loop used by the chat handlers, building it
// lazily from the configured LLM provider + embedder. Tests can override
// this by setting Server.agentLoopFactory.
func (s *Server) chatLoop() (*agent.Loop, error) {
	if s.agentLoopFactory != nil {
		return s.agentLoopFactory()
	}

	cfg := s.Cfg()
	llmProvider, err := llm.FromConfig(cfg, s.tokenStore)
	if err != nil {
		return nil, fmt.Errorf("LLM not configured: %w", err)
	}
	toolProvider, ok := llmProvider.(llm.ToolCallingProvider)
	if !ok {
		return nil, fmt.Errorf("LLM provider %q does not support tool calling", llmProvider.Name())
	}

	embedder, err := embedding.FromConfig(cfg, s.tokenStore)
	if err != nil {
		// Embedder is optional — semantic_search_activities will return an
		// error at call time if it's nil.
		embedder = nil
	}

	registry := agenttools.NewRegistry(agenttools.Deps{
		DB:       s.db,
		Embedder: embedder,
	})
	return agent.NewLoop(toolProvider, registry, agent.LoopOptions{}), nil
}

// runChatFreshness invokes the freshness checker before the agent loop
// starts and re-emits each Event as an SSE `freshness` frame so the UI
// can render "Syncing slack…" / "slack synced (12 new)" lines without
// blocking on the agent. force=true bypasses TTLs and the Enabled flag.
//
// Tests can override the wired checker/syncers via the
// freshnessCheckerFactory and freshnessSyncerFactory hooks on Server.
func (s *Server) runChatFreshness(r *http.Request, w http.ResponseWriter, flusher http.Flusher, force bool) {
	checker, syncers := s.chatFreshness()
	if checker == nil || len(syncers) == 0 {
		return
	}
	added := 0
	for ev := range checker.Run(r.Context(), syncers, force) {
		if ev.Status == freshness.StatusSynced {
			added += ev.Added
		}
		payload, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "event: freshness\ndata: %s\n\n", payload)
		flusher.Flush()
	}
	s.linkAfterFreshness(added)
}

// linkAfterFreshness re-materializes work items when a freshness sync
// brought in new activities, so agent tools see up-to-date links. Only
// the deterministic linking stage runs here — LLM enrichment latency is
// unacceptable mid-chat; the next full sync catches up.
func (s *Server) linkAfterFreshness(added int) {
	if added == 0 {
		return
	}
	if _, err := workitem.Materialize(s.db); err != nil {
		fmt.Fprintf(os.Stderr, "Work-item linking warning: %v\n", err)
	}
}

// runChatFreshnessBuffered runs the freshness checker for the buffered
// (non-streaming) chat handler and returns the events instead of writing
// them to a response. The non-streaming JSON handler reports them under
// a "freshness" key so callers can still see what was synced.
func (s *Server) runChatFreshnessBuffered(ctx context.Context, force bool) []freshness.Event {
	checker, syncers := s.chatFreshness()
	if checker == nil || len(syncers) == 0 {
		return nil
	}
	events := freshness.Collect(checker.Run(ctx, syncers, force))
	added := 0
	for _, ev := range events {
		if ev.Status == freshness.StatusSynced {
			added += ev.Added
		}
	}
	s.linkAfterFreshness(added)
	return events
}

// chatFreshness returns the freshness checker + syncers used by the
// chat-stream handler. Tests inject fakes via freshnessFactory; in
// production it's built lazily from cfg + db on each request (cheap —
// no I/O until Run is called).
func (s *Server) chatFreshness() (*freshness.Checker, map[string]freshness.Syncer) {
	if s.freshnessFactory != nil {
		return s.freshnessFactory()
	}
	cfg := s.Cfg()
	return BuildFreshnessChecker(cfg, s.db), BuildFreshnessSyncers(cfg, s.db)
}

// writeSSE serialises one AgentEvent as an SSE event frame.
//
// The event name is taken from ev.Type so clients can demultiplex with
// EventSource.addEventListener("tool_call", …).
func writeSSE(w http.ResponseWriter, flusher http.Flusher, ev agent.AgentEvent) {
	payload, err := json.Marshal(ev)
	if err != nil {
		// Best-effort: emit an error frame and bail.
		fmt.Fprintf(w, "event: error\ndata: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
	flusher.Flush()
}
