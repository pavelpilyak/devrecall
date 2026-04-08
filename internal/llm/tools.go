package llm

import (
	"context"
	"encoding/json"
)

// Tool describes a function the model can call.
//
// Schema is a JSON Schema document describing the argument object.
// Provider implementations are responsible for translating it into their
// native tool-definition shape.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ToolCall is a single tool invocation requested by the model.
//
// Arguments is the raw JSON object the model produced; tool executors are
// responsible for unmarshalling it into their own typed argument struct.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ChatResponse is the buffered result of a tool-calling chat turn.
//
// If the model produced a final text answer, Content is non-empty and
// ToolCalls is nil. If the model wants to call tools, ToolCalls is non-empty
// (Content may also be set if the model emitted preamble text).
type ChatResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// StreamEventType identifies the kind of event produced by a streaming
// tool-calling chat call.
type StreamEventType string

const (
	// StreamEventToken is a chunk of assistant text.
	StreamEventToken StreamEventType = "token"
	// StreamEventToolCallStart marks the beginning of a tool call. ToolCall
	// carries the ID and Name; Arguments may be empty at this point.
	StreamEventToolCallStart StreamEventType = "tool_call_start"
	// StreamEventToolCallDelta carries an incremental update to a tool
	// call's arguments (a JSON fragment).
	StreamEventToolCallDelta StreamEventType = "tool_call_delta"
	// StreamEventToolCallEnd marks completion of a tool call. ToolCall
	// carries the fully assembled Arguments.
	StreamEventToolCallEnd StreamEventType = "tool_call_end"
	// StreamEventDone signals the end of the stream.
	StreamEventDone StreamEventType = "done"
	// StreamEventError reports a fatal stream error. Err is set; the
	// channel is closed after this event.
	StreamEventError StreamEventType = "error"
)

// StreamEvent is a single event emitted by ChatWithToolsStream.
type StreamEvent struct {
	Type     StreamEventType
	Token    string
	ToolCall *ToolCall
	Err      error
}

// ToolCallingProvider is implemented by providers that support tool calling.
//
// SupportsTools may perform a one-shot capability check (e.g. Ollama
// querying /api/show) and is allowed to take a context for that purpose.
type ToolCallingProvider interface {
	Provider
	ChatWithTools(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (ChatResponse, error)
	ChatWithToolsStream(ctx context.Context, messages []Message, tools []Tool, opts ChatOpts) (<-chan StreamEvent, error)
	SupportsTools(ctx context.Context) bool
	SupportsStreaming() bool
}
