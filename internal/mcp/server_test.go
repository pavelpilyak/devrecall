package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/agent/tools"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// testRig wires a Server against an in-memory storage backend. Each test
// gets its own DB, server, and serving goroutine, all torn down via t.Cleanup.
type testRig struct {
	t        *testing.T
	server   *Server
	registry *tools.Registry
	db       *storage.DB
	// pipes simulate stdin/stdout from the server's perspective.
	// clientWrite → server stdin; server stdout → clientRead.
	clientWrite io.WriteCloser
	clientRead  *bufio.Reader
	done        chan struct{}
}

// newRig creates a server with an empty DB. Pass a seed func to insert
// fixtures before the goroutine starts handling requests.
func newRig(t *testing.T, seed func(*storage.DB)) *testRig {
	t.Helper()

	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if seed != nil {
		seed(db)
	}

	registry := tools.NewRegistry(tools.Deps{
		DB:  db,
		Now: func() time.Time { return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC) },
	})

	serverIn, clientWrite := io.Pipe()
	clientRead, serverOut := io.Pipe()

	server := NewServer(registry, "test-version", serverIn, serverOut)

	rig := &testRig{
		t:           t,
		server:      server,
		registry:    registry,
		db:          db,
		clientWrite: clientWrite,
		clientRead:  bufio.NewReader(clientRead),
		done:        make(chan struct{}),
	}

	go func() {
		_ = server.Serve(context.Background())
		// Close server's stdout when the loop exits so any pending client
		// read unblocks instead of hanging the test.
		_ = serverOut.Close()
		close(rig.done)
	}()

	t.Cleanup(func() {
		_ = clientWrite.Close()
		select {
		case <-rig.done:
		case <-time.After(2 * time.Second):
			t.Error("server didn't exit within 2s of stdin close")
		}
	})

	return rig
}

// send writes one newline-framed JSON-RPC message; for notifications, no
// response is expected so callers don't call recv afterwards.
func (r *testRig) send(line string) {
	r.t.Helper()
	if _, err := io.WriteString(r.clientWrite, line+"\n"); err != nil {
		r.t.Fatalf("write: %v", err)
	}
}

// recv reads one response line and decodes it into a generic Response.
func (r *testRig) recv() Response {
	r.t.Helper()
	line, err := r.clientRead.ReadBytes('\n')
	if err != nil {
		r.t.Fatalf("read: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		r.t.Fatalf("decode response %q: %v", string(line), err)
	}
	return resp
}

func TestInitialize_ReturnsServerInfoAndCapabilities(t *testing.T) {
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)

	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp.Error)
	}
	var got InitializeResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if got.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", got.ProtocolVersion, ProtocolVersion)
	}
	if got.ServerInfo.Name != "devrecall" {
		t.Errorf("serverInfo.name = %q, want devrecall", got.ServerInfo.Name)
	}
	if got.ServerInfo.Version != "test-version" {
		t.Errorf("serverInfo.version = %q, want test-version", got.ServerInfo.Version)
	}
	if got.Capabilities.Tools == nil {
		t.Errorf("tools capability missing")
	}
}

func TestInitialize_AcceptsEmptyParams(t *testing.T) {
	// Some clients send `initialize` without params; the server should
	// accept it rather than reject with "invalid params".
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("expected ok, got error %+v", resp.Error)
	}
}

func TestToolsList_AdvertisesAllRegistryTools(t *testing.T) {
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("tools/list errored: %+v", resp.Error)
	}
	var got ListToolsResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantNames := r.registry.Names()
	if len(got.Tools) != len(wantNames) {
		t.Fatalf("got %d tools, want %d (%v)", len(got.Tools), len(wantNames), wantNames)
	}
	for i, name := range wantNames {
		if got.Tools[i].Name != name {
			t.Errorf("[%d] name = %q, want %q", i, got.Tools[i].Name, name)
		}
		if len(got.Tools[i].InputSchema) == 0 {
			t.Errorf("[%d] %s missing inputSchema", i, name)
		}
	}
}

func TestToolsCall_CurrentTime(t *testing.T) {
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"current_time","arguments":{}}}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("tools/call errored: %+v", resp.Error)
	}
	var got CallToolResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.IsError {
		t.Fatalf("isError true: %+v", got.Content)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "text" {
		t.Fatalf("expected one text content block, got %+v", got.Content)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.Content[0].Text), &payload); err != nil {
		t.Fatalf("tool output not JSON: %v\n%s", err, got.Content[0].Text)
	}
}

func TestToolsCall_ListActivities_EmptyDB(t *testing.T) {
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_activities","arguments":{}}}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("list_activities errored: %+v", resp.Error)
	}
	var got CallToolResult
	json.Unmarshal(resp.Result, &got)
	if got.IsError {
		t.Fatalf("isError: %+v", got.Content)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(got.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Count != 0 {
		t.Errorf("expected empty DB → count=0, got %d", payload.Count)
	}
}

func TestToolsCall_GetActivity_ReturnsSeededRow(t *testing.T) {
	r := newRig(t, func(db *storage.DB) {
		if _, err := db.InsertActivity(models.Activity{
			Source:    models.SourceGit,
			SourceID:  "test:abc",
			Type:      models.TypeCommit,
			Title:     "Test commit",
			Timestamp: time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	})

	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_activity","arguments":{"id":1}}}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("rpc error: %+v", resp.Error)
	}
	var got CallToolResult
	json.Unmarshal(resp.Result, &got)
	if got.IsError {
		t.Fatalf("isError: %+v", got.Content)
	}
	if !strings.Contains(got.Content[0].Text, "Test commit") {
		t.Errorf("expected title in body, got %q", got.Content[0].Text)
	}
}

func TestToolsCall_ToolErrorBecomesIsError(t *testing.T) {
	// get_activity with id=0 → tool returns an error. The MCP layer must
	// surface it as result.isError=true, not as a JSON-RPC error.
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_activity","arguments":{"id":0}}}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("expected RPC ok with isError result, got RPC error %+v", resp.Error)
	}
	var got CallToolResult
	json.Unmarshal(resp.Result, &got)
	if !got.IsError {
		t.Errorf("expected isError=true for tool error")
	}
	if !strings.Contains(got.Content[0].Text, "id") {
		t.Errorf("error text should mention id; got %q", got.Content[0].Text)
	}
}

func TestToolsCall_UnknownToolBecomesIsError(t *testing.T) {
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bogus","arguments":{}}}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("expected RPC ok, got %+v", resp.Error)
	}
	var got CallToolResult
	json.Unmarshal(resp.Result, &got)
	if !got.IsError {
		t.Errorf("unknown tool should produce isError=true")
	}
}

func TestPing_ReturnsEmptyObject(t *testing.T) {
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("ping errored: %+v", resp.Error)
	}
	if string(resp.Result) != `{}` {
		t.Errorf("ping result = %s, want {}", string(resp.Result))
	}
}

func TestNotification_GetsNoResponse(t *testing.T) {
	r := newRig(t, nil)
	// notifications/initialized has no id. Server must not reply. Send a
	// notification followed by a real request and confirm we only get one
	// response with id=42.
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	r.send(`{"jsonrpc":"2.0","id":42,"method":"ping"}`)
	resp := r.recv()
	if string(resp.ID) != "42" {
		t.Errorf("expected response to id=42, got id=%s", string(resp.ID))
	}
}

func TestUnknownMethod_ReturnsMethodNotFound(t *testing.T) {
	r := newRig(t, nil)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"bogus/method"}`)
	resp := r.recv()
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != ErrMethodNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrMethodNotFound)
	}
}

func TestResourcesAndPromptsList_EmptyResponses(t *testing.T) {
	r := newRig(t, nil)

	r.send(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	resp := r.recv()
	if resp.Error != nil {
		t.Fatalf("resources/list errored: %+v", resp.Error)
	}
	if !strings.Contains(string(resp.Result), `"resources":[]`) {
		t.Errorf("resources/list = %s, want empty list", string(resp.Result))
	}

	r.send(`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`)
	resp = r.recv()
	if resp.Error != nil {
		t.Fatalf("prompts/list errored: %+v", resp.Error)
	}
	if !strings.Contains(string(resp.Result), `"prompts":[]`) {
		t.Errorf("prompts/list = %s, want empty list", string(resp.Result))
	}
}
