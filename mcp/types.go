package mcp

import "encoding/json"

// --- JSON-RPC 2.0 transport types ---

// Request is a JSON-RPC 2.0 request envelope carrying an MCP method call.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response envelope carrying a result or error.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object returned inside a Response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	// CodeParseError indicates the server received invalid JSON.
	CodeParseError = -32700
	// CodeInvalidRequest indicates the JSON is not a valid request object.
	CodeInvalidRequest = -32600
	// CodeMethodNotFound indicates the requested method does not exist.
	CodeMethodNotFound = -32601
	// CodeInvalidParams indicates the method parameters are invalid.
	CodeInvalidParams = -32602
	// CodeInternalError indicates an internal server error.
	CodeInternalError = -32603
)

// --- MCP protocol types ---

// InitializeParams holds the parameters sent by the client in the initialize handshake.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    ClientCaps `json:"capabilities"`
	ClientInfo      ClientInfo `json:"clientInfo"`
}

// ClientCaps describes the optional capabilities the MCP client supports.
type ClientCaps struct {
	Roots    *RootsCap    `json:"roots,omitempty"`
	Sampling *SamplingCap `json:"sampling,omitempty"`
}

// RootsCap describes the client's ability to provide filesystem roots.
type RootsCap struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// SamplingCap indicates the client supports server-initiated sampling requests.
type SamplingCap struct{}

// ClientInfo identifies the connecting MCP client by name and version.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the server's response to the initialize handshake.
type InitializeResult struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    ServerCaps `json:"capabilities"`
	ServerInfo      ServerInfo `json:"serverInfo"`
}

// ServerCaps describes the optional capabilities the MCP server supports.
type ServerCaps struct {
	Tools *ToolsCap `json:"tools,omitempty"`
}

// ToolsCap describes the server's tool-related capabilities.
type ToolsCap struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo identifies the MCP server by name and version.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// --- Tool types ---

// ToolDefinition describes a single tool exposed to clients via MCP tools/list.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolsListResult is the response payload for the tools/list method.
type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolCallParams holds the parameters for the tools/call method invocation.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolCallResult is the response payload for the tools/call method.
type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is a single content block (text, image, etc.) in a tool result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ClearanceTier defines the minimum clearance level required to invoke a tool.
type ClearanceTier int

const (
	// ClearancePublic allows any authenticated caller.
	ClearancePublic ClearanceTier = 0
	// ClearanceInternal restricts to internal service callers.
	ClearanceInternal ClearanceTier = 1
	// ClearanceExecute restricts to callers with execution privileges.
	ClearanceExecute ClearanceTier = 2
	// ClearanceAdmin restricts to tenant administrators.
	ClearanceAdmin ClearanceTier = 3
	// ClearanceSuperAdmin restricts to platform super-administrators.
	ClearanceSuperAdmin ClearanceTier = 4
)
