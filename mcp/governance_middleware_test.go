package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/governance"
)

// testGovernanceConfig returns a minimal GovernanceConfig for middleware tests.
func testGovernanceConfig() *governance.GovernanceConfig {
	return &governance.GovernanceConfig{
		Version:   "1.0",
		Framework: "ATF-aligned",
		Agents: map[string]governance.AgentPolicy{
			"sre-agent": {
				Role:      "operator",
				Clearance: "execute",
				Auth:      "api_key",
			},
		},
		Behavior: governance.BehaviorConfig{
			RateLimits: map[string]governance.RateLimit{
				"sre-agent": {PerHour: 100, PerDay: 1000},
			},
			LoopDetection: governance.LoopDetectionConfig{
				MaxAttemptsPerTarget: 5,
				Window:               "1h",
				OnTrigger:            "deny_and_escalate",
			},
			CircuitBreaker: governance.CircuitBreakerConfig{
				ConsecutiveFailures: 3,
				Action:              "freeze_agent",
				Notify:              "human",
				AutoUnfreeze:        false,
			},
		},
		Segments: map[string]governance.SegmentationEntry{
			"sre-agent": {
				AllowedActions: []string{
					"restart_container",
					"get_health",
					"query_metrics",
				},
				DeniedActions: []string{
					"delete_stack",
					"execute_code",
				},
				Scope: "all",
			},
		},
	}
}

// setupMiddleware creates a governance engine and middleware for testing.
func setupMiddleware(t *testing.T) (func(handler ToolHandler) ToolHandler, *governance.Engine) {
	t.Helper()
	engine := governance.NewEngineFromConfig(testGovernanceConfig())
	mw := GovernanceMiddleware(engine)
	return mw, engine
}

// govTestHandler is a ToolHandler that returns a fixed result.
func govTestHandler(_ context.Context, _ json.RawMessage) (any, error) {
	return map[string]string{"status": "ok"}, nil
}

// govTestFailingHandler is a ToolHandler that returns an error.
func govTestFailingHandler(_ context.Context, _ json.RawMessage) (any, error) {
	return nil, &json.SyntaxError{}
}

func TestGovernanceMiddleware_Allowed(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "get_health")

	result, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	m, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", result)
	}
	if m["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", m["status"])
	}
}

func TestGovernanceMiddleware_Denied(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "delete_stack") // explicitly denied

	result, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for denied tool, got result: %v", result)
	}
	if !strings.Contains(err.Error(), "governance denied") {
		t.Errorf("expected governance denied error, got: %v", err)
	}
}

func TestGovernanceMiddleware_DeniedReason(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "execute_code") // explicitly denied in denied_actions

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error for denied tool")
	}
	// The denial reason should mention the tool name.
	if !strings.Contains(err.Error(), "execute_code") {
		t.Errorf("expected denial reason to mention tool name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "governance denied") {
		t.Errorf("expected 'governance denied' prefix, got: %v", err)
	}
}

func TestGovernanceMiddleware_UnknownAgent(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "rogue-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "get_health")

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(err.Error(), "governance denied") {
		t.Errorf("expected governance denied error, got: %v", err)
	}
}

func TestGovernanceMiddleware_MissingAuthContext(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithGovernanceToolName(ctx, "get_health")
	// No MCPAuthContext set.

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error for missing auth context")
	}
	if !strings.Contains(err.Error(), "missing auth context") {
		t.Errorf("expected 'missing auth context' error, got: %v", err)
	}
}

func TestGovernanceMiddleware_EmptyAgentID(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "get_health")

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error for empty agent ID")
	}
	if !strings.Contains(err.Error(), "empty agent ID") {
		t.Errorf("expected 'empty agent ID' error, got: %v", err)
	}
}

func TestGovernanceMiddleware_MissingToolName(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	// No tool name set.

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error for missing tool name")
	}
	if !strings.Contains(err.Error(), "missing tool name") {
		t.Errorf("expected 'missing tool name' error, got: %v", err)
	}
}

func TestGovernanceMiddleware_ActionNotInAllowList(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "deploy_stack") // not in allowed list

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error for action not in allow list")
	}
	if !strings.Contains(err.Error(), "governance denied") {
		t.Errorf("expected governance denied error, got: %v", err)
	}
}

func TestGovernanceMiddleware_CircuitBreaker_RecordsSuccess(t *testing.T) {
	mw, engine := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	// Record 2 failures, then call a successful tool.
	engine.RecordResult("sre-agent", false)
	engine.RecordResult("sre-agent", false)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "get_health")

	// The handler succeeds — middleware should record success, resetting counter.
	_, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Now record 2 more failures. Agent should NOT be frozen (counter was reset by the success).
	engine.RecordResult("sre-agent", false)
	engine.RecordResult("sre-agent", false)

	_, err = wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("expected no error after reset, got: %v", err)
	}
}

func TestGovernanceMiddleware_CircuitBreaker_RecordsFailure(t *testing.T) {
	mw, engine := setupMiddleware(t)
	wrappedFailing := mw(govTestFailingHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "get_health")

	// Call failing handler 3 times. Each call should record a failure.
	for i := 0; i < 3; i++ {
		_, err := wrappedFailing(ctx, nil)
		if err == nil {
			t.Fatalf("call %d: expected error from failing handler", i)
		}
	}

	// Agent should now be frozen by the circuit breaker.
	// Try with a successful handler — should be denied.
	wrappedSuccess := mw(govTestHandler)
	_, err := wrappedSuccess(ctx, nil)
	if err == nil {
		t.Fatal("expected error after circuit breaker freeze")
	}
	if !strings.Contains(err.Error(), "frozen") {
		t.Errorf("expected 'frozen' in error, got: %v", err)
	}

	// Verify through the engine that the agent is recorded as frozen.
	_ = engine // engine state is checked indirectly above
}

func TestGovernanceMiddleware_PassesArgsToEngine(t *testing.T) {
	mw, _ := setupMiddleware(t)
	wrapped := mw(govTestHandler)

	ctx := context.Background()
	ctx = WithMCPAuth(ctx, MCPAuthContext{AgentID: "sre-agent", TenantID: "tnt_test"})
	ctx = WithGovernanceToolName(ctx, "restart_container")

	args := json.RawMessage(`{"target": "my-container", "force": true}`)
	result, err := wrapped(ctx, args)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestWithGovernanceToolName_Roundtrip(t *testing.T) {
	ctx := context.Background()
	if name := getGovernanceToolName(ctx); name != "" {
		t.Errorf("expected empty tool name, got %q", name)
	}

	ctx = WithGovernanceToolName(ctx, "my_tool")
	if name := getGovernanceToolName(ctx); name != "my_tool" {
		t.Errorf("expected my_tool, got %q", name)
	}
}
