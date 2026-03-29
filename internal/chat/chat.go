package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// Session holds the state of an interactive chat session.
type Session struct {
	in  io.Reader
	out io.Writer
}

// NewSession creates a chat session reading from in and writing to out.
func NewSession(in io.Reader, out io.Writer) *Session {
	return &Session{in: in, out: out}
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

		// TODO: pass input through RAG pipeline and stream LLM response
		fmt.Fprintln(s.out, "chat: not yet implemented")
		fmt.Fprintln(s.out)
	}

	return scanner.Err()
}

func (s *Session) handleCommand(cmd string) (quit bool) {
	switch cmd {
	case "/quit", "/exit":
		fmt.Fprintln(s.out, "Bye!")
		return true
	case "/help":
		fmt.Fprintln(s.out, "Commands:")
		fmt.Fprintln(s.out, "  /help      Show this help")
		fmt.Fprintln(s.out, "  /quit      Exit chat")
		fmt.Fprintln(s.out, "  /sources   Show indexed sources")
		fmt.Fprintln(s.out, "  /stats     Show memory stats")
		fmt.Fprintln(s.out, "  /clear     Clear conversation history")
		fmt.Fprintln(s.out)
	case "/clear":
		fmt.Fprintln(s.out, "Conversation cleared.")
		fmt.Fprintln(s.out)
	default:
		fmt.Fprintf(s.out, "Unknown command: %s (type /help)\n\n", cmd)
	}
	return false
}
