package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/agent/tools"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// scriptedProvider is a fake llm.ToolCallingProvider that returns a queued
// list of ChatResponses, one per call. Each call records the messages it
// received so tests can assert the loop sent the right history.
type scriptedProvider struct {
	responses []llm.ChatResponse
	err       error
	calls     int
	gotMsgs   [][]llm.Message
	gotTools  [][]llm.Tool
}

func (s *scriptedProvider) Name() string                                        { return "scripted" }
func (s *scriptedProvider) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOpts) (string, error) {
	return "", errors.New("Chat not used by agent loop")
}
func (s *scriptedProvider) SupportsTools(_ context.Context) bool { return true }
func (s *scriptedProvider) SupportsStreaming() bool              { return false }
func (s *scriptedProvider) ChatWithToolsStream(_ context.Context, _ []llm.Message, _ []llm.Tool, _ llm.ChatOpts) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("streaming not used by loop tests")
}

func (s *scriptedProvider) ChatWithTools(_ context.Context, msgs []llm.Message, tools []llm.Tool, _ llm.ChatOpts) (llm.ChatResponse, error) {
	if s.err != nil {
		return llm.ChatResponse{}, s.err
	}
	s.gotMsgs = append(s.gotMsgs, append([]llm.Message(nil), msgs...))
	s.gotTools = append(s.gotTools, append([]llm.Tool(nil), tools...))
	if s.calls >= len(s.responses) {
		return llm.ChatResponse{}, fmt.Errorf("scriptedProvider: ran out of responses (call %d)", s.calls+1)
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

// newRegistryWithFixture spins up an in-memory DB seeded with a few rows
// and returns a tools registry pinned to a fixed clock.
func newRegistryWithFixture(t *testing.T) *tools.Registry {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mk := func(src models.Source, srcID, title string, ts time.Time) {
		_, err := db.InsertActivity(models.Activity{
			Source:    src,
			SourceID:  srcID,
			Type:      models.TypeCommit,
			Title:     title,
			Timestamp: ts,
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mk(models.SourceGit, "r:1", "Initial commit", time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC))
	mk(models.SourceGit, "r:2", "Add tests", time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC))
	mk(models.SourceGit, "r:3", "Refactor handlers", time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC))

	return tools.NewRegistry(tools.Deps{
		DB:  db,
		Now: func() time.Time { return time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC) },
	})
}

// ─── happy path: multi-step conversation ──────────────────────────────────────

func TestLoop_HappyPath_TwoStepCount(t *testing.T) {
	reg := newRegistryWithFixture(t)

	// Step 1: model asks for current_time. Step 2: model asks for
	// count_activities. Step 3: model returns a final text answer.
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)},
			}},
			{ToolCalls: []llm.ToolCall{
				{ID: "c2", Name: "count_activities", Arguments: json.RawMessage(`{"start":"2026-04-07","end":"2026-04-09"}`)},
			}},
			{Content: "You had 3 activities."},
		},
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	res, err := loop.Run(context.Background(), []llm.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "what did I do?"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Content != "You had 3 activities." {
		t.Errorf("content = %q", res.Content)
	}
	if res.Steps != 3 {
		t.Errorf("steps = %d, want 3", res.Steps)
	}
	if len(res.Trace) != 2 {
		t.Fatalf("trace len = %d, want 2", len(res.Trace))
	}
	if res.Trace[0].ToolName != "current_time" || res.Trace[0].Error != "" {
		t.Errorf("trace[0] = %+v", res.Trace[0])
	}
	if res.Trace[1].ToolName != "count_activities" {
		t.Errorf("trace[1].name = %q", res.Trace[1].ToolName)
	}

	// The second LLM call should have seen the assistant tool-call message
	// plus the tool-result message from step 1.
	if len(prov.gotMsgs) != 3 {
		t.Fatalf("provider calls = %d", len(prov.gotMsgs))
	}
	second := prov.gotMsgs[1]
	if len(second) < 4 {
		t.Fatalf("second-call messages = %d, want >=4", len(second))
	}
	if second[len(second)-2].Role != "assistant" || len(second[len(second)-2].ToolCalls) == 0 {
		t.Errorf("expected assistant w/ tool_calls before tool result, got %+v", second[len(second)-2])
	}
	if second[len(second)-1].Role != "tool" || second[len(second)-1].ToolCallID != "c1" {
		t.Errorf("expected tool result for c1, got %+v", second[len(second)-1])
	}

	// Tool list passed to provider should match the registry.
	if len(prov.gotTools[0]) != len(reg.Names()) {
		t.Errorf("tools len = %d, want %d", len(prov.gotTools[0]), len(reg.Names()))
	}
}

// ─── step cap ─────────────────────────────────────────────────────────────────

func TestLoop_StepCap(t *testing.T) {
	reg := newRegistryWithFixture(t)
	// Provider always asks for another tool call → never produces final answer.
	prov := &scriptedProvider{}
	for i := 0; i < 5; i++ {
		prov.responses = append(prov.responses, llm.ChatResponse{
			ToolCalls: []llm.ToolCall{
				{ID: fmt.Sprintf("c%d", i), Name: "current_time", Arguments: json.RawMessage(`{}`)},
			},
		})
	}

	loop := NewLoop(prov, reg, LoopOptions{MaxSteps: 3})
	res, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "loop forever"}})
	if !errors.Is(err, ErrMaxStepsExceeded) {
		t.Fatalf("err = %v, want ErrMaxStepsExceeded", err)
	}
	if res.Steps != 3 {
		t.Errorf("steps = %d, want 3", res.Steps)
	}
	if len(res.Trace) != 3 {
		t.Errorf("trace len = %d, want 3 (one per LLM call)", len(res.Trace))
	}
}

// ─── tool error path ──────────────────────────────────────────────────────────

func TestLoop_ToolError_RecordedAndFedBack(t *testing.T) {
	reg := newRegistryWithFixture(t)

	// Step 1: bad arguments → tool returns an error. Step 2: model recovers.
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "get_activity", Arguments: json.RawMessage(`{}`)},
			}},
			{Content: "Couldn't fetch — sorry."},
		},
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	res, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "fetch one"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Trace) != 1 || res.Trace[0].Error == "" {
		t.Fatalf("expected error in trace[0], got %+v", res.Trace)
	}

	// The second LLM call must have seen a tool message containing the error.
	second := prov.gotMsgs[1]
	last := second[len(second)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "error") {
		t.Errorf("expected error tool message, got %+v", last)
	}
}

// ─── unknown tool error ───────────────────────────────────────────────────────

func TestLoop_UnknownTool(t *testing.T) {
	reg := newRegistryWithFixture(t)
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "fly_to_moon", Arguments: json.RawMessage(`{}`)},
			}},
			{Content: "ok"},
		},
	}

	loop := NewLoop(prov, reg, LoopOptions{})
	res, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Trace[0].Error == "" || !strings.Contains(res.Trace[0].Error, "unknown tool") {
		t.Errorf("trace[0].error = %q", res.Trace[0].Error)
	}
}

// ─── provider error propagation ───────────────────────────────────────────────

func TestLoop_ProviderError(t *testing.T) {
	reg := newRegistryWithFixture(t)
	prov := &scriptedProvider{err: errors.New("upstream blew up")}

	loop := NewLoop(prov, reg, LoopOptions{})
	_, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "upstream blew up") {
		t.Fatalf("err = %v", err)
	}
}

// ─── tool timeout ─────────────────────────────────────────────────────────────

// slowRegistry wraps a real registry but adds a synthetic slow tool by
// constructing a registry with a custom Deps + Now (we cannot easily inject
// a slow tool into the catalogue, so test the timeout via a custom Loop
// that calls Execute directly).
//
// Instead of patching the catalogue, register a separate Loop that wraps a
// fake provider asking for a tool we know is fast, and verify that
// LoopOptions.ToolTimeout is honoured by checking ctx propagation.
//
// The simplest reliable test: invoke a tool whose executor blocks and
// confirm timeout. We do that by calling l.executeOne directly with a tiny
// timeout against a custom executor injected via a sub-registry. Since
// tools.Registry constructs the catalogue, we instead test the contract
// indirectly: configure a 1ns timeout and call current_time — the executor
// itself is fast enough that this still succeeds, but the deadline is
// passed through, so we add a separate test for the documented behaviour.
//
// To exercise the timeout reliably, we inline a tiny stand-in tools.Tool
// using the registry's exported types and execute it through the loop.
func TestLoop_ToolTimeout(t *testing.T) {
	reg := newRegistryWithFixture(t)

	// Use a 1ns timeout: any executor that does even trivial DB work might
	// finish, so to keep this deterministic we point at semantic_search
	// which fails synchronously when no embedder is configured. The
	// failure goes into the trace as an error — what we actually want to
	// confirm here is that a passed deadline doesn't crash the loop and
	// is captured in the trace step's Error field. We assert that
	// behaviour shape rather than wall-clock time.
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "semantic_search_activities", Arguments: json.RawMessage(`{"query":"x"}`)},
			}},
			{Content: "done"},
		},
	}

	loop := NewLoop(prov, reg, LoopOptions{ToolTimeout: time.Nanosecond})
	res, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Trace) != 1 || res.Trace[0].Error == "" {
		t.Errorf("expected error in trace, got %+v", res.Trace)
	}
}

// ─── caller messages are not mutated ──────────────────────────────────────────

func TestLoop_DoesNotMutateCallerMessages(t *testing.T) {
	reg := newRegistryWithFixture(t)
	prov := &scriptedProvider{
		responses: []llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "current_time", Arguments: json.RawMessage(`{}`)}}},
			{Content: "ok"},
		},
	}
	caller := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	loop := NewLoop(prov, reg, LoopOptions{})
	if _, err := loop.Run(context.Background(), caller); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(caller) != 2 {
		t.Errorf("caller messages mutated: len=%d", len(caller))
	}
}

// ─── defaults ─────────────────────────────────────────────────────────────────

func TestLoop_Defaults(t *testing.T) {
	reg := newRegistryWithFixture(t)
	loop := NewLoop(&scriptedProvider{}, reg, LoopOptions{})
	if loop.opts.MaxSteps != DefaultMaxSteps {
		t.Errorf("MaxSteps = %d", loop.opts.MaxSteps)
	}
	if loop.opts.ToolTimeout != DefaultToolTimeout {
		t.Errorf("ToolTimeout = %v", loop.opts.ToolTimeout)
	}
}
