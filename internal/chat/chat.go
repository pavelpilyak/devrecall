package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/internal/rag"
	"github.com/pavelpiliak/devrecall/internal/storage"
)

const maxHistory = 10 // keep last N user+assistant message pairs

const systemPrompt = `You are DevRecall, a developer work memory assistant. You answer questions about the user's work history based on retrieved activity context.

Rules:
- Answer based ONLY on the provided context. If context doesn't contain enough information, say so.
- Be concise and specific — cite dates, repo names, ticket IDs, and people when available.
- Use natural language, not bullet dumps (unless the user asks for a list).
- If the user asks a follow-up, use conversation history to understand what they're referring to.
- Never make up activities, commits, or people that aren't in the context.`

// Session holds the state of an interactive chat session.
type Session struct {
	in        io.Reader
	out       io.Writer
	retriever rag.Retriever
	llm       llm.Provider
	db        *storage.DB
	history   []llm.Message

	// lastContext stores the retrieved results from the most recent query.
	lastContext []rag.Result

	// dateFilter constrains subsequent queries to a date range.
	dateAfter  time.Time
	dateBefore time.Time
}

// NewSession creates a chat session with RAG retrieval and LLM generation.
func NewSession(in io.Reader, out io.Writer, retriever rag.Retriever, provider llm.Provider, db *storage.DB) *Session {
	return &Session{
		in:        in,
		out:       out,
		retriever: retriever,
		llm:       provider,
		db:        db,
		history:   nil,
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
	// Retrieve relevant activities (with date filter if set).
	var results []rag.Result
	var err error

	if !s.dateAfter.IsZero() || !s.dateBefore.IsZero() {
		if fr, ok := s.retriever.(filterRetriever); ok {
			results, err = fr.RetrieveWithFilters(ctx, query, 10, rag.QueryFilters{
				After:  s.dateAfter,
				Before: s.dateBefore,
			})
		} else {
			results, err = s.retriever.Retrieve(ctx, query, 10)
		}
	} else {
		results, err = s.retriever.Retrieve(ctx, query, 10)
	}
	if err != nil {
		return fmt.Errorf("retrieval failed: %w", err)
	}

	// Store for /context command.
	s.lastContext = results

	// Build context from retrieved activities.
	contextStr := formatContext(results)

	// Build the user message with retrieved context injected.
	userMsg := query
	if contextStr != "" {
		userMsg = fmt.Sprintf("Context from work history:\n%s\n\nUser question: %s", contextStr, query)
	}

	// Assemble messages: system + history + current.
	messages := make([]llm.Message, 0, 2+len(s.history))
	messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, s.history...)
	messages = append(messages, llm.Message{Role: "user", Content: userMsg})

	// Call LLM.
	response, err := s.llm.Chat(ctx, messages, llm.ChatOpts{})
	if err != nil {
		return fmt.Errorf("LLM failed: %w", err)
	}

	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, response)
	fmt.Fprintln(s.out)

	// Append to conversation history (store the raw query, not the context-injected one).
	s.history = append(s.history,
		llm.Message{Role: "user", Content: query},
		llm.Message{Role: "assistant", Content: response},
	)

	// Trim history to last N pairs.
	if len(s.history) > maxHistory*2 {
		s.history = s.history[len(s.history)-maxHistory*2:]
	}

	return nil
}

// filterRetriever is an optional interface for retrievers that support filters.
type filterRetriever interface {
	RetrieveWithFilters(ctx context.Context, query string, limit int, filters rag.QueryFilters) ([]rag.Result, error)
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
		fmt.Fprintln(s.out, "  /context           Show retrieved context for last answer")
		fmt.Fprintln(s.out, "  /date <range>      Set date filter (e.g. 'last week', '2026-03', 'clear')")
		fmt.Fprintln(s.out, "  /stats             Show activity statistics")
		fmt.Fprintln(s.out)

	case "/clear":
		s.history = nil
		s.lastContext = nil
		fmt.Fprintln(s.out, "Conversation cleared.")
		fmt.Fprintln(s.out)

	case "/search":
		s.cmdSearch(arg)

	case "/context":
		s.cmdContext()

	case "/date":
		s.cmdDate(arg)

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

	filter := storage.ActivityFilter{
		After:  s.dateAfter,
		Before: s.dateBefore,
	}
	results, err := s.db.SearchFTS(query, filter, 20)
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

func (s *Session) cmdContext() {
	if len(s.lastContext) == 0 {
		fmt.Fprintln(s.out, "No context available — ask a question first.")
		fmt.Fprintln(s.out)
		return
	}

	fmt.Fprintf(s.out, "Retrieved %d activities for last query:\n", len(s.lastContext))
	for i, r := range s.lastContext {
		a := r.Activity
		fmt.Fprintf(s.out, "  %d. [%s] %s | %s | %s (score: %.4f)\n",
			i+1, a.Timestamp.Format("2006-01-02"), a.Source, a.Type, a.Title, r.Score)
	}
	fmt.Fprintln(s.out)
}

func (s *Session) cmdDate(arg string) {
	if arg == "" {
		if s.dateAfter.IsZero() && s.dateBefore.IsZero() {
			fmt.Fprintln(s.out, "No date filter set.")
		} else {
			fmt.Fprintf(s.out, "Date filter: %s to %s\n",
				formatDateOrOpen(s.dateAfter, "beginning"),
				formatDateOrOpen(s.dateBefore, "now"))
		}
		fmt.Fprintln(s.out)
		return
	}

	if arg == "clear" || arg == "reset" || arg == "off" {
		s.dateAfter = time.Time{}
		s.dateBefore = time.Time{}
		fmt.Fprintln(s.out, "Date filter cleared.")
		fmt.Fprintln(s.out)
		return
	}

	after, before, err := parseDateRange(arg)
	if err != nil {
		fmt.Fprintf(s.out, "Could not parse date range %q: %v\n", arg, err)
		fmt.Fprintln(s.out, "Examples: 'last week', 'last month', '2026-03', '2026-03-01..2026-03-31', 'clear'")
		fmt.Fprintln(s.out)
		return
	}

	s.dateAfter = after
	s.dateBefore = before
	fmt.Fprintf(s.out, "Date filter set: %s to %s\n",
		after.Format("2006-01-02"), before.Format("2006-01-02"))
	fmt.Fprintln(s.out)
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

// formatContext turns retrieved results into a text block for the LLM prompt.
func formatContext(results []rag.Result) string {
	if len(results) == 0 {
		return ""
	}

	var b strings.Builder
	for i, r := range results {
		a := r.Activity
		fmt.Fprintf(&b, "[%d] %s | %s | %s | %s",
			i+1, a.Timestamp.Format("2006-01-02"), a.Source, a.Type, a.Title)
		if a.Content != "" {
			// Truncate long content to avoid blowing up the context window.
			content := a.Content
			if len(content) > 300 {
				content = content[:300] + "..."
			}
			fmt.Fprintf(&b, "\n    %s", content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseDateRange converts common date expressions to after/before timestamps.
func parseDateRange(s string) (after, before time.Time, err error) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	switch strings.ToLower(s) {
	case "today":
		return today, today.AddDate(0, 0, 1), nil
	case "yesterday":
		y := today.AddDate(0, 0, -1)
		return y, today, nil
	case "last week", "this week":
		// Go back to Monday of this/last week.
		weekday := int(today.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		monday := today.AddDate(0, 0, -(weekday - 1))
		if strings.ToLower(s) == "last week" {
			monday = monday.AddDate(0, 0, -7)
		}
		return monday, monday.AddDate(0, 0, 7), nil
	case "last month", "this month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		if strings.ToLower(s) == "last month" {
			first = first.AddDate(0, -1, 0)
		}
		return first, first.AddDate(0, 1, 0), nil
	}

	// Try range format: 2026-03-01..2026-03-31
	if idx := strings.Index(s, ".."); idx > 0 {
		a, errA := time.Parse("2006-01-02", s[:idx])
		b, errB := time.Parse("2006-01-02", s[idx+2:])
		if errA != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start date: %w", errA)
		}
		if errB != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end date: %w", errB)
		}
		return a, b.AddDate(0, 0, 1), nil
	}

	// Try YYYY-MM (month)
	if t, err := time.Parse("2006-01", s); err == nil {
		return t, t.AddDate(0, 1, 0), nil
	}

	// Try YYYY-MM-DD (single day)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, t.AddDate(0, 0, 1), nil
	}

	return time.Time{}, time.Time{}, fmt.Errorf("unrecognized format")
}

func formatDateOrOpen(t time.Time, fallback string) string {
	if t.IsZero() {
		return fallback
	}
	return t.Format("2006-01-02")
}
