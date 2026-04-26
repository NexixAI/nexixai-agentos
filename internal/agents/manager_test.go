package agents

import (
	"context"
	"strings"
	"testing"
	"time"
)

// helper: create a manager with a seeded root agent.
func setupManager(t *testing.T, maxConcurrent int) (*InMemoryAgentManager, *Agent) {
	t.Helper()
	mgr := NewInMemoryAgentManager(maxConcurrent)
	root := &Agent{
		ID:           "root-agent",
		ParentID:     "",
		TenantID:     "tenant-1",
		Directive:    "root",
		AllowedTools: []string{},
		Status:       AgentStatusRunning,
		Messages:     []Message{},
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	mgr.SeedAgent(root)
	return mgr, root
}

func TestSpawnAgent_CreatesWithCorrectFields(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	tools := []string{"memory_store", "memory_recall"}
	child, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "analyze data", tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if child.ID == "" {
		t.Error("expected non-empty ID")
	}
	if !strings.HasPrefix(child.ID, "agent_") {
		t.Errorf("ID %q should have agent_ prefix", child.ID)
	}
	if child.ParentID != root.ID {
		t.Errorf("ParentID = %q, want %q", child.ParentID, root.ID)
	}
	if child.TenantID != root.TenantID {
		t.Errorf("TenantID = %q, want %q", child.TenantID, root.TenantID)
	}
	if child.Directive != "analyze data" {
		t.Errorf("Directive = %q, want %q", child.Directive, "analyze data")
	}
	if len(child.AllowedTools) != 2 {
		t.Errorf("AllowedTools length = %d, want 2", len(child.AllowedTools))
	}
	if child.Status != AgentStatusCreated {
		t.Errorf("Status = %q, want %q", child.Status, AgentStatusCreated)
	}
	if child.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if child.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}
}

func TestSpawnAgent_InheritsTenantFromParent(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	child, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "do work", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if child.TenantID != root.TenantID {
		t.Errorf("child TenantID = %q, want parent's %q", child.TenantID, root.TenantID)
	}
}

func TestSpawnAgent_TenantMismatch_ReturnsError(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	_, err := mgr.SpawnAgent(ctx, root.ID, "other-tenant", "do work", nil)
	if err == nil {
		t.Fatal("expected error for tenant mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "tenant mismatch") {
		t.Errorf("error = %q, want to contain 'tenant mismatch'", err.Error())
	}
}

func TestSpawnAgent_EnforcesMaxConcurrentLimit(t *testing.T) {
	maxAgents := 3
	mgr, root := setupManager(t, maxAgents)
	ctx := context.Background()

	// Spawn up to the limit.
	for i := 0; i < maxAgents; i++ {
		_, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "task", nil)
		if err != nil {
			t.Fatalf("spawn %d: unexpected error: %v", i, err)
		}
	}

	// Next spawn should fail.
	_, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "one too many", nil)
	if err == nil {
		t.Fatal("expected error for exceeding max concurrent agents, got nil")
	}
	if !strings.Contains(err.Error(), "max concurrent") {
		t.Errorf("error = %q, want to contain 'max concurrent'", err.Error())
	}
}

func TestSpawnAgent_CompletedChildrenDontCountTowardLimit(t *testing.T) {
	maxAgents := 2
	mgr, root := setupManager(t, maxAgents)
	ctx := context.Background()

	// Spawn and complete one child.
	child1, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "first", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Mark as completed directly (simulating lifecycle transition).
	mgr.mu.Lock()
	mgr.agents[child1.ID].Status = AgentStatusCompleted
	mgr.mu.Unlock()

	// Spawn two more — first shouldn't count since it's completed.
	_, err = mgr.SpawnAgent(ctx, root.ID, root.TenantID, "second", nil)
	if err != nil {
		t.Fatalf("second spawn should succeed: %v", err)
	}
	_, err = mgr.SpawnAgent(ctx, root.ID, root.TenantID, "third", nil)
	if err != nil {
		t.Fatalf("third spawn should succeed (first completed): %v", err)
	}
}

func TestSpawnAgent_ParentNotFound_ReturnsError(t *testing.T) {
	mgr := NewInMemoryAgentManager(0)
	ctx := context.Background()

	_, err := mgr.SpawnAgent(ctx, "nonexistent", "tenant-1", "do work", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent parent, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain 'not found'", err.Error())
	}
}

func TestSpawnAgent_FailedParent_ReturnsError(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	// Mark parent as failed.
	mgr.mu.Lock()
	mgr.agents[root.ID].Status = AgentStatusFailed
	mgr.mu.Unlock()

	_, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "do work", nil)
	if err == nil {
		t.Fatal("expected error for failed parent, got nil")
	}
	if !strings.Contains(err.Error(), "failed parent") {
		t.Errorf("error = %q, want to contain 'failed parent'", err.Error())
	}
}

func TestSpawnAgent_EmptyDirective_ReturnsError(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	_, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "", nil)
	if err == nil {
		t.Fatal("expected error for empty directive, got nil")
	}
}

func TestSendMessage_DeliversToTarget(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	child, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "receiver", nil)
	if err != nil {
		t.Fatal(err)
	}

	err = mgr.SendMessage(ctx, root.ID, child.ID, "hello child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify message was delivered.
	status, err := mgr.GetStatus(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(status.Messages))
	}
	msg := status.Messages[0]
	if msg.FromID != root.ID {
		t.Errorf("FromID = %q, want %q", msg.FromID, root.ID)
	}
	if msg.ToID != child.ID {
		t.Errorf("ToID = %q, want %q", msg.ToID, child.ID)
	}
	if msg.Content != "hello child" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello child")
	}
	if msg.ID == "" {
		t.Error("expected non-empty message ID")
	}
	if msg.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestSendMessage_CrossTenant_Denied(t *testing.T) {
	mgr, _ := setupManager(t, 0)
	ctx := context.Background()

	// Seed an agent in a different tenant.
	other := &Agent{
		ID:        "other-agent",
		TenantID:  "tenant-2",
		Status:    AgentStatusRunning,
		Messages:  []Message{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	mgr.SeedAgent(other)

	err := mgr.SendMessage(ctx, "root-agent", "other-agent", "cross-tenant msg")
	if err == nil {
		t.Fatal("expected error for cross-tenant messaging, got nil")
	}
	if !strings.Contains(err.Error(), "cross-tenant") {
		t.Errorf("error = %q, want to contain 'cross-tenant'", err.Error())
	}
}

func TestSendMessage_RecipientNotFound_ReturnsError(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	err := mgr.SendMessage(ctx, root.ID, "nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for nonexistent recipient, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain 'not found'", err.Error())
	}
}

func TestSendMessage_EmptyMessage_ReturnsError(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	child, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "receiver", nil)
	if err != nil {
		t.Fatal(err)
	}

	err = mgr.SendMessage(ctx, root.ID, child.ID, "")
	if err == nil {
		t.Fatal("expected error for empty message, got nil")
	}
}

func TestGetStatus_ReturnsCorrectState(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	child, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "worker", []string{"tool_a"})
	if err != nil {
		t.Fatal(err)
	}

	status, err := mgr.GetStatus(ctx, child.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.ID != child.ID {
		t.Errorf("ID = %q, want %q", status.ID, child.ID)
	}
	if status.Status != AgentStatusCreated {
		t.Errorf("Status = %q, want %q", status.Status, AgentStatusCreated)
	}
	if status.Directive != "worker" {
		t.Errorf("Directive = %q, want %q", status.Directive, "worker")
	}
	if len(status.AllowedTools) != 1 || status.AllowedTools[0] != "tool_a" {
		t.Errorf("AllowedTools = %v, want [tool_a]", status.AllowedTools)
	}
}

func TestGetStatus_ReturnsCopy(t *testing.T) {
	mgr, root := setupManager(t, 0)
	ctx := context.Background()

	child, err := mgr.SpawnAgent(ctx, root.ID, root.TenantID, "worker", []string{"tool_a"})
	if err != nil {
		t.Fatal(err)
	}

	status, err := mgr.GetStatus(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the returned copy.
	status.Messages = append(status.Messages, Message{Content: "rogue"})
	status.AllowedTools = append(status.AllowedTools, "rogue_tool")

	// Original should be unchanged.
	original, err := mgr.GetStatus(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Messages) != 0 {
		t.Errorf("original messages mutated: len=%d, want 0", len(original.Messages))
	}
	if len(original.AllowedTools) != 1 {
		t.Errorf("original tools mutated: len=%d, want 1", len(original.AllowedTools))
	}
}

func TestGetStatus_NotFound_ReturnsError(t *testing.T) {
	mgr := NewInMemoryAgentManager(0)
	ctx := context.Background()

	_, err := mgr.GetStatus(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain 'not found'", err.Error())
	}
}

func TestGetStatus_EmptyID_ReturnsError(t *testing.T) {
	mgr := NewInMemoryAgentManager(0)
	ctx := context.Background()

	_, err := mgr.GetStatus(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty agent_id, got nil")
	}
}

func TestTenantIsolation_SpawnedAgentsInheritTenant(t *testing.T) {
	mgr := NewInMemoryAgentManager(0)
	ctx := context.Background()

	// Create two root agents in different tenants.
	root1 := &Agent{
		ID:        "root-t1",
		TenantID:  "tenant-1",
		Status:    AgentStatusRunning,
		Messages:  []Message{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	root2 := &Agent{
		ID:        "root-t2",
		TenantID:  "tenant-2",
		Status:    AgentStatusRunning,
		Messages:  []Message{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	mgr.SeedAgent(root1)
	mgr.SeedAgent(root2)

	child1, err := mgr.SpawnAgent(ctx, root1.ID, root1.TenantID, "t1 work", nil)
	if err != nil {
		t.Fatal(err)
	}
	child2, err := mgr.SpawnAgent(ctx, root2.ID, root2.TenantID, "t2 work", nil)
	if err != nil {
		t.Fatal(err)
	}

	if child1.TenantID != "tenant-1" {
		t.Errorf("child1 TenantID = %q, want tenant-1", child1.TenantID)
	}
	if child2.TenantID != "tenant-2" {
		t.Errorf("child2 TenantID = %q, want tenant-2", child2.TenantID)
	}

	// Cross-tenant messaging should be denied.
	err = mgr.SendMessage(ctx, child1.ID, child2.ID, "cross-tenant")
	if err == nil {
		t.Fatal("expected error for cross-tenant message, got nil")
	}
}

func TestDefaultMaxConcurrentAgents(t *testing.T) {
	mgr := NewInMemoryAgentManager(0)
	if mgr.maxConcurrentPerParent != DefaultMaxConcurrentAgents {
		t.Errorf("default max = %d, want %d", mgr.maxConcurrentPerParent, DefaultMaxConcurrentAgents)
	}
}
