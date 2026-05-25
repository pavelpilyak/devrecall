// Package mcp implements an MCP (Model Context Protocol) server over stdio,
// exposing DevRecall's tool catalogue to MCP-compatible clients like Claude
// Code, Cursor, Codex, Continue, and Zed.
//
// Transport: newline-delimited JSON-RPC 2.0 on stdio. stdout is reserved for
// protocol messages — all logging goes to stderr.
//
// Wire types here mirror the subset of the MCP spec we implement
// (initialize, tools/list, tools/call, ping). They're hand-rolled instead of
// pulled from an SDK so we can track spec drift in one file.
package mcp

import "encoding/json"

// ProtocolVersion is the MCP spec revision we negotiate against on
// initialize. Clients that send a newer version still get this one back —
// MCP is designed to fall back to the server's supported version.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 envelope shared by every request and response.

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// JSON-RPC 2.0 error codes used by MCP.
const (
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
)

// --- initialize ---

type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities,omitempty"`
	ClientInfo      ServerInfo         `json:"clientInfo,omitempty"`
}

type ClientCapabilities struct {
	// We don't act on any client capabilities yet — accept them and move on.
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

// ServerCapabilities advertises which protocol surfaces we support. Only
// `tools` for now; resources and prompts arrive in Phase 2.
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

type ToolsCapability struct {
	// ListChanged would let the server push tool-list updates. Our tool set
	// is static for the process lifetime, so omit.
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// --- tools/list ---

type ListToolsResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

// ToolDescriptor is the public-facing tool definition advertised to clients.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// --- tools/call ---

type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is the MCP-defined response shape. For our tools, every
// result is a single text block carrying JSON — clients that want to inspect
// structure parse the text themselves.
type ContentBlock struct {
	Type string `json:"type"`           // "text" / "image" / "resource"
	Text string `json:"text,omitempty"` // populated when Type == "text"
}
