package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/pavelpilyak/devrecall/internal/agent/tools"
	"github.com/pavelpilyak/devrecall/internal/storage"
)

// Server runs the MCP stdio loop. It reads newline-delimited JSON-RPC
// requests from a reader and writes responses to a writer. Tool calls are
// dispatched against the supplied tools.Registry.
//
// One server per process. Concurrent writes to the same writer would corrupt
// the wire — we serialize via an internal mutex.
type Server struct {
	registry *tools.Registry
	db       *storage.DB // used by resource handlers; tools go through registry
	info     ServerInfo

	in  *bufio.Reader
	out io.Writer
	mu  sync.Mutex // serializes writes to out
}

// NewServer wires a server against a tool registry, the storage handle (for
// resource handlers), the binary version, and an io pair. Callers usually
// pass os.Stdin and os.Stdout.
func NewServer(registry *tools.Registry, db *storage.DB, version string, in io.Reader, out io.Writer) *Server {
	return &Server{
		registry: registry,
		db:       db,
		info:     ServerInfo{Name: "devrecall", Version: version},
		in:       bufio.NewReader(in),
		out:      out,
	}
}

// Serve runs the message loop until the reader returns io.EOF or ctx is done.
// Errors writing to stdout are fatal (the client has gone away); errors from
// individual handlers become JSON-RPC errors on the wire and the loop keeps
// running.
func (s *Server) Serve(ctx context.Context) error {
	// Larger-than-default buffer for activity content that can run to a few
	// KB per row when get_activity returns full bodies.
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		s.handleLine(ctx, line)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("mcp scanner: %w", err)
	}
	return nil
}

// handleLine parses one request line, dispatches it, and (for non-
// notifications) writes a response. Logging goes to stderr so it never
// pollutes the protocol stream.
func (s *Server) handleLine(ctx context.Context, line []byte) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		log.Printf("mcp: parse error: %v line=%q", err, string(line))
		s.writeError(nil, ErrParse, "parse error")
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeError(req.ID, ErrInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	// Notifications (no id) get processed but no response — per JSON-RPC 2.0
	// spec, the absence of an id field means the client doesn't want one.
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	result, rpcErr := s.dispatch(ctx, req)

	if isNotification {
		if rpcErr != nil {
			log.Printf("mcp: notification %q failed: %s", req.Method, rpcErr.Message)
		}
		return
	}
	if rpcErr != nil {
		s.writeError(req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	s.writeResult(req.ID, result)
}

// dispatch routes a request to the handler for its method. Returns the
// result body (will be wrapped as JSON-RPC result) or an RPCError.
func (s *Server) dispatch(ctx context.Context, req Request) (json.RawMessage, *RPCError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params)
	case "initialized", "notifications/initialized":
		// Client's "I'm ready" handshake. No response.
		return nil, nil
	case "ping":
		// Liveness check from clients. Return an empty object.
		return json.RawMessage(`{}`), nil
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params)
	case "prompts/list":
		b, _ := json.Marshal(listPrompts())
		return b, nil
	case "prompts/get":
		return s.handlePromptsGet(req.Params)
	case "resources/list":
		// We expose templated resources only — no concrete URIs to enumerate.
		return json.RawMessage(`{"resources":[]}`), nil
	case "resources/templates/list":
		b, _ := json.Marshal(ListResourceTemplatesResult{ResourceTemplates: resourceTemplates})
		return b, nil
	case "resources/read":
		return s.handleResourcesRead(req.Params)
	default:
		return nil, &RPCError{Code: ErrMethodNotFound, Message: "method not found: " + req.Method}
	}
}

func (s *Server) handleInitialize(params json.RawMessage) (json.RawMessage, *RPCError) {
	// We accept the client's params verbatim — we don't act on capabilities
	// yet — but a malformed payload should still surface as invalid params.
	if len(params) > 0 {
		var p InitializeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: "initialize: " + err.Error()}
		}
	}
	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools:     &ToolsCapability{},
			Prompts:   &PromptsCapability{},
			Resources: &ResourcesCapability{},
		},
		ServerInfo: s.info,
	}
	b, _ := json.Marshal(result)
	return b, nil
}

func (s *Server) handleToolsList() (json.RawMessage, *RPCError) {
	all := s.registry.All()
	descriptors := make([]ToolDescriptor, 0, len(all))
	for _, t := range all {
		descriptors = append(descriptors, ToolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Schema,
		})
	}
	b, _ := json.Marshal(ListToolsResult{Tools: descriptors})
	return b, nil
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var p CallToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "tools/call: " + err.Error()}
	}
	if p.Name == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "tools/call: name is required"}
	}

	out, err := s.registry.Execute(ctx, p.Name, p.Arguments)
	if err != nil {
		// MCP convention: tool execution errors land in `result.isError`,
		// not as JSON-RPC errors. Lets the model see the error message and
		// decide what to do next.
		result := CallToolResult{
			IsError: true,
			Content: []ContentBlock{{Type: "text", Text: err.Error()}},
		}
		b, _ := json.Marshal(result)
		return b, nil
	}

	result := CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: string(out)}},
	}
	b, _ := json.Marshal(result)
	return b, nil
}

func (s *Server) handlePromptsGet(params json.RawMessage) (json.RawMessage, *RPCError) {
	var p GetPromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "prompts/get: " + err.Error()}
	}
	if p.Name == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "prompts/get: name is required"}
	}
	result, rpcErr := getPrompt(p.Name, p.Arguments)
	if rpcErr != nil {
		return nil, rpcErr
	}
	b, _ := json.Marshal(result)
	return b, nil
}

func (s *Server) handleResourcesRead(params json.RawMessage) (json.RawMessage, *RPCError) {
	var p ReadResourceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "resources/read: " + err.Error()}
	}
	if p.URI == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "resources/read: uri is required"}
	}
	if s.db == nil {
		return nil, &RPCError{Code: ErrInternal, Message: "resources/read: server has no DB handle"}
	}
	result, rpcErr := readResource(p.URI, s.db)
	if rpcErr != nil {
		return nil, rpcErr
	}
	b, _ := json.Marshal(result)
	return b, nil
}

// --- wire writers ---

func (s *Server) writeResult(id, result json.RawMessage) {
	s.writeMessage(Response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, code int, message string) {
	if id == nil {
		id = json.RawMessage(`null`)
	}
	s.writeMessage(Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: message}})
}

func (s *Server) writeMessage(resp Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		log.Printf("mcp: marshal response: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		log.Printf("mcp: write to client: %v", err)
	}
}
