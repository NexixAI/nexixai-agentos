//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// Integration tests for AgentVersionStore.
// Currently exercises the in-memory implementation through the interface.
// When a real Postgres backend exists, swap NewMemoryAgentVersionStore for a
// testcontainer-backed store to catch SQL/sentinel mismatches.

func TestIntegration_CreateAndListRoundtrip(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	cfg := json.RawMessage(`{"model":"gpt-4","max_steps":10}`)
	if err := store.CreateSnapshot(ctx, "t1", "agent-a", "v1.0", "user1", cfg); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := store.CreateSnapshot(ctx, "t1", "agent-a", "v1.1", "user1", cfg); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	versions, hasMore, err := store.ListVersions(ctx, "t1", "agent-a", 10, 0)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if hasMore {
		t.Error("expected hasMore=false for 2 items with limit=10")
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if versions[0].Version != "v1.0" {
		t.Errorf("expected first version v1.0, got %s", versions[0].Version)
	}
	if versions[1].Version != "v1.1" {
		t.Errorf("expected second version v1.1, got %s", versions[1].Version)
	}

	// Verify config is preserved through the round trip.
	var parsed map[string]any
	if err := json.Unmarshal(versions[0].Config, &parsed); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if parsed["model"] != "gpt-4" {
		t.Errorf("expected model gpt-4, got %v", parsed["model"])
	}
}

func TestIntegration_GetVersionByVersionString(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	cfg := json.RawMessage(`{"key":"value"}`)
	if err := store.CreateSnapshot(ctx, "t1", "agent-b", "v2.0", "admin", cfg); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	v, err := store.GetVersion(ctx, "t1", "agent-b", "v2.0")
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if v == nil {
		t.Fatal("expected version, got nil")
	}
	if v.Version != "v2.0" {
		t.Errorf("expected v2.0, got %s", v.Version)
	}
	if v.CreatedBy != "admin" {
		t.Errorf("expected created_by=admin, got %s", v.CreatedBy)
	}
	if v.TenantID != "t1" {
		t.Errorf("expected tenant_id=t1, got %s", v.TenantID)
	}
	if v.AgentID != "agent-b" {
		t.Errorf("expected agent_id=agent-b, got %s", v.AgentID)
	}

	// Not found case.
	v, err = store.GetVersion(ctx, "t1", "agent-b", "v9.9")
	if err != nil {
		t.Fatalf("GetVersion (not found): %v", err)
	}
	if v != nil {
		t.Errorf("expected nil for non-existent version, got %+v", v)
	}
}

func TestIntegration_CountVersionsAccuracy(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	cfg := json.RawMessage(`{}`)

	count, err := store.CountVersions(ctx, "t1", "agent-c")
	if err != nil {
		t.Fatalf("CountVersions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	for i := 0; i < 5; i++ {
		if err := store.CreateSnapshot(ctx, "t1", "agent-c", fmt.Sprintf("v%d", i), "user", cfg); err != nil {
			t.Fatalf("CreateSnapshot[%d]: %v", i, err)
		}
	}

	count, err = store.CountVersions(ctx, "t1", "agent-c")
	if err != nil {
		t.Fatalf("CountVersions: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5, got %d", count)
	}
}

func TestIntegration_BoundedEviction(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	cfg := json.RawMessage(`{}`)

	// Insert maxVersionsPerAgent + 10 snapshots.
	total := maxVersionsPerAgent + 10
	for i := 0; i < total; i++ {
		if err := store.CreateSnapshot(ctx, "t1", "agent-evict", fmt.Sprintf("v%d", i), "user", cfg); err != nil {
			t.Fatalf("CreateSnapshot[%d]: %v", i, err)
		}
	}

	count, err := store.CountVersions(ctx, "t1", "agent-evict")
	if err != nil {
		t.Fatalf("CountVersions: %v", err)
	}
	if count != maxVersionsPerAgent {
		t.Errorf("expected %d after eviction, got %d", maxVersionsPerAgent, count)
	}

	// The oldest 10 should have been evicted; first available should be v10.
	versions, _, err := store.ListVersions(ctx, "t1", "agent-evict", maxVersionsPerAgent, 0)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("expected versions, got none")
	}
	if versions[0].Version != "v10" {
		t.Errorf("expected oldest remaining to be v10, got %s", versions[0].Version)
	}
}

func TestIntegration_PaginationCursor(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	cfg := json.RawMessage(`{}`)
	for i := 0; i < 5; i++ {
		if err := store.CreateSnapshot(ctx, "t1", "agent-page", fmt.Sprintf("v%d", i), "user", cfg); err != nil {
			t.Fatalf("CreateSnapshot[%d]: %v", i, err)
		}
	}

	// Page 1: limit=2, afterID=0.
	page1, hasMore1, err := store.ListVersions(ctx, "t1", "agent-page", 2, 0)
	if err != nil {
		t.Fatalf("ListVersions page1: %v", err)
	}
	if !hasMore1 {
		t.Error("expected hasMore=true for page 1")
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 items in page 1, got %d", len(page1))
	}

	// Page 2: afterID = last ID from page 1.
	cursor := page1[len(page1)-1].ID
	page2, hasMore2, err := store.ListVersions(ctx, "t1", "agent-page", 2, cursor)
	if err != nil {
		t.Fatalf("ListVersions page2: %v", err)
	}
	if !hasMore2 {
		t.Error("expected hasMore=true for page 2")
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 items in page 2, got %d", len(page2))
	}

	// Page 3: should have 1 item, hasMore=false.
	cursor = page2[len(page2)-1].ID
	page3, hasMore3, err := store.ListVersions(ctx, "t1", "agent-page", 2, cursor)
	if err != nil {
		t.Fatalf("ListVersions page3: %v", err)
	}
	if hasMore3 {
		t.Error("expected hasMore=false for last page")
	}
	if len(page3) != 1 {
		t.Fatalf("expected 1 item in page 3, got %d", len(page3))
	}

	// Verify all IDs are unique and strictly ascending across pages.
	var allIDs []int64
	for _, p := range [][]AgentVersion{page1, page2, page3} {
		for _, v := range p {
			allIDs = append(allIDs, v.ID)
		}
	}
	for i := 1; i < len(allIDs); i++ {
		if allIDs[i] <= allIDs[i-1] {
			t.Errorf("IDs not strictly ascending: %v", allIDs)
			break
		}
	}
	if len(allIDs) != 5 {
		t.Errorf("expected 5 total items across pages, got %d", len(allIDs))
	}
}
