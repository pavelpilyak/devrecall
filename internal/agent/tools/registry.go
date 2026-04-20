// Package tools implements the read-only tool catalogue exposed to the
// chat agent. Each tool is a thin wrapper over internal/storage (and, for
// semantic search, internal/embedding) — no network calls, no writes.
//
// The catalogue is intentionally small. New tools should only be added when
// a real query cannot be answered with the existing set; see
// docs/chat-agent-rewrite.md for the design rationale.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/pavelpilyak/devrecall/internal/embedding"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
)

// Deps bundles the read-only dependencies tools need to execute. Embedder
// may be nil; semantic_search_activities will return an error in that case.
// Now lets tests pin a deterministic clock; if nil, time.Now is used.
type Deps struct {
	DB       *storage.DB
	Embedder embedding.Embedder
	Now      func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Executor runs a tool against a JSON-encoded argument blob. The result
// must be JSON-encodable so the agent loop can feed it back to the model.
type Executor func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)

// Tool is one entry in the registry: a name, a JSON Schema describing its
// arguments, a human-readable description for the model, and an executor.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Execute     Executor
}

// Registry holds the available tools by name. It preserves registration
// order so the model sees a stable tool list across calls.
type Registry struct {
	deps  Deps
	tools map[string]Tool
	order []string
}

// NewRegistry returns a registry pre-populated with the v1 tool catalogue.
// Pass non-nil DB. Embedder is optional.
func NewRegistry(deps Deps) *Registry {
	r := &Registry{
		deps:  deps,
		tools: map[string]Tool{},
	}
	for _, t := range buildCatalogue(deps) {
		r.register(t)
	}
	return r
}

func (r *Registry) register(t Tool) {
	if _, exists := r.tools[t.Name]; exists {
		panic(fmt.Sprintf("tools: duplicate registration for %q", t.Name))
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
}

// Tool returns the named tool, or false if missing.
func (r *Registry) Tool(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// All returns the tools in registration order.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// Names returns the registered tool names in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Execute runs the named tool with the given arguments. Returns an error if
// the tool is unknown or its executor fails.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	// Log args verbatim so we can spot mid-stream JSON corruption
	// (e.g. `{}{"key":"v"}` from broken Anthropic delta concatenation)
	// at the boundary between provider and tool execution.
	if !json.Valid(args) {
		log.Printf("tools: Execute received invalid JSON args tool=%q raw=%q", name, string(args))
	}
	return t.Execute(ctx, args)
}

// LLMTools returns the catalogue formatted for the llm.ToolCallingProvider
// interface. The agent loop uses this to advertise tools to the model.
func (r *Registry) LLMTools() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		out = append(out, llm.Tool{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.Schema,
		})
	}
	return out
}
