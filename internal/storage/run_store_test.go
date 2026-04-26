package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestFileRunStorePersistsRunsAndEnforcesTenantScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.json")

	store, err := NewFileRunStore(path)
	if err != nil {
		t.Fatalf("NewFileRunStore error: %v", err)
	}

	run := types.Run{
		TenantID:  "tnt_alpha",
		AgentID:   "agt_demo",
		RunID:     "run_123",
		Status:    "queued",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		EventsURL: "/v1/runs/run_123/events",
	}

	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create run error: %v", err)
	}

	if _, ok, _ := store.Get(ctx, "tnt_other", run.RunID); ok {
		t.Fatalf("expected tenant isolation on Get")
	}

	fetched, ok, err := store.Get(ctx, run.TenantID, run.RunID)
	if err != nil {
		t.Fatalf("Get run error: %v", err)
	}
	if !ok {
		t.Fatalf("run not found after create")
	}
	if fetched.AgentID != run.AgentID {
		t.Fatalf("unexpected AgentID: %s", fetched.AgentID)
	}

	fetched.Status = "completed"
	fetched.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	if err := store.Save(ctx, fetched); err != nil {
		t.Fatalf("Save run error: %v", err)
	}

	reloaded, err := NewFileRunStore(path)
	if err != nil {
		t.Fatalf("reload store error: %v", err)
	}
	persisted, ok, err := reloaded.Get(ctx, run.TenantID, run.RunID)
	if err != nil {
		t.Fatalf("Get after reload error: %v", err)
	}
	if !ok {
		t.Fatalf("run missing after reload")
	}
	if persisted.Status != "completed" || persisted.CompletedAt == "" {
		t.Fatalf("expected persisted completion, got status=%s completed_at=%s", persisted.Status, persisted.CompletedAt)
	}

	if err := store.Create(ctx, run); !errors.Is(err, ErrRunExists) {
		t.Fatalf("expected ErrRunExists on duplicate create, got %v", err)
	}
}

func TestIdempotencyKeyEnforcedAcrossRetries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.json")

	store, err := NewFileRunStore(path)
	if err != nil {
		t.Fatalf("NewFileRunStore error: %v", err)
	}

	run := types.Run{
		TenantID:       "tnt_alpha",
		AgentID:        "agt_demo",
		RunID:          "run_idem_001",
		Status:         "queued",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		EventsURL:      "/v1/runs/run_idem_001/events",
		IdempotencyKey: "idem_unique_key_123",
	}

	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create run error: %v", err)
	}

	// First lookup should find the run
	found, ok, err := store.GetByIdempotencyKey(ctx, run.TenantID, run.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetByIdempotencyKey error: %v", err)
	}
	if !ok {
		t.Fatalf("expected to find run by idempotency key")
	}
	if found.RunID != run.RunID {
		t.Fatalf("expected run_id=%s, got %s", run.RunID, found.RunID)
	}

	// Simulate retry: lookup should still return the same run
	retryFound, ok, err := store.GetByIdempotencyKey(ctx, run.TenantID, run.IdempotencyKey)
	if err != nil {
		t.Fatalf("retry GetByIdempotencyKey error: %v", err)
	}
	if !ok {
		t.Fatalf("expected to find run on retry")
	}
	if retryFound.RunID != run.RunID {
		t.Fatalf("expected same run_id on retry, got %s", retryFound.RunID)
	}

	// Non-existent key should return not found
	_, ok, err = store.GetByIdempotencyKey(ctx, run.TenantID, "non_existent_key")
	if err != nil {
		t.Fatalf("GetByIdempotencyKey for non-existent error: %v", err)
	}
	if ok {
		t.Fatalf("expected not found for non-existent idempotency key")
	}
}

func TestDifferentTenantsCanUseSameIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.json")

	store, err := NewFileRunStore(path)
	if err != nil {
		t.Fatalf("NewFileRunStore error: %v", err)
	}

	sharedIdemKey := "shared_idem_key_456"

	runTenantA := types.Run{
		TenantID:       "tnt_alpha",
		AgentID:        "agt_demo",
		RunID:          "run_tenant_a",
		Status:         "queued",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		EventsURL:      "/v1/runs/run_tenant_a/events",
		IdempotencyKey: sharedIdemKey,
	}

	runTenantB := types.Run{
		TenantID:       "tnt_beta",
		AgentID:        "agt_demo",
		RunID:          "run_tenant_b",
		Status:         "queued",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		EventsURL:      "/v1/runs/run_tenant_b/events",
		IdempotencyKey: sharedIdemKey,
	}

	// Create runs for both tenants with same idempotency key
	if err := store.Create(ctx, runTenantA); err != nil {
		t.Fatalf("Create run for tenant A error: %v", err)
	}
	if err := store.Create(ctx, runTenantB); err != nil {
		t.Fatalf("Create run for tenant B error: %v", err)
	}

	// Each tenant should get their own run
	foundA, ok, err := store.GetByIdempotencyKey(ctx, "tnt_alpha", sharedIdemKey)
	if err != nil {
		t.Fatalf("GetByIdempotencyKey for tenant A error: %v", err)
	}
	if !ok {
		t.Fatalf("expected to find run for tenant A")
	}
	if foundA.RunID != "run_tenant_a" {
		t.Fatalf("expected run_tenant_a for tenant A, got %s", foundA.RunID)
	}

	foundB, ok, err := store.GetByIdempotencyKey(ctx, "tnt_beta", sharedIdemKey)
	if err != nil {
		t.Fatalf("GetByIdempotencyKey for tenant B error: %v", err)
	}
	if !ok {
		t.Fatalf("expected to find run for tenant B")
	}
	if foundB.RunID != "run_tenant_b" {
		t.Fatalf("expected run_tenant_b for tenant B, got %s", foundB.RunID)
	}

	// Cross-tenant lookup should not find the other tenant's run
	_, ok, err = store.GetByIdempotencyKey(ctx, "tnt_gamma", sharedIdemKey)
	if err != nil {
		t.Fatalf("GetByIdempotencyKey for tenant C error: %v", err)
	}
	if ok {
		t.Fatalf("expected not found for tenant that didn't create a run")
	}
}
