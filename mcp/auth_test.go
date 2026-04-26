package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// --- mock ClearanceStore for auth tests ---

type mockClearanceStore struct {
	tiers map[string]ClearanceTier
	err   error // if non-nil, GetClearance always returns this error
}

func (m *mockClearanceStore) GetClearance(_ context.Context, agentID string) (ClearanceTier, error) {
	if m.err != nil {
		return 0, m.err
	}
	tier, ok := m.tiers[agentID]
	if !ok {
		return 0, errors.New("agent not found")
	}
	return tier, nil
}

func TestAuthorizeToolCall_Allowed(t *testing.T) {
	// Agent with T2 clearance calls a T1 tool → allowed.
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{"agent-1": ClearanceAdmin},
	}
	tool := &Tool{
		Name:         "admin-tool",
		MinClearance: ClearanceInternal,
		Handler:      dummyHandler,
	}

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	rpcErr := AuthorizeToolCall(ctx, tool, store)
	if rpcErr != nil {
		t.Fatalf("expected nil error, got: %s", rpcErr.Message)
	}
}

func TestAuthorizeToolCall_SameTierAllowed(t *testing.T) {
	// Agent with T1 clearance calls a T1 tool → allowed.
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{"agent-1": ClearanceInternal},
	}
	tool := &Tool{
		Name:         "internal-tool",
		MinClearance: ClearanceInternal,
		Handler:      dummyHandler,
	}

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	rpcErr := AuthorizeToolCall(ctx, tool, store)
	if rpcErr != nil {
		t.Fatalf("expected nil error, got: %s", rpcErr.Message)
	}
}

func TestAuthorizeToolCall_InsufficientClearance(t *testing.T) {
	// Agent with T0 clearance cannot call T2 tool → denied.
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{"agent-low": ClearancePublic},
	}
	tool := &Tool{
		Name:         "admin-tool",
		MinClearance: ClearanceAdmin,
		Handler:      dummyHandler,
	}

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "agent-low",
		TenantID: "tenant-1",
	})

	rpcErr := AuthorizeToolCall(ctx, tool, store)
	if rpcErr == nil {
		t.Fatal("expected denial, got nil")
	}
	if rpcErr.Code != CodeInvalidRequest {
		t.Errorf("expected code %d, got %d", CodeInvalidRequest, rpcErr.Code)
	}
}

func TestAuthorizeToolCall_UnknownAgent(t *testing.T) {
	// Unknown agent → denied (fail closed).
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{}, // agent not present
	}
	tool := &Tool{
		Name:         "any-tool",
		MinClearance: ClearancePublic,
		Handler:      dummyHandler,
	}

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "unknown-agent",
		TenantID: "tenant-1",
	})

	rpcErr := AuthorizeToolCall(ctx, tool, store)
	if rpcErr == nil {
		t.Fatal("expected denial for unknown agent, got nil")
	}
}

func TestAuthorizeToolCall_StoreError(t *testing.T) {
	// Store error → denied (fail closed).
	store := &mockClearanceStore{
		err: errors.New("database connection lost"),
	}
	tool := &Tool{
		Name:         "any-tool",
		MinClearance: ClearancePublic,
		Handler:      dummyHandler,
	}

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	rpcErr := AuthorizeToolCall(ctx, tool, store)
	if rpcErr == nil {
		t.Fatal("expected denial on store error, got nil")
	}
}

func TestAuthorizeToolCall_MissingAuthContext(t *testing.T) {
	// No auth context on the context → denied.
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{"agent-1": ClearanceAdmin},
	}
	tool := &Tool{
		Name:         "any-tool",
		MinClearance: ClearancePublic,
		Handler:      dummyHandler,
	}

	// Plain context, no MCPAuthContext attached.
	rpcErr := AuthorizeToolCall(context.Background(), tool, store)
	if rpcErr == nil {
		t.Fatal("expected denial for missing auth context, got nil")
	}
}

func TestAuthorizeToolCall_EmptyAgentID(t *testing.T) {
	// Auth context present but agent_id is empty → denied.
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{},
	}
	tool := &Tool{
		Name:         "any-tool",
		MinClearance: ClearancePublic,
		Handler:      dummyHandler,
	}

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "",
		TenantID: "tenant-1",
	})

	rpcErr := AuthorizeToolCall(ctx, tool, store)
	if rpcErr == nil {
		t.Fatal("expected denial for empty agent_id, got nil")
	}
}

func TestAuthMiddleware_Allowed(t *testing.T) {
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{"agent-1": ClearanceAdmin},
	}
	tool := &Tool{
		Name:         "test-tool",
		MinClearance: ClearancePublic,
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]string{"status": "ok"}, nil
		},
	}

	wrapped := AuthMiddleware(store, tool)

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	result, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	m, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", result)
	}
	if m["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", m["status"])
	}
}

func TestAuthMiddleware_Denied(t *testing.T) {
	store := &mockClearanceStore{
		tiers: map[string]ClearanceTier{"agent-1": ClearancePublic},
	}
	tool := &Tool{
		Name:         "admin-tool",
		MinClearance: ClearanceAdmin,
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			t.Fatal("handler should not be called when denied")
			return nil, nil
		},
	}

	wrapped := AuthMiddleware(store, tool)

	ctx := WithMCPAuth(context.Background(), MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error from middleware, got nil")
	}
}
