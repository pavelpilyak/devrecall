// Package chat hosts the interactive REPL that talks to the agent loop.
//
// As of the agentic-chat rewrite, this package is intentionally thin: it
// owns input parsing, slash commands, and conversation history, and
// delegates every question to internal/agent.Loop. There is no longer any
// RAG retrieval, date-hint extraction, or context formatting in here —
// those concerns moved into the tool catalogue (internal/agent/tools).
package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pavelpilyak/devrecall/internal/agent"
	"github.com/pavelpilyak/devrecall/internal/chat/freshness"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
)

const maxHistory = 10 // keep last N user+assistant message pairs

const systemPrompt = `You are DevRecall, a developer work-memory assistant. You answer questions about the user's work history by calling the read-only tools provided to you.

Tools available:
- current_time: get the user's current local time. Call this whenever the question contains a relative date ("yesterday", "last week", "this month") so you can convert it to absolute dates.
- list_activities / count_activities: enumerate or count activities with filters (start, end, source, type, identity_id, group_by).
- search_activities: FTS5 keyword search over titles and content.
- semantic_search_activities: vector search by meaning (for "anything about auth refactoring").
- get_activity: fetch the full body of a single activity by id.
- list_summaries / get_summary: read pre-computed standup/weekly/monthly/quarterly summaries.
- list_identities / resolve_person: look up people the user has worked with.

Rules:
- Always call current_time before making date-based queries; do not assume what "today" is.
- Prefer count_activities + list_activities over dumping all rows. Only fetch the bodies you need with get_activity.
- Answer based ONLY on tool results. If the tools return nothing, say so plainly — never invent commits, PRs, or people.
- Be concise: cite dates, repo names, ticket IDs, and people that appear in the tool output.
- Use conversation history to resolve follow-ups ("what about the day before?", "show me more from that PR").`

// Session holds the state of an interactive chat session.
type Session struct {
	in      io.Reader
	out     io.Writer
	loop    *agent.Loop
	db      *storage.DB
	history []llm.Message

	// freshness, if non-nil, is run before each query against syncers
	// to keep stale sources up to date. Both fields are usually wired
	// up together; either being nil disables the step entirely.
	freshness *freshness.Checker
	syncers   map[string]freshness.Syncer

	// lastTrace holds the tool-call trace from the most recent agent run,
	// so /trace can show what the agent did.
	lastTrace []agent.TraceStep
}

// NewSession creates a chat session driven by an agent loop.
//
// The db handle is retained only for slash commands that query SQLite
// directly (/search, /stats); the agent itself goes through the loop's
// tool registry.
func NewSession(in io.Reader, out io.Writer, loop *agent.Loop, db *storage.DB) *Session {
	return &Session{
		in:   in,
		out:  out,
		loop: loop,
		db:   db,
	}
}

// WithFreshness wires a pre-agent sync step into this session. Before
// each query the Checker runs the provided syncers; stale sources emit
// "Syncing …" / "synced N new" lines and the agent loop only starts
// after the wait cap. The /sync slash command forces a refresh of every
// registered source on demand.
func (s *Session) WithFreshness(checker *freshness.Checker, syncers map[string]freshness.Syncer) *Session {
	s.freshness = checker
	s.syncers = syncers
	return s
}

// Run starts the interactive chat REPL. It blocks until the user types /quit or input ends.
func (s *Session) Run(ctx context.Context) error {
	fmt.Fprintln(s.out, "DevRecall Chat — ask anything about your work history.")
	fmt.Fprintln(s.out, "Type /help for commands, /quit to exit.")
	fmt.Fprintln(s.out)

	scanner := bufio.NewScanner(s.in)
	for {
		fmt.Fprint(s.out, "> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/") {
			if done := s.handleCommand(ctx, input); done {
				return nil
			}
			continue
		}

		if err := s.handleQuery(ctx, input); err != nil {
			fmt.Fprintf(s.out, "Error: %v\n\n", err)
		}
	}

	return scanner.Err()
}

// runFreshness runs the freshness checker (if wired) and renders any
// emitted events to the REPL output. force=true is used by the /sync
// slash command to bypass TTLs and the Enabled flag.
//
// Errors from individual syncers are surfaced inline as " ! source: err"
// lines but never abort the chat loop — the agent should still try to
// answer with whatever data is already in SQLite.
func (s *Session) runFreshness(ctx context.Context, force bool) {
	if s.freshness == nil || len(s.syncers) == 0 {
		if force {
			fmt.Fprintln(s.out, "(no syncers wired)")
			fmt.Fprintln(s.out)
		}
		return
	}

	events := s.freshness.Run(ctx, s.syncers, force)
	rendered := false
	for ev := range events {
		switch ev.Status {
		case freshness.StatusSyncing:
			fmt.Fprintf(s.out, "⟳ Syncing %s…\n", ev.Source)
			rendered = true
		case freshness.StatusSynced:
			if ev.Added > 0 {
				fmt.Fprintf(s.out, "✓ %s synced (%d new)\n", ev.Source, ev.Added)
			} else {
				fmt.Fprintf(s.out, "✓ %s synced\n", ev.Source)
			}
			rendered = true
		case freshness.StatusError:
			fmt.Fprintf(s.out, "! %s sync failed: %s\n", ev.Source, ev.Err)
			rendered = true
		case freshness.StatusFresh:
			// Only emitted on a forced run.
			fmt.Fprintf(s.out, "· %s fresh\n", ev.Source)
			rendered = true
		}
	}
	if rendered {
		fmt.Fprintln(s.out)
	}
}

func (s *Session) handleQuery(ctx context.Context, query string) error {
	// Pre-agent freshness sync — silent when everything is fresh.
	s.runFreshness(ctx, false)

	// Assemble messages: system + history + current.
	messages := make([]llm.Message, 0, 2+len(s.history))
	messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, s.history...)
	messages = append(messages, llm.Message{Role: "user", Content: query})

	events := s.loop.RunStream(ctx, messages)

	var (
		answer    strings.Builder
		trace     []agent.TraceStep
		streamErr string
		// inAnswer becomes true after the first token of the final answer
		// is rendered, so we can prefix it with a blank line cleanly.
		answerStarted bool
	)

	for ev := range events {
		switch ev.Type {
		case agent.AgentEventThinking:
			// Show "thinking" between tool steps so the user can tell the
			// agent is alive even before the first token streams in.
			if ev.Step > 1 {
				fmt.Fprintln(s.out)
			}
			fmt.Fprintf(s.out, "✦ thinking (step %d)…\n", ev.Step)

		case agent.AgentEventToken:
			if !answerStarted {
				fmt.Fprintln(s.out)
				answerStarted = true
			}
			fmt.Fprint(s.out, ev.Token)
			answer.WriteString(ev.Token)

		case agent.AgentEventToolCall:
			fmt.Fprintf(s.out, "→ %s(%s)\n", ev.ToolName, compactArgs(ev.ToolArgs))

		case agent.AgentEventToolResult:
			step := agent.TraceStep{
				Step:       ev.Step,
				ToolName:   ev.ToolName,
				ToolArgs:   ev.ToolArgs,
				Result:     ev.ToolResult,
				Error:      ev.ToolError,
				DurationMs: ev.DurationMs,
			}
			trace = append(trace, step)
			if ev.ToolError != "" {
				fmt.Fprintf(s.out, "  ← error: %s (%dms)\n", ev.ToolError, ev.DurationMs)
			} else {
				fmt.Fprintf(s.out, "  ← %s (%dms)\n", previewJSON(ev.ToolResult, 80), ev.DurationMs)
			}

		case agent.AgentEventDone:
			// If the model produced a content-only Done (no streamed
			// tokens, e.g. non-streaming provider in tool-only loops),
			// surface its content here.
			if !answerStarted && ev.Content != "" {
				fmt.Fprintln(s.out)
				fmt.Fprint(s.out, ev.Content)
				answer.WriteString(ev.Content)
			}

		case agent.AgentEventError:
			streamErr = ev.Err
		}
	}

	s.lastTrace = trace

	if streamErr != "" {
		fmt.Fprintln(s.out)
		return fmt.Errorf("agent: %s", streamErr)
	}

	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out)

	// Append to conversation history.
	s.history = append(s.history,
		llm.Message{Role: "user", Content: query},
		llm.Message{Role: "assistant", Content: answer.String()},
	)

	// Trim history to last N pairs.
	if len(s.history) > maxHistory*2 {
		s.history = s.history[len(s.history)-maxHistory*2:]
	}

	return nil
}

func (s *Session) handleCommand(ctx context.Context, cmd string) (quit bool) {
	parts := strings.SplitN(cmd, " ", 2)
	command := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch command {
	case "/quit", "/exit":
		fmt.Fprintln(s.out, "Bye!")
		return true

	case "/help":
		fmt.Fprintln(s.out, "Commands:")
		fmt.Fprintln(s.out, "  /help              Show this help")
		fmt.Fprintln(s.out, "  /quit              Exit chat")
		fmt.Fprintln(s.out, "  /clear             Clear conversation history")
		fmt.Fprintln(s.out, "  /search <query>    FTS5 keyword search (no LLM)")
		fmt.Fprintln(s.out, "  /trace             Show tool calls from the last answer")
		fmt.Fprintln(s.out, "  /stats             Show activity statistics")
		fmt.Fprintln(s.out, "  /sync              Force re-sync of every wired source")
		fmt.Fprintln(s.out)

	case "/clear":
		s.history = nil
		s.lastTrace = nil
		fmt.Fprintln(s.out, "Conversation cleared.")
		fmt.Fprintln(s.out)

	case "/search":
		s.cmdSearch(arg)

	case "/trace":
		s.cmdTrace()

	case "/stats":
		s.cmdStats()

	case "/sync":
		s.runFreshness(ctx, true)

	default:
		fmt.Fprintf(s.out, "Unknown command: %s (type /help)\n\n", cmd)
	}
	return false
}

func (s *Session) cmdSearch(query string) {
	if query == "" {
		fmt.Fprintln(s.out, "Usage: /search <query>")
		fmt.Fprintln(s.out)
		return
	}
	if s.db == nil {
		fmt.Fprintln(s.out, "Search not available (no database connection).")
		fmt.Fprintln(s.out)
		return
	}

	results, err := s.db.SearchFTS(query, storage.ActivityFilter{}, 20)
	if err != nil {
		fmt.Fprintf(s.out, "Search error: %v\n\n", err)
		return
	}

	if len(results) == 0 {
		fmt.Fprintln(s.out, "No results found.")
		fmt.Fprintln(s.out)
		return
	}

	fmt.Fprintf(s.out, "Found %d results:\n", len(results))
	for i, r := range results {
		a := r.Activity
		fmt.Fprintf(s.out, "  %d. [%s] %s | %s | %s\n",
			i+1, a.Timestamp.Format("2006-01-02"), a.Source, a.Type, a.Title)
	}
	fmt.Fprintln(s.out)
}

func (s *Session) cmdTrace() {
	if len(s.lastTrace) == 0 {
		fmt.Fprintln(s.out, "No trace available — ask a question first.")
		fmt.Fprintln(s.out)
		return
	}

	fmt.Fprintf(s.out, "Last answer used %d tool call(s):\n", len(s.lastTrace))
	for i, t := range s.lastTrace {
		args := strings.TrimSpace(string(t.ToolArgs))
		if args == "" {
			args = "{}"
		}
		fmt.Fprintf(s.out, "  %d. %s(%s) — %dms",
			i+1, t.ToolName, args, t.DurationMs)
		if t.Error != "" {
			fmt.Fprintf(s.out, " ERROR: %s", t.Error)
		} else if len(t.Result) > 0 {
			preview := compactJSON(t.Result)
			if len(preview) > 120 {
				preview = preview[:117] + "..."
			}
			fmt.Fprintf(s.out, " → %s", preview)
		}
		fmt.Fprintln(s.out)
	}
	fmt.Fprintln(s.out)
}

// compactJSON re-marshals a JSON blob without indentation so trace previews
// fit on one line. Falls back to the raw bytes on parse errors.
func compactJSON(raw json.RawMessage) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// compactArgs renders tool-call arguments for the streaming "→ tool(args)"
// line. Empty objects collapse to nothing so the line stays clean.
func compactArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	c := compactJSON(raw)
	if c == "{}" {
		return ""
	}
	return c
}

// previewJSON compacts a JSON blob and truncates it to max characters
// (with an ellipsis) so streamed tool result lines stay one-line.
func previewJSON(raw json.RawMessage, max int) string {
	if len(raw) == 0 {
		return "{}"
	}
	c := compactJSON(raw)
	if max > 3 && len(c) > max {
		return c[:max-3] + "..."
	}
	return c
}

func (s *Session) cmdStats() {
	if s.db == nil {
		fmt.Fprintln(s.out, "Stats not available (no database connection).")
		fmt.Fprintln(s.out)
		return
	}

	stats, err := s.db.Stats()
	if err != nil {
		fmt.Fprintf(s.out, "Stats error: %v\n\n", err)
		return
	}

	fmt.Fprintf(s.out, "Activities: %d total\n", stats.TotalCount)
	if len(stats.BySource) > 0 {
		fmt.Fprintln(s.out, "By source:")
		for source, count := range stats.BySource {
			fmt.Fprintf(s.out, "  %-12s %d\n", source, count)
		}
	}
	if !stats.OldestTime.IsZero() {
		fmt.Fprintf(s.out, "Date range: %s to %s\n",
			stats.OldestTime.Format("2006-01-02"),
			stats.NewestTime.Format("2006-01-02"))
	}
	fmt.Fprintf(s.out, "Embeddings: %d\n", stats.EmbeddedCount)
	fmt.Fprintln(s.out)
}
