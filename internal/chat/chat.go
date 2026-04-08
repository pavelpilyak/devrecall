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

	"github.com/pavelpiliak/devrecall/internal/agent"
	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/internal/storage"
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

func (s *Session) handleQuery(ctx context.Context, query string) error {
	// Assemble messages: system + history + current.
	messages := make([]llm.Message, 0, 2+len(s.history))
	messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, s.history...)
	messages = append(messages, llm.Message{Role: "user", Content: query})

	fmt.Fprintf(s.out, "Thinking...")
	res, err := s.loop.Run(ctx, messages)
	// Always store the trace, even on error, so /trace can show what was attempted.
	s.lastTrace = res.Trace
	if err != nil {
		fmt.Fprintln(s.out)
		return fmt.Errorf("agent: %w", err)
	}

	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, res.Content)
	fmt.Fprintln(s.out)

	// Append to conversation history.
	s.history = append(s.history,
		llm.Message{Role: "user", Content: query},
		llm.Message{Role: "assistant", Content: res.Content},
	)

	// Trim history to last N pairs.
	if len(s.history) > maxHistory*2 {
		s.history = s.history[len(s.history)-maxHistory*2:]
	}

	return nil
}

func (s *Session) handleCommand(_ context.Context, cmd string) (quit bool) {
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
