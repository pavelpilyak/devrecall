package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pavelpiliak/devrecall/internal/llm"
)

// streamingProvider is a fake llm.ToolCallingProvider that replays a queued
// list of stream-event slices, one per turn.
//
// Each turn is a slice of llm.StreamEvent. The provider also accepts a
// fallback list of buffered ChatResponse for the SupportsStreaming=false
// path so the same fixture can drive both code paths.
type streamingProvider struct {
	turns        [][]llm.StreamEvent
	buffered     []llm.ChatResponse
	streamingOn  bool
	streamErr    error
	bufferedErr  error
	calls        int
	bufCalls     int
	gotStreamMsg [][]llm.Message
}

func (s *streamingProvider) Name() string { return "streaming" }
func (s *streamingProvider) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOpts) (string, error) {
	return "", errors.New("Chat not used")
}
func (s *streamingProvider) SupportsTools(_ context.Context) bool { return true }
func (s *streamingProvider) SupportsStreaming() bool              { return s.streamingOn }

func (s *streamingProvider) ChatWithTools(_ context.Context, _ []llm.Message, _ []llm.Tool, _ llm.ChatOpts) (llm.ChatResponse, error) {
	if s.bufferedErr != nil {
		return llm.ChatResponse{}, s.bufferedErr
	}
	if s.bufCalls >= len(s.buffered) {
		return llm.ChatResponse{}, fmt.Errorf("streamingProvider: ran out of buffered responses (call %d)", s.bufCalls+1)
	}
	resp := s.buffered[s.bufCalls]
	s.bufCalls++
	return resp, nil
}

func (s *streamingProvider) ChatWithToolsStream(_ context.Context, msgs []llm.Message, _ []llm.Tool, _ llm.ChatOpts) (<-chan llm.StreamEvent, error) {
	if s.streamErr != nil {
		return nil, s.streamErr
	}
	if s.calls >= len(s.turns) {
		return nil, fmt.Errorf("streamingProvider: ran out of turns (call %d)", s.calls+1)
	}
	s.gotStreamMsg = append(s.gotStreamMsg, append([]llm.Message(nil), msgs...))
	turn := s.turns[s.calls]
	s.calls++
	ch := make(chan llm.StreamEvent, len(turn)+1)
	for _, ev := range turn {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// drain collects events from a RunStream channel until it closes.
func drain(ch <-chan AgentEvent) []AgentEvent {
	var out []AgentEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func eventTypes(events []AgentEvent) []AgentEventType {
	out := make([]AgentEventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// ─── streaming happy path ─────────────────────────────────────────────────────

func TestRunStream_StreamingHappyPath(t *testing.T) {
	reg := newRegistryWithFixture(t)

	// Turn 1: model streams a tool call to current_time.
	// Turn 2: model streams text tokens "It is " "noon." with no tool calls.
	turn1 := []llm.StreamEvent{
		{Type: llm.StreamEventToolCallStart, ToolCall: &llm.ToolCall{ID: "c1", Name: "current_time"}},
		{Type: llm.StreamEventToolCallDelta, ToolCall: &llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`{}`)}},
		{Type: llm.StreamEventToolCallEnd, ToolCall: &llm.ToolCall{ID: "c1"}},
		{Type: llm.StreamEventDone},
	}
	turn2 := []llm.StreamEvent{
		{Type: llm.StreamEventToken, Token: "It is "},
		{Type: llm.StreamEventToken, Token: "noon."},
		{Type: llm.StreamEventDone},
	}
	prov := &streamingProvider{
		turns:       [][]llm.StreamEvent{turn1, turn2},
		streamingOn: true,
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	events := drain(loop.RunStream(context.Background(), []llm.Message{
		{Role: "user", Content: "what time is it?"},
	}))

	// Verify event sequence shape:
	// thinking, tool_call, tool_result, thinking, token, token, done
	want := []AgentEventType{
		AgentEventThinking,
		AgentEventToolCall,
		AgentEventToolResult,
		AgentEventThinking,
		AgentEventToken,
		AgentEventToken,
		AgentEventDone,
	}
	got := eventTypes(events)
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %s, want %s", i, got[i], want[i])
		}
	}

	// Final Done event should carry the assembled assistant text.
	last := events[len(events)-1]
	if last.Content != "It is noon." {
		t.Errorf("done.Content = %q, want %q", last.Content, "It is noon.")
	}

	// Tool call event should carry name and args.
	tc := events[1]
	if tc.ToolName != "current_time" {
		t.Errorf("tool_call name = %q", tc.ToolName)
	}
	if string(tc.ToolArgs) != "{}" {
		t.Errorf("tool_call args = %s", string(tc.ToolArgs))
	}

	// Tool result must include duration and result body.
	tr := events[2]
	if tr.DurationMs < 0 {
		t.Errorf("duration_ms = %d", tr.DurationMs)
	}
	if len(tr.ToolResult) == 0 || !strings.Contains(string(tr.ToolResult), "now") {
		t.Errorf("tool_result body unexpected: %s", string(tr.ToolResult))
	}

	// The second turn should have seen the tool result fed back as a tool message.
	if len(prov.gotStreamMsg) != 2 {
		t.Fatalf("provider stream calls = %d, want 2", len(prov.gotStreamMsg))
	}
	second := prov.gotStreamMsg[1]
	last2 := second[len(second)-1]
	if last2.Role != "tool" || last2.ToolCallID != "c1" {
		t.Errorf("expected tool result for c1, got %+v", last2)
	}
}

// ─── Anthropic-style streaming: Start with `{}` placeholder + delta chunks ───

// TestRunStream_AnthropicStylePartialJSON regression-tests the bug where
// Anthropic emits a tool_call_start with Arguments=`{}` (placeholder),
// followed by delta chunks carrying the real JSON, then a tool_call_end
// with the assembled args. The streaming finalizer used to concatenate
// the placeholder with the deltas, producing `{}{"start":"…"}` which
// failed json.RawMessage marshalling downstream ("invalid character '{'
// after top-level value").
func TestRunStream_AnthropicStylePartialJSON(t *testing.T) {
	reg := newRegistryWithFixture(t)

	turn1 := []llm.StreamEvent{
		// Start carries the `{}` placeholder Anthropic always sends.
		{Type: llm.StreamEventToolCallStart, ToolCall: &llm.ToolCall{
			ID: "c1", Name: "list_activities", Arguments: json.RawMessage(`{}`),
		}},
		// Deltas stream the real JSON character-by-character (chunked here).
		{Type: llm.StreamEventToolCallDelta, ToolCall: &llm.ToolCall{
			ID: "c1", Arguments: json.RawMessage(`{"limit":`),
		}},
		{Type: llm.StreamEventToolCallDelta, ToolCall: &llm.ToolCall{
			ID: "c1", Arguments: json.RawMessage(`2}`),
		}},
		// End carries the fully-assembled args.
		{Type: llm.StreamEventToolCallEnd, ToolCall: &llm.ToolCall{
			ID: "c1", Arguments: json.RawMessage(`{"limit":2}`),
		}},
		{Type: llm.StreamEventDone},
	}
	turn2 := []llm.StreamEvent{
		{Type: llm.StreamEventToken, Token: "ok"},
		{Type: llm.StreamEventDone},
	}
	prov := &streamingProvider{
		turns:       [][]llm.StreamEvent{turn1, turn2},
		streamingOn: true,
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	events := drain(loop.RunStream(context.Background(), []llm.Message{
		{Role: "user", Content: "show me 2"},
	}))

	// Find the tool_call event and verify its args round-trip cleanly.
	var tc *AgentEvent
	for i := range events {
		if events[i].Type == AgentEventToolCall {
			tc = &events[i]
			break
		}
	}
	if tc == nil {
		t.Fatalf("no tool_call event in stream")
	}
	got := strings.TrimSpace(string(tc.ToolArgs))
	if got != `{"limit":2}` {
		t.Errorf("tool_call args = %q, want {\"limit\":2}", got)
	}

	// And the tool_result must have actually fired (not been blocked by
	// a JSON-parse error in the registry).
	var tr *AgentEvent
	for i := range events {
		if events[i].Type == AgentEventToolResult {
			tr = &events[i]
			break
		}
	}
	if tr == nil {
		t.Fatalf("no tool_result event — args were probably mis-parsed")
	}
	if tr.ToolError != "" {
		t.Errorf("tool_result error = %q (args were corrupted)", tr.ToolError)
	}

	// Final round-trip MarshalJSON should not error — that was the user-
	// reported symptom downstream.
	if _, err := json.Marshal(tc.ToolArgs); err != nil {
		t.Errorf("tool_call args MarshalJSON: %v", err)
	}
}

// ─── non-streaming fallback ───────────────────────────────────────────────────

func TestRunStream_NonStreamingFallback(t *testing.T) {
	reg := newRegistryWithFixture(t)

	prov := &streamingProvider{
		streamingOn: false,
		buffered: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
			}},
			{Content: "Final answer."},
		},
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	events := drain(loop.RunStream(context.Background(), []llm.Message{
		{Role: "user", Content: "go"},
	}))

	got := eventTypes(events)
	// thinking, (no token on tool-only turn), tool_call, tool_result,
	// thinking, token (synthesised), done.
	want := []AgentEventType{
		AgentEventThinking,
		AgentEventToolCall,
		AgentEventToolResult,
		AgentEventThinking,
		AgentEventToken,
		AgentEventDone,
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if events[len(events)-1].Content != "Final answer." {
		t.Errorf("done.Content = %q", events[len(events)-1].Content)
	}
}

// ─── tool error surfaced in tool_result event ─────────────────────────────────

func TestRunStream_ToolError(t *testing.T) {
	reg := newRegistryWithFixture(t)

	turn1 := []llm.StreamEvent{
		{Type: llm.StreamEventToolCallStart, ToolCall: &llm.ToolCall{ID: "c1", Name: "get_activity"}},
		{Type: llm.StreamEventToolCallDelta, ToolCall: &llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`{}`)}},
		{Type: llm.StreamEventToolCallEnd, ToolCall: &llm.ToolCall{ID: "c1"}},
		{Type: llm.StreamEventDone},
	}
	turn2 := []llm.StreamEvent{
		{Type: llm.StreamEventToken, Token: "Sorry."},
		{Type: llm.StreamEventDone},
	}
	prov := &streamingProvider{
		turns:       [][]llm.StreamEvent{turn1, turn2},
		streamingOn: true,
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	events := drain(loop.RunStream(context.Background(), []llm.Message{
		{Role: "user", Content: "fetch one"},
	}))

	var sawErr bool
	for _, e := range events {
		if e.Type == AgentEventToolResult && e.ToolError != "" {
			sawErr = true
			break
		}
	}
	if !sawErr {
		t.Fatalf("expected tool_result event with ToolError set: %+v", events)
	}
	if events[len(events)-1].Type != AgentEventDone {
		t.Errorf("expected terminal Done, got %s", events[len(events)-1].Type)
	}
}

// ─── provider error → terminal AgentEventError ────────────────────────────────

func TestRunStream_ProviderError(t *testing.T) {
	reg := newRegistryWithFixture(t)
	prov := &streamingProvider{
		streamingOn: true,
		streamErr:   errors.New("upstream blew up"),
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	events := drain(loop.RunStream(context.Background(), []llm.Message{
		{Role: "user", Content: "hi"},
	}))

	last := events[len(events)-1]
	if last.Type != AgentEventError {
		t.Fatalf("expected terminal Error, got %s", last.Type)
	}
	if !strings.Contains(last.Err, "upstream blew up") {
		t.Errorf("error message = %q", last.Err)
	}
}

// ─── max steps cap → terminal Error with ErrMaxStepsExceeded message ──────────

func TestRunStream_MaxSteps(t *testing.T) {
	reg := newRegistryWithFixture(t)

	loopTurn := []llm.StreamEvent{
		{Type: llm.StreamEventToolCallStart, ToolCall: &llm.ToolCall{ID: "c1", Name: "current_time"}},
		{Type: llm.StreamEventToolCallDelta, ToolCall: &llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`{}`)}},
		{Type: llm.StreamEventToolCallEnd, ToolCall: &llm.ToolCall{ID: "c1"}},
		{Type: llm.StreamEventDone},
	}
	prov := &streamingProvider{
		turns:       [][]llm.StreamEvent{loopTurn, loopTurn, loopTurn},
		streamingOn: true,
	}

	loop := NewLoop(prov, reg, LoopOptions{MaxSteps: 3})
	events := drain(loop.RunStream(context.Background(), []llm.Message{
		{Role: "user", Content: "loop"},
	}))

	last := events[len(events)-1]
	if last.Type != AgentEventError {
		t.Fatalf("expected terminal Error, got %s", last.Type)
	}
	if !strings.Contains(last.Err, ErrMaxStepsExceeded.Error()) {
		t.Errorf("error message = %q", last.Err)
	}
}
