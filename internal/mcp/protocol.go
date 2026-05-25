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

// ServerCapabilities advertises which protocol surfaces we support.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
}

type ToolsCapability struct {
	// ListChanged would let the server push tool-list updates. Our tool set
	// is static for the process lifetime, so omit.
}

type PromptsCapability struct {
	// Same shape — we don't push prompt-list updates.
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
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

// --- prompts/list, prompts/get ---

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptDescriptor struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type ListPromptsResult struct {
	Prompts []PromptDescriptor `json:"prompts"`
}

type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

type PromptMessage struct {
	Role    string       `json:"role"` // "user" / "assistant" / "system"
	Content ContentBlock `json:"content"`
}

type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// --- resources/list, resources/templates/list, resources/read ---

type ResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ListResourcesResult struct {
	Resources []ResourceDescriptor `json:"resources"`
}

type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
}

type ReadResourceParams struct {
	URI string `json:"uri"`
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
}
