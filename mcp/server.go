package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/governance"
)

const (
	// ProtocolVersion is the MCP protocol version this server implements.
	ProtocolVersion = "2024-11-05"
	// ServerName is the name reported in initialize responses.
	ServerName = "agentos-mcp"
)

// Server is the MCP JSON-RPC 2.0 server.
type Server struct {
	registry       *ToolRegistry
	clearanceStore ClearanceStore
	govEngine      *governance.Engine // optional — nil means no governance enforcement
	version        string
	http           *http.Server

	mu          sync.Mutex
	initialized bool
}

// NewServer creates a new MCP server that listens on the given address.
// The version string is reported in initialize responses.
// NewServer creates a new MCP server. govEngine is optional (nil = no governance).
func NewServer(addr string, version string, registry *ToolRegistry, clearanceStore ClearanceStore, govEngine ...*governance.Engine) *Server {
	var engine *governance.Engine
	if len(govEngine) > 0 {
		engine = govEngine[0]
	}
	s := &Server{
		registry:       registry,
		clearanceStore: clearanceStore,
		govEngine:      engine,
		version:        version,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRPC)

	s.http = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return s
}

// ListenAndServe starts the MCP server. It blocks until the server is stopped.
func (s *Server) ListenAndServe() error {
	slog.Info("mcp: server starting", "addr", s.http.Addr)
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("mcp: listen: %w", err)
	}
	// Store the resolved address so tests can discover the actual port.
	s.http.Addr = ln.Addr().String()
	return s.http.Serve(ln)
}

// Addr returns the address the server is configured to listen on.
// After ListenAndServe is called, this returns the resolved address
// (including the actual port if :0 was used).
func (s *Server) Addr() string {
	return s.http.Addr
}

// Shutdown gracefully shuts down the MCP server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("mcp: server shutting down")
	return s.http.Shutdown(ctx)
}

// handleRPC is the single HTTP handler that processes JSON-RPC 2.0 requests.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, nil, CodeInvalidRequest, "only POST is allowed")
		return
	}

	// Use http.MaxBytesReader so the server returns 413 on oversize bodies
	// instead of silently truncating (v9.0 M-10).
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("mcp: failed to read request body", "error", err)
		s.writeError(w, nil, CodeParseError, "failed to read request body")
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, nil, CodeParseError, "invalid JSON")
		return
	}

	if req.JSONRPC != "2.0" {
		s.writeError(w, req.ID, CodeInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	slog.Debug("mcp: request received", "method", req.Method, "id", string(req.ID))

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, req)
	case "tools/list":
		s.handleToolsList(w, req)
	case "tools/call":
		s.handleToolsCall(w, r.Context(), r, req)
	default:
		s.writeError(w, req.ID, CodeMethodNotFound, fmt.Sprintf("method %q not found", req.Method))
	}
}

// handleInitialize handles the MCP initialize handshake.
func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()

	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ServerCaps{
			Tools: &ToolsCap{ListChanged: false},
		},
		ServerInfo: ServerInfo{
			Name:    ServerName,
			Version: s.version,
		},
	}

	s.writeResult(w, req.ID, result)
}

// requireInitialized checks that the initialize handshake has occurred.
// Returns true if the server is initialized; false (and writes an error) otherwise (v9.0 L-7).
func (s *Server) requireInitialized(w http.ResponseWriter, req Request) bool {
	s.mu.Lock()
	ok := s.initialized
	s.mu.Unlock()
	if !ok {
		s.writeError(w, req.ID, CodeInvalidRequest, "server not initialized — call initialize first")
		return false
	}
	return true
}

// handleToolsList returns all registered tools.
func (s *Server) handleToolsList(w http.ResponseWriter, req Request) {
	if !s.requireInitialized(w, req) {
		return
	}
	tools := s.registry.List()

	defs := make([]ToolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	s.writeResult(w, req.ID, ToolsListResult{Tools: defs})
}

// handleToolsCall dispatches to the named tool's handler.
func (s *Server) handleToolsCall(w http.ResponseWriter, ctx context.Context, r *http.Request, req Request) {
	if !s.requireInitialized(w, req) {
		return
	}

	// Extract MCP auth context from HTTP headers, but ONLY trust them when
	// the request was authenticated via a validated credential (API key, OIDC)
	// or when running in dev mode. This prevents header spoofing (v9.0 C-1/C-2).
	ac, _ := auth.Get(ctx)
	if ac.Validated || auth.DevMode() {
		if agentID := r.Header.Get("X-Agent-ID"); agentID != "" {
			ctx = WithMCPAuth(ctx, MCPAuthContext{
				AgentID:  agentID,
				TenantID: r.Header.Get("X-Tenant-ID"),
			})
		} else if tenantID := r.Header.Get("X-Tenant-ID"); tenantID != "" {
			ctx = WithMCPAuth(ctx, MCPAuthContext{
				AgentID:  tenantID,
				TenantID: tenantID,
			})
		}
	} else if r.Header.Get("X-Agent-ID") != "" || r.Header.Get("X-Tenant-ID") != "" {
		slog.Warn("mcp: rejecting unvalidated identity headers",
			"agent_id", r.Header.Get("X-Agent-ID"),
			"tenant_id", r.Header.Get("X-Tenant-ID"),
			"path", r.URL.Path,
		)
		s.writeError(w, req.ID, CodeInvalidRequest, "insufficient clearance: identity headers require validated credentials (API key or OIDC token)")
		return
	}
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(w, req.ID, CodeInvalidParams, "invalid tools/call params")
		return
	}

	tool := s.registry.Get(params.Name)
	if tool == nil {
		s.writeError(w, req.ID, CodeInvalidParams, fmt.Sprintf("tool %q not found", params.Name))
		return
	}

	if s.clearanceStore == nil {
		s.writeError(w, req.ID, CodeInternalError, "authorization unavailable")
		return
	}
	if rpcErr := AuthorizeToolCall(ctx, tool, s.clearanceStore); rpcErr != nil {
		s.writeError(w, req.ID, rpcErr.Code, rpcErr.Message)
		return
	}

	// Governance check (if engine is loaded)
	if s.govEngine != nil {
		ac, _ := GetMCPAuth(ctx)
		start := time.Now()
		var govArgs map[string]any
		_ = json.Unmarshal(params.Arguments, &govArgs) // best-effort parse for governance
		govOpts := governance.CheckOpts{}
		if tool.Category == "observe" {
			govOpts.Category = governance.CategoryObserve
		} else {
			govOpts.Category = governance.CategoryAction
		}
		decision, govErr := s.govEngine.Check(ctx, ac.AgentID, params.Name, govArgs, govOpts)
		duration := time.Since(start)
		if govErr != nil {
			slog.Error("mcp: governance check error", "tool", params.Name, "agent", ac.AgentID, "error", govErr, "duration_ms", duration.Milliseconds())
			// Fail closed — deny on error
			s.writeError(w, req.ID, CodeInternalError, fmt.Sprintf("governance check failed: %v", govErr))
			return
		}
		if decision.Verdict == governance.Deny {
			slog.Warn("mcp: governance denied", "tool", params.Name, "agent", ac.AgentID, "reason", decision.Reason, "domain", decision.Domain, "duration_ms", duration.Milliseconds())
			s.writeError(w, req.ID, CodeInvalidRequest, fmt.Sprintf("governance denied: %s", decision.Reason))
			return
		}
		slog.Debug("mcp: governance allowed", "tool", params.Name, "agent", ac.AgentID, "duration_ms", duration.Milliseconds())
	}

	result, err := tool.Handler(ctx, params.Arguments)
	if err != nil {
		slog.Error("mcp: tool call failed", "tool", params.Name, "error", err)
		// Track failure for circuit breaker
		if s.govEngine != nil {
			ac, _ := GetMCPAuth(ctx)
			s.govEngine.RecordResult(ac.AgentID, false)
		}
		s.writeResult(w, req.ID, ToolCallResult{
			Content: []ToolContent{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
		return
	}

	// Track success for circuit breaker
	if s.govEngine != nil {
		ac, _ := GetMCPAuth(ctx)
		s.govEngine.RecordResult(ac.AgentID, true)
	}

	// Marshal the handler result to a text content block.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		slog.Error("mcp: failed to marshal tool result", "tool", params.Name, "error", err)
		s.writeResult(w, req.ID, ToolCallResult{
			Content: []ToolContent{{Type: "text", Text: "internal error: failed to marshal result"}},
			IsError: true,
		})
		return
	}

	s.writeResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(resultBytes)}},
	})
}

// writeResult sends a successful JSON-RPC response.
func (s *Server) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("mcp: failed to write response", "error", err)
	}
}

// writeError sends a JSON-RPC error response.
func (s *Server) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("mcp: failed to write error response", "error", err)
	}
}
