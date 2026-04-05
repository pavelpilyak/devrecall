package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/pavelpiliak/devrecall/internal/llm"
	"github.com/pavelpiliak/devrecall/internal/rag"
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
	history   []llm.Message
}

// NewSession creates a chat session with RAG retrieval and LLM generation.
func NewSession(in io.Reader, out io.Writer, retriever rag.Retriever, provider llm.Provider) *Session {
	return &Session{
		in:        in,
		out:       out,
		retriever: retriever,
		llm:       provider,
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
			if done := s.handleCommand(input); done {
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
	// Retrieve relevant activities.
	results, err := s.retriever.Retrieve(ctx, query, 10)
	if err != nil {
		return fmt.Errorf("retrieval failed: %w", err)
	}

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

func (s *Session) handleCommand(cmd string) (quit bool) {
	switch {
	case cmd == "/quit" || cmd == "/exit":
		fmt.Fprintln(s.out, "Bye!")
		return true
	case cmd == "/help":
		fmt.Fprintln(s.out, "Commands:")
		fmt.Fprintln(s.out, "  /help      Show this help")
		fmt.Fprintln(s.out, "  /quit      Exit chat")
		fmt.Fprintln(s.out, "  /clear     Clear conversation history")
		fmt.Fprintln(s.out)
	case cmd == "/clear":
		s.history = nil
		fmt.Fprintln(s.out, "Conversation cleared.")
		fmt.Fprintln(s.out)
	default:
		fmt.Fprintf(s.out, "Unknown command: %s (type /help)\n\n", cmd)
	}
	return false
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
