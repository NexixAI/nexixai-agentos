package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- mock ClearanceStore for unit tests ---
// The PostgresClearanceStore itself requires a real database.
// These tests validate the ClearanceStore interface contract using an
// in-memory mock. Integration tests against Postgres come later.

type mockClearanceStore struct {
	agents map[string]mockAgent
	err    error
}

type mockAgent struct {
	tenantID string
	tier     mcp.ClearanceTier
}

func newMockClearanceStore() *mockClearanceStore {
	return &mockClearanceStore{
		agents: make(map[string]mockAgent),
	}
}

func (m *mockClearanceStore) GetClearance(_ context.Context, agentID string) (mcp.ClearanceTier, error) {
	if m.err != nil {
		return 0, m.err
	}
	if agentID == "" {
		return 0, errors.New("clearance: agent_id must not be empty")
	}
	agent, ok := m.agents[agentID]
	if !ok {
		return 0, errors.New("clearance: agent not found")
	}
	return agent.tier, nil
}

func (m *mockClearanceStore) SetClearance(_ context.Context, agentID, tenantID string, tier mcp.ClearanceTier) error {
	if m.err != nil {
		return m.err
	}
	if agentID == "" {
		return errors.New("clearance: agent_id must not be empty")
	}
	if tenantID == "" {
		return errors.New("clearance: tenant_id must not be empty")
	}
	m.agents[agentID] = mockAgent{tenantID: tenantID, tier: tier}
	return nil
}

func TestGetClearance_ReturnsCorrectTier(t *testing.T) {
	store := newMockClearanceStore()
	ctx := context.Background()

	// Seed an agent with Internal clearance.
	if err := store.SetClearance(ctx, "agent-1", "tenant-1", mcp.ClearanceInternal); err != nil {
		t.Fatalf("SetClearance() returned error: %v", err)
	}

	tier, err := store.GetClearance(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != mcp.ClearanceInternal {
		t.Errorf("GetClearance() = %d, want %d", tier, mcp.ClearanceInternal)
	}
}

func TestGetClearance_UnknownAgentReturnsError(t *testing.T) {
	store := newMockClearanceStore()
	ctx := context.Background()

	// Do NOT seed any agents. Unknown agent must fail closed (error, not default tier).
	_, err := store.GetClearance(ctx, "nonexistent-agent")
	if err == nil {
		t.Fatal("GetClearance() for unknown agent should return error, got nil")
	}
}

func TestGetClearance_EmptyAgentIDReturnsError(t *testing.T) {
	store := newMockClearanceStore()
	ctx := context.Background()

	_, err := store.GetClearance(ctx, "")
	if err == nil {
		t.Fatal("GetClearance() with empty agent_id should return error, got nil")
	}
}

func TestSetClearance_UpdatesTier(t *testing.T) {
	store := newMockClearanceStore()
	ctx := context.Background()

	// Set initial tier.
	if err := store.SetClearance(ctx, "agent-1", "tenant-1", mcp.ClearancePublic); err != nil {
		t.Fatalf("SetClearance() returned error: %v", err)
	}

	// Update tier.
	if err := store.SetClearance(ctx, "agent-1", "tenant-1", mcp.ClearanceAdmin); err != nil {
		t.Fatalf("SetClearance() returned error: %v", err)
	}

	tier, err := store.GetClearance(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != mcp.ClearanceAdmin {
		t.Errorf("GetClearance() after update = %d, want %d", tier, mcp.ClearanceAdmin)
	}
}

func TestSetClearance_EmptyAgentIDReturnsError(t *testing.T) {
	store := newMockClearanceStore()
	ctx := context.Background()

	err := store.SetClearance(ctx, "", "tenant-1", mcp.ClearancePublic)
	if err == nil {
		t.Fatal("SetClearance() with empty agent_id should return error, got nil")
	}
}

func TestSetClearance_EmptyTenantIDReturnsError(t *testing.T) {
	store := newMockClearanceStore()
	ctx := context.Background()

	err := store.SetClearance(ctx, "agent-1", "", mcp.ClearancePublic)
	if err == nil {
		t.Fatal("SetClearance() with empty tenant_id should return error, got nil")
	}
}

func TestGetClearance_StoreErrorReturnsError(t *testing.T) {
	store := &mockClearanceStore{
		agents: make(map[string]mockAgent),
		err:    errors.New("database down"),
	}
	ctx := context.Background()

	_, err := store.GetClearance(ctx, "agent-1")
	if err == nil {
		t.Fatal("GetClearance() should propagate store error, got nil")
	}
}

// Verify that the PostgresClearanceStore implements the ClearanceStore interface.
// This is a compile-time check, not a runtime test.
var _ ClearanceStore = (*PostgresClearanceStore)(nil)

// Verify that InMemoryElevationStore implements the ElevationStore interface.
var _ ElevationStore = (*InMemoryElevationStore)(nil)

// --- ElevationStore tests ---

func TestRequestElevation_AndGetStatus_RoundTrip(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	reqID, err := store.RequestElevation(ctx, "agent-1", "tenant-1", 3, "need admin access for deployment")
	if err != nil {
		t.Fatalf("RequestElevation() returned error: %v", err)
	}
	if reqID == "" {
		t.Fatal("RequestElevation() returned empty request ID")
	}

	req, err := store.GetElevationStatus(ctx, reqID)
	if err != nil {
		t.Fatalf("GetElevationStatus() returned error: %v", err)
	}
	if req.ID != reqID {
		t.Errorf("ID = %q, want %q", req.ID, reqID)
	}
	if req.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", req.AgentID, "agent-1")
	}
	if req.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", req.TenantID, "tenant-1")
	}
	if req.RequestedTier != 3 {
		t.Errorf("RequestedTier = %d, want 3", req.RequestedTier)
	}
	if req.Reason != "need admin access for deployment" {
		t.Errorf("Reason = %q, want %q", req.Reason, "need admin access for deployment")
	}
	if req.Status != "pending" {
		t.Errorf("Status = %q, want %q", req.Status, "pending")
	}
	if req.ApprovedAt != nil {
		t.Error("ApprovedAt should be nil for pending request")
	}
	if req.ExpiresAt != nil {
		t.Error("ExpiresAt should be nil for pending request")
	}
}

func TestApproveElevation_SetsStatusAndExpiry(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	reqID, err := store.RequestElevation(ctx, "agent-1", "tenant-1", 2, "need execute access")
	if err != nil {
		t.Fatalf("RequestElevation() returned error: %v", err)
	}

	duration := 1 * time.Hour
	if err := store.ApproveElevation(ctx, reqID, duration); err != nil {
		t.Fatalf("ApproveElevation() returned error: %v", err)
	}

	req, err := store.GetElevationStatus(ctx, reqID)
	if err != nil {
		t.Fatalf("GetElevationStatus() returned error: %v", err)
	}
	if req.Status != "approved" {
		t.Errorf("Status = %q, want %q", req.Status, "approved")
	}
	if req.ApprovedAt == nil {
		t.Fatal("ApprovedAt should not be nil for approved request")
	}
	if req.ExpiresAt == nil {
		t.Fatal("ExpiresAt should not be nil for approved request")
	}
	if req.ExpiresAt.Before(*req.ApprovedAt) {
		t.Error("ExpiresAt should be after ApprovedAt")
	}
}

func TestDenyElevation_SetsStatus(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	reqID, err := store.RequestElevation(ctx, "agent-1", "tenant-1", 4, "need superadmin")
	if err != nil {
		t.Fatalf("RequestElevation() returned error: %v", err)
	}

	if err := store.DenyElevation(ctx, reqID); err != nil {
		t.Fatalf("DenyElevation() returned error: %v", err)
	}

	req, err := store.GetElevationStatus(ctx, reqID)
	if err != nil {
		t.Fatalf("GetElevationStatus() returned error: %v", err)
	}
	if req.Status != "denied" {
		t.Errorf("Status = %q, want %q", req.Status, "denied")
	}
}

func TestRequestElevation_InvalidTier(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	_, err := store.RequestElevation(ctx, "agent-1", "tenant-1", 5, "too high")
	if err == nil {
		t.Fatal("RequestElevation() with tier 5 should return error, got nil")
	}

	_, err = store.RequestElevation(ctx, "agent-1", "tenant-1", -1, "negative")
	if err == nil {
		t.Fatal("RequestElevation() with tier -1 should return error, got nil")
	}
}

func TestRequestElevation_EmptyFields(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	_, err := store.RequestElevation(ctx, "", "tenant-1", 2, "reason")
	if err == nil {
		t.Fatal("RequestElevation() with empty agent_id should return error")
	}

	_, err = store.RequestElevation(ctx, "agent-1", "", 2, "reason")
	if err == nil {
		t.Fatal("RequestElevation() with empty tenant_id should return error")
	}

	_, err = store.RequestElevation(ctx, "agent-1", "tenant-1", 2, "")
	if err == nil {
		t.Fatal("RequestElevation() with empty reason should return error")
	}
}

func TestGetElevationStatus_NotFound(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	_, err := store.GetElevationStatus(ctx, "nonexistent-id")
	if err == nil {
		t.Fatal("GetElevationStatus() for nonexistent ID should return error")
	}
}

func TestApproveElevation_NotFound(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	err := store.ApproveElevation(ctx, "nonexistent-id", time.Hour)
	if err == nil {
		t.Fatal("ApproveElevation() for nonexistent ID should return error")
	}
}

func TestDenyElevation_NotFound(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	err := store.DenyElevation(ctx, "nonexistent-id")
	if err == nil {
		t.Fatal("DenyElevation() for nonexistent ID should return error")
	}
}

func TestApproveElevation_InvalidDuration(t *testing.T) {
	store := NewInMemoryElevationStore()
	ctx := context.Background()

	reqID, err := store.RequestElevation(ctx, "agent-1", "tenant-1", 2, "reason")
	if err != nil {
		t.Fatalf("RequestElevation() returned error: %v", err)
	}

	err = store.ApproveElevation(ctx, reqID, 0)
	if err == nil {
		t.Fatal("ApproveElevation() with zero duration should return error")
	}

	err = store.ApproveElevation(ctx, reqID, -time.Hour)
	if err == nil {
		t.Fatal("ApproveElevation() with negative duration should return error")
	}
}
