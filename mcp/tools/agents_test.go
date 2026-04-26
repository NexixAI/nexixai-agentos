package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/agents"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- mock AgentManager ---

type mockAgentManager struct {
	mu       sync.Mutex
	agents   map[string]*agents.Agent
	children map[string][]string // parentID -> childIDs
	err      error               // inject errors
	spawnCount int
}

func newMockAgentManager() *mockAgentManager {
	return &mockAgentManager{
		agents:   make(map[string]*agents.Agent),
		children: make(map[string][]string),
	}
}

func (m *mockAgentManager) seedAgent(a *agents.Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[a.ID] = a
}

func (m *mockAgentManager) SpawnAgent(_ context.Context, parentID, tenantID, directive string, tools []string) (*agents.Agent, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	parent, ok := m.agents[parentID]
	if !ok {
		return nil, fmt.Errorf("agents: parent %q not found", parentID)
	}
	if parent.TenantID != tenantID {
		return nil, fmt.Errorf("agents: tenant mismatch")
	}

	m.spawnCount++
	now := time.Now().UTC()
	agent := &agents.Agent{
		ID:           fmt.Sprintf("child-%d", m.spawnCount),
		ParentID:     parentID,
		TenantID:     tenantID,
		Directive:    directive,
		AllowedTools: tools,
		Status:       agents.AgentStatusCreated,
		Messages:     []agents.Message{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.agents[agent.ID] = agent
	m.children[parentID] = append(m.children[parentID], agent.ID)
	return agent, nil
}

func (m *mockAgentManager) SendMessage(_ context.Context, fromID, toID, message string) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	from, ok := m.agents[fromID]
	if !ok {
		return fmt.Errorf("agents: sender %q not found", fromID)
	}
	to, ok := m.agents[toID]
	if !ok {
		return fmt.Errorf("agents: recipient %q not found", toID)
	}
	if from.TenantID != to.TenantID {
		return fmt.Errorf("agents: cross-tenant messaging denied")
	}

	now := time.Now().UTC()
	msg := agents.Message{
		ID:        fmt.Sprintf("msg-%d", len(to.Messages)+1),
		FromID:    fromID,
		ToID:      toID,
		Content:   message,
		CreatedAt: now,
	}
	to.Messages = append(to.Messages, msg)
	return nil
}

func (m *mockAgentManager) GetStatus(_ context.Context, agentID string) (*agents.Agent, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[agentID]
	if !ok {
		return nil, fmt.Errorf("agents: agent %q not found", agentID)
	}
	cp := *agent
	cp.Messages = make([]agents.Message, len(agent.Messages))
	copy(cp.Messages, agent.Messages)
	cp.AllowedTools = make([]string, len(agent.AllowedTools))
	copy(cp.AllowedTools, agent.AllowedTools)
	return &cp, nil
}

// --- agent tool auth context helper ---

func agentAuthCtx(agentID, tenantID string) context.Context {
	return mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  agentID,
		TenantID: tenantID,
	})
}

// --- spawn_agent tests ---

func TestSpawnAgent_Success(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	// Seed a parent agent.
	mgr.seedAgent(&agents.Agent{
		ID:        "parent-1",
		TenantID:  "tenant-1",
		Status:    agents.AgentStatusRunning,
		Messages:  []agents.Message{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("spawn_agent")
	if tool == nil {
		t.Fatal("spawn_agent not registered")
	}

	ctx := agentAuthCtx("parent-1", "tenant-1")
	params, _ := json.Marshal(spawnAgentInput{
		Directive:    "analyze logs",
		AllowedTools: []string{"memory_store"},
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(spawnAgentOutput)
	if !ok {
		t.Fatalf("expected spawnAgentOutput, got %T", result)
	}
	if out.AgentID == "" {
		t.Error("expected non-empty agent_id")
	}
	if out.ParentID != "parent-1" {
		t.Errorf("parent_id = %q, want %q", out.ParentID, "parent-1")
	}
	if out.TenantID != "tenant-1" {
		t.Errorf("tenant_id = %q, want %q", out.TenantID, "tenant-1")
	}
	if out.Status != "created" {
		t.Errorf("status = %q, want %q", out.Status, "created")
	}
	if out.Directive != "analyze logs" {
		t.Errorf("directive = %q, want %q", out.Directive, "analyze logs")
	}
}

func TestSpawnAgent_MissingDirective_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("spawn_agent")
	ctx := agentAuthCtx("parent-1", "tenant-1")

	params, _ := json.Marshal(spawnAgentInput{Directive: ""})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing directive, got nil")
	}
}

func TestSpawnAgent_MissingAuth_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("spawn_agent")

	params, _ := json.Marshal(spawnAgentInput{Directive: "do work"})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth, got nil")
	}
}

func TestSpawnAgent_ManagerError_Propagates(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()
	mgr.err = fmt.Errorf("database exploded")

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("spawn_agent")
	ctx := agentAuthCtx("parent-1", "tenant-1")

	params, _ := json.Marshal(spawnAgentInput{Directive: "do work"})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error from manager, got nil")
	}
}

// --- message_agent tests ---

func TestMessageAgent_Success(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	mgr.seedAgent(&agents.Agent{
		ID:        "sender-1",
		TenantID:  "tenant-1",
		Status:    agents.AgentStatusRunning,
		Messages:  []agents.Message{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	mgr.seedAgent(&agents.Agent{
		ID:        "receiver-1",
		TenantID:  "tenant-1",
		Status:    agents.AgentStatusRunning,
		Messages:  []agents.Message{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("message_agent")
	if tool == nil {
		t.Fatal("message_agent not registered")
	}

	ctx := agentAuthCtx("sender-1", "tenant-1")
	params, _ := json.Marshal(messageAgentInput{
		AgentID: "receiver-1",
		Message: "hello receiver",
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(messageAgentOutput)
	if !ok {
		t.Fatalf("expected messageAgentOutput, got %T", result)
	}
	if !out.Delivered {
		t.Error("expected delivered=true")
	}
	if out.ToAgentID != "receiver-1" {
		t.Errorf("to_agent_id = %q, want %q", out.ToAgentID, "receiver-1")
	}
}

func TestMessageAgent_MissingAgentID_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("message_agent")
	ctx := agentAuthCtx("sender-1", "tenant-1")

	params, _ := json.Marshal(messageAgentInput{AgentID: "", Message: "hello"})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing agent_id, got nil")
	}
}

func TestMessageAgent_MissingMessage_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("message_agent")
	ctx := agentAuthCtx("sender-1", "tenant-1")

	params, _ := json.Marshal(messageAgentInput{AgentID: "receiver-1", Message: ""})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing message, got nil")
	}
}

func TestMessageAgent_MissingAuth_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("message_agent")

	params, _ := json.Marshal(messageAgentInput{AgentID: "receiver-1", Message: "hello"})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth, got nil")
	}
}

// --- get_agent_status tests ---

func TestGetAgentStatus_Success(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	now := time.Now().UTC()
	mgr.seedAgent(&agents.Agent{
		ID:           "agent-1",
		ParentID:     "parent-0",
		TenantID:     "tenant-1",
		Directive:    "monitor",
		AllowedTools: []string{"health_check"},
		Status:       agents.AgentStatusRunning,
		Messages:     []agents.Message{},
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_agent_status")
	if tool == nil {
		t.Fatal("get_agent_status not registered")
	}

	ctx := agentAuthCtx("caller-1", "tenant-1")
	params, _ := json.Marshal(getAgentStatusInput{AgentID: "agent-1"})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(getAgentStatusOutput)
	if !ok {
		t.Fatalf("expected getAgentStatusOutput, got %T", result)
	}
	if out.AgentID != "agent-1" {
		t.Errorf("agent_id = %q, want %q", out.AgentID, "agent-1")
	}
	if out.Status != "running" {
		t.Errorf("status = %q, want %q", out.Status, "running")
	}
	if out.Directive != "monitor" {
		t.Errorf("directive = %q, want %q", out.Directive, "monitor")
	}
	if len(out.AllowedTools) != 1 || out.AllowedTools[0] != "health_check" {
		t.Errorf("allowed_tools = %v, want [health_check]", out.AllowedTools)
	}
	if out.CreatedAt == "" {
		t.Error("expected non-empty created_at")
	}
}

func TestGetAgentStatus_MissingAgentID_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_agent_status")
	ctx := agentAuthCtx("caller-1", "tenant-1")

	params, _ := json.Marshal(getAgentStatusInput{AgentID: ""})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing agent_id, got nil")
	}
}

func TestGetAgentStatus_MissingAuth_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_agent_status")

	params, _ := json.Marshal(getAgentStatusInput{AgentID: "agent-1"})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth, got nil")
	}
}

func TestGetAgentStatus_NotFound_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_agent_status")
	ctx := agentAuthCtx("caller-1", "tenant-1")

	params, _ := json.Marshal(getAgentStatusInput{AgentID: "nonexistent"})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for nonexistent agent, got nil")
	}
}

// --- clearance tier tests ---

func TestAgentTools_ClearanceTiers(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		toolName string
		want     mcp.ClearanceTier
	}{
		{"spawn_agent requires Execute", "spawn_agent", mcp.ClearanceExecute},
		{"message_agent requires Execute", "message_agent", mcp.ClearanceExecute},
		{"get_agent_status requires Internal", "get_agent_status", mcp.ClearanceInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool := registry.Get(tc.toolName)
			if tool == nil {
				t.Fatalf("%s not registered", tc.toolName)
			}
			if tool.MinClearance != tc.want {
				t.Errorf("MinClearance = %d, want %d", tool.MinClearance, tc.want)
			}
		})
	}
}

func TestAgentTools_InvalidJSON_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	mgr := newMockAgentManager()

	if err := RegisterAgentTools(registry, mgr); err != nil {
		t.Fatal(err)
	}

	ctx := agentAuthCtx("parent-1", "tenant-1")
	badJSON := json.RawMessage(`{invalid json`)

	toolNames := []string{"spawn_agent", "message_agent", "get_agent_status"}
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			tool := registry.Get(name)
			_, err := tool.Handler(ctx, badJSON)
			if err == nil {
				t.Fatalf("%s: expected error for invalid JSON, got nil", name)
			}
		})
	}
}
