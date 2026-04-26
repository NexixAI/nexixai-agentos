package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestCreateSnapshot(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	cfg := json.RawMessage(`{"system_prompt":"hello"}`)
	err := store.CreateSnapshot(ctx, "t1", "a1", "v1", "user@test.com", cfg)
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	count, err := store.CountVersions(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("CountVersions failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count=1, got %d", count)
	}
}

func TestCreateSnapshotValidation(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	tests := []struct {
		name     string
		tenant   string
		agent    string
		version  string
		wantErr  bool
	}{
		{"empty tenant", "", "a1", "v1", true},
		{"empty agent", "t1", "", "v1", true},
		{"empty version", "t1", "a1", "", true},
		{"valid", "t1", "a1", "v1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.CreateSnapshot(ctx, tt.tenant, tt.agent, tt.version, "user", nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("wantErr=%v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestGetVersion(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	cfg1 := json.RawMessage(`{"model_id":"gpt-4"}`)
	cfg2 := json.RawMessage(`{"model_id":"gpt-4o"}`)
	if err := store.CreateSnapshot(ctx, "t1", "a1", "v1", "alice", cfg1); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSnapshot(ctx, "t1", "a1", "v2", "bob", cfg2); err != nil {
		t.Fatal(err)
	}

	v, err := store.GetVersion(ctx, "t1", "a1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if v == nil {
		t.Fatal("expected version v1, got nil")
	}
	if v.Version != "v1" {
		t.Fatalf("expected version=v1, got %s", v.Version)
	}
	if v.CreatedBy != "alice" {
		t.Fatalf("expected created_by=alice, got %s", v.CreatedBy)
	}
	if string(v.Config) != `{"model_id":"gpt-4"}` {
		t.Fatalf("unexpected config: %s", string(v.Config))
	}

	// Non-existent version returns nil.
	v, err = store.GetVersion(ctx, "t1", "a1", "v99")
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Fatal("expected nil for non-existent version")
	}

	// Empty inputs return nil.
	v, err = store.GetVersion(ctx, "", "a1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Fatal("expected nil for empty tenant")
	}
}

func TestListVersionsPagination(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	// Create 5 versions.
	for i := 1; i <= 5; i++ {
		cfg := json.RawMessage(fmt.Sprintf(`{"step":%d}`, i))
		if err := store.CreateSnapshot(ctx, "t1", "a1", fmt.Sprintf("v%d", i), "user", cfg); err != nil {
			t.Fatal(err)
		}
	}

	// Page 1: limit=2, afterID=0.
	versions, hasMore, err := store.ListVersions(ctx, "t1", "a1", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if !hasMore {
		t.Fatal("expected has_more=true")
	}
	if versions[0].Version != "v1" || versions[1].Version != "v2" {
		t.Fatalf("unexpected versions: %s, %s", versions[0].Version, versions[1].Version)
	}

	// Page 2: afterID = last ID from page 1.
	versions, hasMore, err = store.ListVersions(ctx, "t1", "a1", 2, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if !hasMore {
		t.Fatal("expected has_more=true")
	}
	if versions[0].Version != "v3" || versions[1].Version != "v4" {
		t.Fatalf("unexpected versions: %s, %s", versions[0].Version, versions[1].Version)
	}

	// Page 3: afterID = last ID from page 2 — should get 1 remaining.
	versions, hasMore, err = store.ListVersions(ctx, "t1", "a1", 2, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if hasMore {
		t.Fatal("expected has_more=false")
	}
	if versions[0].Version != "v5" {
		t.Fatalf("expected v5, got %s", versions[0].Version)
	}
}

func TestListVersionsDefaultLimit(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	// Zero limit defaults to 20.
	versions, _, err := store.ListVersions(ctx, "t1", "a1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if versions == nil {
		// Empty is fine for no data.
	}
	_ = versions
}

func TestListVersionsEmptyInputs(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	versions, hasMore, err := store.ListVersions(ctx, "", "a1", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if versions != nil || hasMore {
		t.Fatal("expected nil/false for empty tenant")
	}
}

func TestCountVersions(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	count, err := store.CountVersions(ctx, "t1", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}

	for i := 0; i < 3; i++ {
		if err := store.CreateSnapshot(ctx, "t1", "a1", fmt.Sprintf("v%d", i+1), "user", nil); err != nil {
			t.Fatal(err)
		}
	}

	count, err = store.CountVersions(ctx, "t1", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}

	// Different agent is isolated.
	count, err = store.CountVersions(ctx, "t1", "a2")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 for different agent, got %d", count)
	}
}

func TestBoundedCollection(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	// Create maxVersionsPerAgent + 50 snapshots.
	total := maxVersionsPerAgent + 50
	for i := 1; i <= total; i++ {
		cfg := json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))
		if err := store.CreateSnapshot(ctx, "t1", "a1", fmt.Sprintf("v%d", i), "user", cfg); err != nil {
			t.Fatal(err)
		}
	}

	count, err := store.CountVersions(ctx, "t1", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if count != maxVersionsPerAgent {
		t.Fatalf("expected bounded at %d, got %d", maxVersionsPerAgent, count)
	}

	// The oldest versions should have been evicted. The first remaining version
	// should be v51 (the first 50 were evicted).
	versions, _, err := store.ListVersions(ctx, "t1", "a1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].Version != "v51" {
		t.Fatalf("expected oldest remaining to be v51, got %s", versions[0].Version)
	}
}

func TestCrossAgentIsolation(t *testing.T) {
	store := NewMemoryAgentVersionStore()
	ctx := context.Background()

	if err := store.CreateSnapshot(ctx, "t1", "a1", "v1", "user", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSnapshot(ctx, "t1", "a2", "v1", "user", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSnapshot(ctx, "t2", "a1", "v1", "user", nil); err != nil {
		t.Fatal(err)
	}

	c1, _ := store.CountVersions(ctx, "t1", "a1")
	c2, _ := store.CountVersions(ctx, "t1", "a2")
	c3, _ := store.CountVersions(ctx, "t2", "a1")
	if c1 != 1 || c2 != 1 || c3 != 1 {
		t.Fatalf("expected each agent to have 1 version, got %d/%d/%d", c1, c2, c3)
	}

	// GetVersion should not cross agents.
	v, _ := store.GetVersion(ctx, "t1", "a2", "v1")
	if v == nil {
		t.Fatal("expected version for t1/a2")
	}
	v, _ = store.GetVersion(ctx, "t2", "a1", "v1")
	if v == nil {
		t.Fatal("expected version for t2/a1")
	}
	v, _ = store.GetVersion(ctx, "t2", "a2", "v1")
	if v != nil {
		t.Fatal("expected nil for non-existent t2/a2")
	}
}
