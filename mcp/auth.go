package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// ClearanceStore abstracts clearance tier lookups for agents.
// Implementations MUST return an error for unknown agents (fail closed).
type ClearanceStore interface {
	GetClearance(ctx context.Context, agentID string) (ClearanceTier, error)
}

// authContextKey is the unexported key type for MCP auth context values.
type authContextKey struct{}

// MCPAuthContext holds the identity extracted from an MCP request.
type MCPAuthContext struct {
	AgentID  string
	TenantID string
}

// WithMCPAuth stores auth context on a context.Context.
func WithMCPAuth(ctx context.Context, ac MCPAuthContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, ac)
}

// GetMCPAuth retrieves auth context from a context.Context.
// Returns the context and true if present, zero value and false otherwise.
func GetMCPAuth(ctx context.Context) (MCPAuthContext, bool) {
	v := ctx.Value(authContextKey{})
	if v == nil {
		return MCPAuthContext{}, false
	}
	ac, ok := v.(MCPAuthContext)
	return ac, ok
}

// AuthorizeToolCall checks whether the calling agent has sufficient clearance
// to invoke the given tool. It returns a JSON-RPC error response if denied,
// or nil if the call is authorized.
//
// Auth invariant: DENY on any error — missing context, store failure, or
// unknown agent. This function never falls through to "allow" on error.
func AuthorizeToolCall(ctx context.Context, tool *Tool, store ClearanceStore) *RPCError {
	ac, ok := GetMCPAuth(ctx)
	if !ok {
		slog.Warn("mcp/auth: denied — missing auth context", "tool", tool.Name)
		return &RPCError{
			Code:    CodeInvalidRequest,
			Message: "insufficient clearance: missing auth context",
		}
	}

	if ac.AgentID == "" {
		slog.Warn("mcp/auth: denied — empty agent_id", "tool", tool.Name)
		return &RPCError{
			Code:    CodeInvalidRequest,
			Message: "insufficient clearance: missing agent identity",
		}
	}

	tier, err := store.GetClearance(ctx, ac.AgentID)
	if err != nil {
		// Fail closed: any store error means deny.
		slog.Warn("mcp/auth: denied — clearance lookup failed",
			"tool", tool.Name,
			"agent_id", ac.AgentID,
			"error", err,
		)
		return &RPCError{
			Code:    CodeInvalidRequest,
			Message: fmt.Sprintf("insufficient clearance: %v", err),
		}
	}

	if tier < tool.MinClearance {
		slog.Info("mcp/auth: denied — insufficient tier",
			"tool", tool.Name,
			"agent_id", ac.AgentID,
			"agent_tier", tier,
			"required_tier", tool.MinClearance,
		)
		return &RPCError{
			Code:    CodeInvalidRequest,
			Message: fmt.Sprintf("insufficient clearance: agent tier %d < required %d", tier, tool.MinClearance),
		}
	}

	slog.Debug("mcp/auth: authorized",
		"tool", tool.Name,
		"agent_id", ac.AgentID,
		"agent_tier", tier,
	)
	return nil
}

// AuthMiddleware returns a ToolHandler that wraps the given handler with
// clearance-based authorization. It is intended to be applied per-tool
// in the MCP server's dispatch path.
func AuthMiddleware(store ClearanceStore, tool *Tool) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		if rpcErr := AuthorizeToolCall(ctx, tool, store); rpcErr != nil {
			return nil, fmt.Errorf("%s", rpcErr.Message)
		}
		return tool.Handler(ctx, params)
	}
}
