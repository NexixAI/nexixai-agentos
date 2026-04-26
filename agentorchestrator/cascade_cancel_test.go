package agentorchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// TestCascadeCancelChildren verifies that cancelling a parent run also
// cancels child runs linked via RetryOf or ParentRunID.
func TestCascadeCancelChildren(t *testing.T) {
	srv := newTestServer(t)

	// Shut down executor so runs stay in whatever state we set them to.
	if srv.executor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		srv.executor.Shutdown(ctx)
		cancel()
		srv.executor = nil
	}

	tenantID := "tnt_cascade"

	// Create parent run.
	parent := types.Run{
		TenantID:  tenantID,
		RunID:     "run_parent",
		AgentID:   "agt_test",
		Status:    "running",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := srv.runs.Create(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Create child run linked via RetryOf.
	childRetry := types.Run{
		TenantID:  tenantID,
		RunID:     "run_child_retry",
		AgentID:   "agt_test",
		Status:    "running",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		RetryOf:   "run_parent",
	}
	if err := srv.runs.Create(context.Background(), childRetry); err != nil {
		t.Fatalf("create child retry: %v", err)
	}

	// Create child run linked via ParentRunID (delegation).
	childDelegated := types.Run{
		TenantID:    tenantID,
		RunID:       "run_child_delegated",
		AgentID:     "agt_test",
		Status:      "queued",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ParentRunID: "run_parent",
	}
	if err := srv.runs.Create(context.Background(), childDelegated); err != nil {
		t.Fatalf("create child delegated: %v", err)
	}

	// Create a child in terminal state (should NOT be canceled).
	childCompleted := types.Run{
		TenantID:    tenantID,
		RunID:       "run_child_completed",
		AgentID:     "agt_test",
		Status:      "completed",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ParentRunID: "run_parent",
	}
	if err := srv.runs.Create(context.Background(), childCompleted); err != nil {
		t.Fatalf("create child completed: %v", err)
	}

	// Cascade cancel.
	srv.cascadeCancelChildren(context.Background(), tenantID, "run_parent", 0)

	// Verify child retry was canceled.
	got, ok, err := srv.runs.Get(context.Background(), tenantID, "run_child_retry")
	if err != nil {
		t.Fatalf("get child retry: %v", err)
	}
	if !ok {
		t.Fatal("child retry not found")
	}
	if got.Status != "canceled" {
		t.Errorf("expected child retry status=canceled, got %s", got.Status)
	}

	// Verify child delegated was canceled.
	got, ok, err = srv.runs.Get(context.Background(), tenantID, "run_child_delegated")
	if err != nil {
		t.Fatalf("get child delegated: %v", err)
	}
	if !ok {
		t.Fatal("child delegated not found")
	}
	if got.Status != "canceled" {
		t.Errorf("expected child delegated status=canceled, got %s", got.Status)
	}

	// Verify completed child was NOT affected.
	got, ok, err = srv.runs.Get(context.Background(), tenantID, "run_child_completed")
	if err != nil {
		t.Fatalf("get child completed: %v", err)
	}
	if !ok {
		t.Fatal("child completed not found")
	}
	if got.Status != "completed" {
		t.Errorf("expected child completed status=completed, got %s", got.Status)
	}
}

// TestCascadeCancelRecursive verifies that cancellation cascades through
// multiple levels of child runs.
func TestCascadeCancelRecursive(t *testing.T) {
	srv := newTestServer(t)

	// Shut down executor.
	if srv.executor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		srv.executor.Shutdown(ctx)
		cancel()
		srv.executor = nil
	}

	tenantID := "tnt_cascade_rec"

	// Parent -> child -> grandchild
	parent := types.Run{
		TenantID:  tenantID,
		RunID:     "run_p",
		AgentID:   "agt_test",
		Status:    "running",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := srv.runs.Create(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	child := types.Run{
		TenantID:    tenantID,
		RunID:       "run_c",
		AgentID:     "agt_test",
		Status:      "running",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ParentRunID: "run_p",
	}
	if err := srv.runs.Create(context.Background(), child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	grandchild := types.Run{
		TenantID:    tenantID,
		RunID:       "run_gc",
		AgentID:     "agt_test",
		Status:      "queued",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ParentRunID: "run_c",
	}
	if err := srv.runs.Create(context.Background(), grandchild); err != nil {
		t.Fatalf("create grandchild: %v", err)
	}

	// Cascade from parent.
	srv.cascadeCancelChildren(context.Background(), tenantID, "run_p", 0)

	// Verify child canceled.
	got, _, err := srv.runs.Get(context.Background(), tenantID, "run_c")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.Status != "canceled" {
		t.Errorf("expected child status=canceled, got %s", got.Status)
	}

	// Verify grandchild canceled (recursive).
	got, _, err = srv.runs.Get(context.Background(), tenantID, "run_gc")
	if err != nil {
		t.Fatalf("get grandchild: %v", err)
	}
	if got.Status != "canceled" {
		t.Errorf("expected grandchild status=canceled, got %s", got.Status)
	}
}

// TestCascadeCancelDepthLimit verifies that recursion stops at the configured
// maximum delegation depth.
func TestCascadeCancelDepthLimit(t *testing.T) {
	t.Setenv("AGENTOS_MAX_DELEGATION_DEPTH", "1")

	srv := newTestServer(t)

	// Shut down executor.
	if srv.executor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		srv.executor.Shutdown(ctx)
		cancel()
		srv.executor = nil
	}

	tenantID := "tnt_cascade_depth"

	// Parent -> child -> grandchild (grandchild should NOT be canceled at depth 1)
	parent := types.Run{
		TenantID:  tenantID,
		RunID:     "run_dp",
		AgentID:   "agt_test",
		Status:    "running",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := srv.runs.Create(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	child := types.Run{
		TenantID:    tenantID,
		RunID:       "run_dc",
		AgentID:     "agt_test",
		Status:      "running",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ParentRunID: "run_dp",
	}
	if err := srv.runs.Create(context.Background(), child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	grandchild := types.Run{
		TenantID:    tenantID,
		RunID:       "run_dgc",
		AgentID:     "agt_test",
		Status:      "queued",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ParentRunID: "run_dc",
	}
	if err := srv.runs.Create(context.Background(), grandchild); err != nil {
		t.Fatalf("create grandchild: %v", err)
	}

	// Cascade with depth 0 -> finds child at depth 0 -> recurses at depth 1 -> hits limit.
	srv.cascadeCancelChildren(context.Background(), tenantID, "run_dp", 0)

	// Child should be canceled (found at depth 0).
	got, _, err := srv.runs.Get(context.Background(), tenantID, "run_dc")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.Status != "canceled" {
		t.Errorf("expected child status=canceled, got %s", got.Status)
	}

	// Grandchild should NOT be canceled (recursion stopped at depth 1).
	got, _, err = srv.runs.Get(context.Background(), tenantID, "run_dgc")
	if err != nil {
		t.Fatalf("get grandchild: %v", err)
	}
	if got.Status != "queued" {
		t.Errorf("expected grandchild status=queued (depth limit), got %s", got.Status)
	}
}

// TestCascadeCancelNoCrossTenanT verifies that cascade cancel only affects
// runs within the same tenant.
func TestCascadeCancelNoCrossTenant(t *testing.T) {
	srv := newTestServer(t)

	// Shut down executor.
	if srv.executor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		srv.executor.Shutdown(ctx)
		cancel()
		srv.executor = nil
	}

	// Create a child-like run in a different tenant with the same parent run ID.
	otherChild := types.Run{
		TenantID:    "tnt_other",
		RunID:       "run_other_child",
		AgentID:     "agt_test",
		Status:      "running",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ParentRunID: "run_parent",
	}
	if err := srv.runs.Create(context.Background(), otherChild); err != nil {
		t.Fatalf("create other child: %v", err)
	}

	// Cascade cancel for tnt_cascade tenant.
	srv.cascadeCancelChildren(context.Background(), "tnt_cascade", "run_parent", 0)

	// Other tenant's child should NOT be affected.
	got, ok, err := srv.runs.Get(context.Background(), "tnt_other", "run_other_child")
	if err != nil {
		t.Fatalf("get other child: %v", err)
	}
	if !ok {
		t.Fatal("other child not found")
	}
	if got.Status != "running" {
		t.Errorf("expected other child status=running (cross-tenant isolation), got %s", got.Status)
	}
}
