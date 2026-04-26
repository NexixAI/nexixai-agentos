package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- mock ClearanceStore ---

type mockClearanceStore struct {
	tiers map[string]mcp.ClearanceTier
	err   error
}

func (m *mockClearanceStore) GetClearance(_ context.Context, agentID string) (mcp.ClearanceTier, error) {
	if m.err != nil {
		return 0, m.err
	}
	tier, ok := m.tiers[agentID]
	if !ok {
		return 0, errors.New("agent not found")
	}
	return tier, nil
}

// --- mock AuditLogger ---

type mockAuditLogger struct {
	mu      sync.Mutex
	entries []DecisionEntry
	err     error
}

func (m *mockAuditLogger) LogDecision(_ context.Context, entry DecisionEntry) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockAuditLogger) lastEntry() (DecisionEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return DecisionEntry{}, false
	}
	return m.entries[len(m.entries)-1], true
}

// --- check_authorization tests ---

func TestCheckAuthorization_Allowed(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearanceAdmin},
	}
	logger := &mockAuditLogger{}

	// Register a tool to check against.
	if err := registry.Register(mcp.Tool{
		Name:         "target-tool",
		MinClearance: mcp.ClearanceInternal,
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("check_authorization")
	if tool == nil {
		t.Fatal("check_authorization not registered")
	}

	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(checkAuthInput{ToolName: "target-tool"})
	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(checkAuthOutput)
	if !ok {
		t.Fatalf("expected checkAuthOutput, got %T", result)
	}
	if !out.Authorized {
		t.Errorf("expected authorized=true, got false; reason: %s", out.Reason)
	}
}

func TestCheckAuthorization_Denied(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-low": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}

	if err := registry.Register(mcp.Tool{
		Name:         "admin-tool",
		MinClearance: mcp.ClearanceAdmin,
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("check_authorization")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-low",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(checkAuthInput{ToolName: "admin-tool"})
	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(checkAuthOutput)
	if !ok {
		t.Fatalf("expected checkAuthOutput, got %T", result)
	}
	if out.Authorized {
		t.Error("expected authorized=false, got true")
	}
}

func TestCheckAuthorization_ToolNotFound(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearanceAdmin},
	}
	logger := &mockAuditLogger{}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("check_authorization")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(checkAuthInput{ToolName: "nonexistent"})
	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(checkAuthOutput)
	if !ok {
		t.Fatalf("expected checkAuthOutput, got %T", result)
	}
	if out.Authorized {
		t.Error("expected authorized=false for missing tool")
	}
}

func TestCheckAuthorization_EmptyToolName(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearanceAdmin},
	}
	logger := &mockAuditLogger{}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("check_authorization")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(checkAuthInput{ToolName: ""})
	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for empty tool_name, got nil")
	}
}

// --- log_decision tests ---

func TestLogDecision_WritesToAuditLogger(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("log_decision")
	if tool == nil {
		t.Fatal("log_decision not registered")
	}

	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(logDecisionInput{
		Decision:  "approve deployment",
		Reasoning: "all checks passed",
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(logDecisionOutput)
	if !ok {
		t.Fatalf("expected logDecisionOutput, got %T", result)
	}
	if !out.Logged {
		t.Error("expected logged=true")
	}
	if out.ID == "" {
		t.Error("expected non-empty ID")
	}

	entry, ok := logger.lastEntry()
	if !ok {
		t.Fatal("no entries logged")
	}
	if entry.Decision != "approve deployment" {
		t.Errorf("decision = %q, want %q", entry.Decision, "approve deployment")
	}
	if entry.Reasoning != "all checks passed" {
		t.Errorf("reasoning = %q, want %q", entry.Reasoning, "all checks passed")
	}
	if entry.AgentID != "agent-1" {
		t.Errorf("agent_id = %q, want %q", entry.AgentID, "agent-1")
	}
	if entry.TenantID != "tenant-1" {
		t.Errorf("tenant_id = %q, want %q", entry.TenantID, "tenant-1")
	}
}

func TestLogDecision_MissingAuthContext(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{},
	}
	logger := &mockAuditLogger{}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("log_decision")

	// No auth context.
	params, _ := json.Marshal(logDecisionInput{
		Decision:  "some decision",
		Reasoning: "some reason",
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
}

func TestLogDecision_EmptyDecision(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("log_decision")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(logDecisionInput{
		Decision:  "",
		Reasoning: "some reason",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for empty decision, got nil")
	}
}

func TestLogDecision_EmptyReasoning(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("log_decision")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(logDecisionInput{
		Decision:  "some decision",
		Reasoning: "",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for empty reasoning, got nil")
	}
}

func TestLogDecision_AuditLoggerError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{err: errors.New("disk full")}

	if err := RegisterGovernanceTools(registry, store, logger); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("log_decision")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(logDecisionInput{
		Decision:  "some decision",
		Reasoning: "some reason",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error from audit logger, got nil")
	}
}

func TestNewUUID_Format(t *testing.T) {
	id, err := newUUID()
	if err != nil {
		t.Fatalf("newUUID() returned error: %v", err)
	}
	if len(id) != 36 {
		t.Errorf("UUID length = %d, want 36; got %q", len(id), id)
	}
	// Basic format check: 8-4-4-4-12 hex groups.
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Errorf("UUID format invalid: %q", id)
	}
}

// --- request_elevation tests ---

func TestRequestElevation_CreatesPendingRequest(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("request_elevation")
	if tool == nil {
		t.Fatal("request_elevation not registered")
	}

	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(requestElevationInput{
		Reason:        "need admin access for deployment review",
		RequestedTier: 3,
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(requestElevationOutput)
	if !ok {
		t.Fatalf("expected requestElevationOutput, got %T", result)
	}
	if out.RequestID == "" {
		t.Error("expected non-empty request_id")
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want %q", out.Status, "pending")
	}

	// Verify the request exists in the store.
	req, err := elevStore.GetElevationStatus(ctx, out.RequestID)
	if err != nil {
		t.Fatalf("GetElevationStatus() returned error: %v", err)
	}
	if req.Status != "pending" {
		t.Errorf("stored status = %q, want %q", req.Status, "pending")
	}
	if req.AgentID != "agent-1" {
		t.Errorf("stored agent_id = %q, want %q", req.AgentID, "agent-1")
	}
	if req.TenantID != "tenant-1" {
		t.Errorf("stored tenant_id = %q, want %q", req.TenantID, "tenant-1")
	}
}

func TestRequestElevation_MissingReason(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("request_elevation")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(requestElevationInput{
		Reason:        "",
		RequestedTier: 2,
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing reason, got nil")
	}
}

func TestRequestElevation_MissingTier(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("request_elevation")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	// JSON with no requested_tier field — Go will default to 0 (ClearancePublic),
	// which is valid. Test invalid tier explicitly.
	params, _ := json.Marshal(requestElevationInput{
		Reason:        "reason",
		RequestedTier: -1,
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for invalid tier, got nil")
	}
}

func TestRequestElevation_InvalidTier(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("request_elevation")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(requestElevationInput{
		Reason:        "reason",
		RequestedTier: 5,
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for tier 5, got nil")
	}
}

func TestRequestElevation_MissingAuthContext(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("request_elevation")

	params, _ := json.Marshal(requestElevationInput{
		Reason:        "need access",
		RequestedTier: 2,
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
}

// --- check_elevation_status tests ---

func TestCheckElevationStatus_PendingRequest(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	// Create a request first.
	reqTool := registry.Get("request_elevation")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	reqParams, _ := json.Marshal(requestElevationInput{
		Reason:        "need access",
		RequestedTier: 2,
	})
	reqResult, err := reqTool.Handler(ctx, reqParams)
	if err != nil {
		t.Fatalf("request_elevation error: %v", err)
	}
	reqOut := reqResult.(requestElevationOutput)

	// Now check status.
	statusTool := registry.Get("check_elevation_status")
	if statusTool == nil {
		t.Fatal("check_elevation_status not registered")
	}

	statusParams, _ := json.Marshal(checkElevationStatusInput{
		RequestID: reqOut.RequestID,
	})
	statusResult, err := statusTool.Handler(ctx, statusParams)
	if err != nil {
		t.Fatalf("check_elevation_status error: %v", err)
	}

	statusOut, ok := statusResult.(checkElevationStatusOutput)
	if !ok {
		t.Fatalf("expected checkElevationStatusOutput, got %T", statusResult)
	}
	if statusOut.Status != "pending" {
		t.Errorf("status = %q, want %q", statusOut.Status, "pending")
	}
	if statusOut.RequestID != reqOut.RequestID {
		t.Errorf("request_id = %q, want %q", statusOut.RequestID, reqOut.RequestID)
	}
	if statusOut.ApprovedTier != nil {
		t.Error("approved_tier should be nil for pending request")
	}
	if statusOut.ExpiresAt != nil {
		t.Error("expires_at should be nil for pending request")
	}
}

func TestCheckElevationStatus_EmptyRequestID(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("check_elevation_status")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(checkElevationStatusInput{RequestID: ""})
	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for empty request_id, got nil")
	}
}

func TestCheckElevationStatus_NotFound(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{"agent-1": mcp.ClearancePublic},
	}
	logger := &mockAuditLogger{}
	elevStore := postgres.NewInMemoryElevationStore()

	if err := RegisterGovernanceTools(registry, store, logger, elevStore); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("check_elevation_status")
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})

	params, _ := json.Marshal(checkElevationStatusInput{RequestID: "nonexistent-id"})
	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for nonexistent request, got nil")
	}
}

// --- clearance tier tests ---

func TestClearanceTierValues(t *testing.T) {
	tests := []struct {
		tier mcp.ClearanceTier
		want int
	}{
		{mcp.ClearancePublic, 0},
		{mcp.ClearanceInternal, 1},
		{mcp.ClearanceExecute, 2},
		{mcp.ClearanceAdmin, 3},
		{mcp.ClearanceSuperAdmin, 4},
	}
	for _, tt := range tests {
		if int(tt.tier) != tt.want {
			t.Errorf("ClearanceTier %d != expected %d", tt.tier, tt.want)
		}
	}
}

func TestClearanceTierOrdering(t *testing.T) {
	if mcp.ClearancePublic >= mcp.ClearanceInternal {
		t.Error("ClearancePublic should be < ClearanceInternal")
	}
	if mcp.ClearanceInternal >= mcp.ClearanceExecute {
		t.Error("ClearanceInternal should be < ClearanceExecute")
	}
	if mcp.ClearanceExecute >= mcp.ClearanceAdmin {
		t.Error("ClearanceExecute should be < ClearanceAdmin")
	}
	if mcp.ClearanceAdmin >= mcp.ClearanceSuperAdmin {
		t.Error("ClearanceAdmin should be < ClearanceSuperAdmin")
	}
}
