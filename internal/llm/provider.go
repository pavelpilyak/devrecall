package llm

import "context"

// Message represents a single message in a chat conversation.
//
// For tool-calling flows, extra fields carry tool-call metadata:
//   - An assistant message that emitted tool calls sets ToolCalls.
//   - A tool-result message uses Role=="tool" with ToolCallID set to the
//     ID of the call it is answering and Content holding the result payload.
type Message struct {
	Role    string // "system", "user", "assistant", "tool"
	Content string

	// ToolCalls is populated on assistant messages that requested tool
	// invocations. Empty for normal text replies.
	ToolCalls []ToolCall

	// ToolCallID is populated on tool-result messages (Role=="tool") and
	// identifies which ToolCall the content is answering.
	ToolCallID string
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
