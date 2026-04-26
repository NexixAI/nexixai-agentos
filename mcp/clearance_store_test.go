package mcp

import (
	"context"
	"testing"
)

func TestInMemoryClearanceStore_ExplicitAgent(t *testing.T) {
	store := NewInMemoryClearanceStore(
		WithAgents(map[string]ClearanceTier{
			"agent-1": ClearanceAdmin,
		}),
	)

	tier, err := store.GetClearance(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != ClearanceAdmin {
		t.Errorf("GetClearance() = %d, want %d (ClearanceAdmin)", tier, ClearanceAdmin)
	}
}

func TestInMemoryClearanceStore_FailClosedByDefault(t *testing.T) {
	store := NewInMemoryClearanceStore()

	_, err := store.GetClearance(context.Background(), "unknown-agent")
	if err == nil {
		t.Fatal("GetClearance() for unknown agent should return error when fail-closed (default)")
	}
}

func TestInMemoryClearanceStore_DefaultTier(t *testing.T) {
	store := NewInMemoryClearanceStore(WithDefaultTier(ClearanceInternal))

	tier, err := store.GetClearance(context.Background(), "any-agent")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != ClearanceInternal {
		t.Errorf("GetClearance() = %d, want %d (ClearanceInternal)", tier, ClearanceInternal)
	}
}

func TestInMemoryClearanceStore_ExplicitOverridesDefault(t *testing.T) {
	store := NewInMemoryClearanceStore(
		WithDefaultTier(ClearancePublic),
		WithAgents(map[string]ClearanceTier{
			"admin-agent": ClearanceSuperAdmin,
		}),
	)

	// Explicit agent gets its registered tier.
	tier, err := store.GetClearance(context.Background(), "admin-agent")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != ClearanceSuperAdmin {
		t.Errorf("GetClearance() = %d, want %d (ClearanceSuperAdmin)", tier, ClearanceSuperAdmin)
	}

	// Unknown agent gets default.
	tier, err = store.GetClearance(context.Background(), "random-agent")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != ClearancePublic {
		t.Errorf("GetClearance() = %d, want %d (ClearancePublic)", tier, ClearancePublic)
	}
}

func TestInMemoryClearanceStore_EmptyAgentID(t *testing.T) {
	store := NewInMemoryClearanceStore(WithDefaultTier(ClearanceAdmin))

	_, err := store.GetClearance(context.Background(), "")
	if err == nil {
		t.Fatal("GetClearance() with empty agent_id should return error")
	}
}

func TestInMemoryClearanceStore_SetClearance(t *testing.T) {
	store := NewInMemoryClearanceStore()

	if err := store.SetClearance("agent-1", ClearanceExecute); err != nil {
		t.Fatalf("SetClearance() returned error: %v", err)
	}

	tier, err := store.GetClearance(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != ClearanceExecute {
		t.Errorf("GetClearance() = %d, want %d (ClearanceExecute)", tier, ClearanceExecute)
	}
}

func TestInMemoryClearanceStore_SetClearanceEmptyID(t *testing.T) {
	store := NewInMemoryClearanceStore()

	err := store.SetClearance("", ClearancePublic)
	if err == nil {
		t.Fatal("SetClearance() with empty agent_id should return error")
	}
}

func TestInMemoryClearanceStore_SetClearanceUpdates(t *testing.T) {
	store := NewInMemoryClearanceStore()

	if err := store.SetClearance("agent-1", ClearancePublic); err != nil {
		t.Fatalf("SetClearance() returned error: %v", err)
	}
	if err := store.SetClearance("agent-1", ClearanceAdmin); err != nil {
		t.Fatalf("SetClearance() returned error: %v", err)
	}

	tier, err := store.GetClearance(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != ClearanceAdmin {
		t.Errorf("GetClearance() after update = %d, want %d (ClearanceAdmin)", tier, ClearanceAdmin)
	}
}

func TestInMemoryClearanceStore_DefaultTierPublic(t *testing.T) {
	// Verify that WithDefaultTier(ClearancePublic) works — tier 0 should
	// not be confused with "not set".
	store := NewInMemoryClearanceStore(WithDefaultTier(ClearancePublic))

	tier, err := store.GetClearance(context.Background(), "any-agent")
	if err != nil {
		t.Fatalf("GetClearance() returned error: %v", err)
	}
	if tier != ClearancePublic {
		t.Errorf("GetClearance() = %d, want %d (ClearancePublic)", tier, ClearancePublic)
	}
}
