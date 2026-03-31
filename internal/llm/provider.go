package llm

import "context"

// Message represents a single message in a chat conversation.
type Message struct {
	Role    string // "system", "user", "assistant"
	Content string
}

// ChatOpts controls LLM generation behavior.
type ChatOpts struct {
	Model       string
	Temperature float64
	MaxTokens   int
}

// Provider is the abstraction for LLM backends.
// Implementations: Ollama (local), OpenAI, Anthropic.
type Provider interface {
	// Chat sends messages to the LLM and returns the response text.
	Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error)

	// Name returns the provider identifier ("ollama", "openai", "anthropic").
	Name() string
}
