package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// helper: register a set of fake tools at various clearance tiers.
func seedRegistry(t *testing.T) *mcp.ToolRegistry {
	t.Helper()
	reg := mcp.NewToolRegistry()
	tiers := []struct {
		name string
		tier mcp.ClearanceTier
	}{
		{"get_health", mcp.ClearancePublic},     // T0
		{"list_models", mcp.ClearancePublic},     // T0
		{"chat_completion", mcp.ClearanceInternal}, // T1
		{"execute_code", mcp.ClearanceExecute},     // T2
		{"query_metrics", mcp.ClearanceAdmin},      // T3
	}
	for _, tt := range tiers {
		name := tt.name // capture
		if err := reg.Register(mcp.Tool{
			Name:         name,
			Description:  "test tool " + name,
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			MinClearance: tt.tier,
			Static:       true,
			Handler: func(_ context.Context, params json.RawMessage) (any, error) {
				return map[string]string{"invoked": name}, nil
			},
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return reg
}

func ctxWithAuth(agentID string) context.Context {
	return mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  agentID,
		TenantID: "tenant-1",
	})
}

// --- discover_tools tests ---

func TestDiscoverTools_FiltersByClearance(t *testing.T) {
	reg := seedRegistry(t)
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{
			"agent-t1": mcp.ClearanceInternal, // T1 — should see T0 + T1
		},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tool := reg.Get("discover_tools")
	if tool == nil {
		t.Fatal("discover_tools not found")
	}

	result, err := tool.Handler(ctxWithAuth("agent-t1"), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(discoverToolsResponse)
	if !ok {
		t.Fatalf("expected discoverToolsResponse, got %T", result)
	}

	// T1 agent should see: get_health (T0), list_models (T0), chat_completion (T1),
	// plus the 4 dynamic tools: discover_tools (T0), get_tool_schema (T0),
	// invoke_tool (T1), list_capabilities (T0).
	// Should NOT see: execute_code (T2), query_metrics (T3).
	visible := make(map[string]bool)
	for _, dt := range resp.Tools {
		visible[dt.Name] = true
	}

	mustSee := []string{
		"get_health", "list_models", "chat_completion",
		"discover_tools", "get_tool_schema", "invoke_tool", "list_capabilities",
	}
	for _, name := range mustSee {
		if !visible[name] {
			t.Errorf("expected %q to be visible for T1 agent", name)
		}
	}

	mustNotSee := []string{"execute_code", "query_metrics"}
	for _, name := range mustNotSee {
		if visible[name] {
			t.Errorf("expected %q to be hidden for T1 agent", name)
		}
	}

	if resp.Count != len(resp.Tools) {
		t.Errorf("count %d != len(tools) %d", resp.Count, len(resp.Tools))
	}
}

func TestDiscoverTools_MissingAuthContext(t *testing.T) {
	reg := seedRegistry(t)
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tool := reg.Get("discover_tools")
	// Call without auth context.
	_, err := tool.Handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
	if !strings.Contains(err.Error(), "missing auth context") {
		t.Errorf("expected 'missing auth context' in error, got: %v", err)
	}
}

// --- get_tool_schema tests ---

func TestGetToolSchema_ReturnsCorrectSchema(t *testing.T) {
	reg := seedRegistry(t)
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tool := reg.Get("get_tool_schema")
	if tool == nil {
		t.Fatal("get_tool_schema not found")
	}

	params, err := json.Marshal(getToolSchemaInput{ToolName: "get_health"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(getToolSchemaResponse)
	if !ok {
		t.Fatalf("expected getToolSchemaResponse, got %T", result)
	}

	if resp.Name != "get_health" {
		t.Errorf("expected name %q, got %q", "get_health", resp.Name)
	}
	if resp.Description == "" {
		t.Error("expected non-empty description")
	}
	if len(resp.InputSchema) == 0 {
		t.Error("expected non-empty input_schema")
	}

	// Verify the schema is valid JSON.
	var schema map[string]any
	if err := json.Unmarshal(resp.InputSchema, &schema); err != nil {
		t.Fatalf("invalid InputSchema JSON: %v", err)
	}
}

func TestGetToolSchema_UnknownToolReturnsError(t *testing.T) {
	reg := seedRegistry(t)
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tool := reg.Get("get_tool_schema")
	params, err := json.Marshal(getToolSchemaInput{ToolName: "nonexistent_tool"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	_, err = tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// --- invoke_tool tests ---

func TestInvokeTool_DispatchesToCorrectHandler(t *testing.T) {
	reg := seedRegistry(t)
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{
			"agent-t1": mcp.ClearanceInternal,
		},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tool := reg.Get("invoke_tool")
	if tool == nil {
		t.Fatal("invoke_tool not found")
	}

	params, err := json.Marshal(invokeToolInput{
		ToolName: "get_health",
		Params:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	result, err := tool.Handler(ctxWithAuth("agent-t1"), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// The seeded get_health handler returns {"invoked": "get_health"}.
	m, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", result)
	}
	if m["invoked"] != "get_health" {
		t.Errorf("expected invoked=%q, got %q", "get_health", m["invoked"])
	}
}

func TestInvokeTool_InsufficientClearanceForTarget(t *testing.T) {
	reg := seedRegistry(t)
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{
			"agent-t1": mcp.ClearanceInternal, // T1
		},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tool := reg.Get("invoke_tool")
	// Try to invoke execute_code (T2) — agent is T1, should be denied.
	params, err := json.Marshal(invokeToolInput{
		ToolName: "execute_code",
		Params:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	_, err = tool.Handler(ctxWithAuth("agent-t1"), params)
	if err == nil {
		t.Fatal("expected error for insufficient clearance, got nil")
	}
	if !strings.Contains(err.Error(), "insufficient clearance") {
		t.Errorf("expected 'insufficient clearance' in error, got: %v", err)
	}
}

// --- list_capabilities tests ---

func TestListCapabilities_ReturnsCategorySummary(t *testing.T) {
	reg := seedRegistry(t)
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tool := reg.Get("list_capabilities")
	if tool == nil {
		t.Fatal("list_capabilities not found")
	}

	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(listCapabilitiesResponse)
	if !ok {
		t.Fatalf("expected listCapabilitiesResponse, got %T", result)
	}

	// Should have entries for all known categories.
	catMap := make(map[string]capabilityCategory)
	for _, c := range resp.Categories {
		catMap[c.Category] = c
	}

	for _, cat := range knownCategories {
		if _, ok := catMap[cat]; !ok {
			t.Errorf("missing category %q in response", cat)
		}
	}

	// Seeded tools + dynamic tools: health has get_health, chat has list_models + chat_completion,
	// sandbox has execute_code, observe has query_metrics, dynamic has the 4 dynamic tools.
	if catMap["health"].Count < 1 {
		t.Errorf("expected health count >= 1, got %d", catMap["health"].Count)
	}
	if !catMap["health"].Active {
		t.Error("expected health to be active")
	}
	if catMap["dynamic"].Count != 4 {
		t.Errorf("expected dynamic count = 4, got %d", catMap["dynamic"].Count)
	}
	if !catMap["dynamic"].Active {
		t.Error("expected dynamic to be active")
	}

	// Categories with no seeded tools should be inactive.
	for _, cat := range []string{"governance", "http", "memory", "knowledge", "facets"} {
		if catMap[cat].Active {
			t.Errorf("expected %q to be inactive (no tools seeded), got active with count %d", cat, catMap[cat].Count)
		}
	}
}

// --- clearance tier correctness ---

func TestDynamicTools_ClearanceTiers(t *testing.T) {
	reg := mcp.NewToolRegistry()
	store := &mockClearanceStore{
		tiers: map[string]mcp.ClearanceTier{},
	}

	if err := RegisterDynamicTools(reg, store); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	tests := []struct {
		name string
		want mcp.ClearanceTier
	}{
		{"discover_tools", mcp.ClearancePublic},
		{"get_tool_schema", mcp.ClearancePublic},
		{"invoke_tool", mcp.ClearanceInternal},
		{"list_capabilities", mcp.ClearancePublic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := reg.Get(tt.name)
			if tool == nil {
				t.Fatalf("tool %q not found", tt.name)
			}
			if tool.MinClearance != tt.want {
				t.Errorf("MinClearance = %d, want %d", tool.MinClearance, tt.want)
			}
		})
	}
}
