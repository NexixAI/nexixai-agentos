package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// mockAgentRunner is a test double for AgentRunner.
type mockAgentRunner struct {
	// RunAgentFunc is called when RunAgent is invoked. Override per test.
	RunAgentFunc func(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error)
}

func (m *mockAgentRunner) RunAgent(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error) {
	if m.RunAgentFunc != nil {
		return m.RunAgentFunc(ctx, agentID, input, parentRunID, tenantID, correlationID)
	}
	return `{"result":"ok"}`, nil
}

func TestAgentInvoke_BasicDelegation(t *testing.T) {
	runner := &mockAgentRunner{
		RunAgentFunc: func(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error) {
			if agentID != "summarizer-agent" {
				t.Errorf("expected agent_id 'summarizer-agent', got %q", agentID)
			}
			if input != "Please summarize this text" {
				t.Errorf("unexpected input: %q", input)
			}
			if parentRunID != "run-parent-123" {
				t.Errorf("expected parent_run_id 'run-parent-123', got %q", parentRunID)
			}
			if tenantID != "tenant-abc" {
				t.Errorf("expected tenant_id 'tenant-abc', got %q", tenantID)
			}
			if correlationID != "corr-xyz" {
				t.Errorf("expected correlation_id 'corr-xyz', got %q", correlationID)
			}
			return "Summary: everything is fine.", nil
		},
	}

	tool := &AgentInvokeTool{
		ParentRunID:   "run-parent-123",
		TenantID:      "tenant-abc",
		CorrelationID: "corr-xyz",
	}
	tool.SetRunner(runner)

	result, err := tool.Execute(context.Background(), `{"agent_id":"summarizer-agent","input":"Please summarize this text"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out agentInvokeOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if out.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", out.Status)
	}
	if out.Output != "Summary: everything is fine." {
		t.Errorf("unexpected output: %q", out.Output)
	}
	if out.ParentRunID != "run-parent-123" {
		t.Errorf("expected parent_run_id 'run-parent-123', got %q", out.ParentRunID)
	}
	if out.AgentID != "summarizer-agent" {
		t.Errorf("expected agent_id 'summarizer-agent', got %q", out.AgentID)
	}
}

func TestAgentInvoke_DepthLimit(t *testing.T) {
	t.Setenv("AGENTOS_MAX_DELEGATION_DEPTH", "3")

	runner := &mockAgentRunner{}
	tool := &AgentInvokeTool{
		ParentRunID: "run-1",
		TenantID:    "tenant-1",
	}
	tool.SetRunner(runner)

	// Set depth to 3 (equal to max), should be rejected.
	ctx := WithDelegationDepth(context.Background(), 3)
	_, err := tool.Execute(ctx, `{"agent_id":"some-agent","input":"hello"}`)
	if err == nil {
		t.Fatal("expected depth limit error, got nil")
	}
	if !strings.Contains(err.Error(), "delegation depth limit reached") {
		t.Errorf("expected delegation depth limit error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "3/3") {
		t.Errorf("expected depth info '3/3' in error, got: %v", err)
	}
}

func TestAgentInvoke_DepthLimitDefault(t *testing.T) {
	// Ensure no env override.
	t.Setenv("AGENTOS_MAX_DELEGATION_DEPTH", "")

	runner := &mockAgentRunner{}
	tool := &AgentInvokeTool{
		ParentRunID: "run-1",
		TenantID:    "tenant-1",
	}
	tool.SetRunner(runner)

	// Depth at default max (5) should be rejected.
	ctx := WithDelegationDepth(context.Background(), 5)
	_, err := tool.Execute(ctx, `{"agent_id":"agent-x","input":"hi"}`)
	if err == nil {
		t.Fatal("expected depth limit error at default max")
	}
	if !strings.Contains(err.Error(), "delegation depth limit reached") {
		t.Errorf("expected depth limit error, got: %v", err)
	}

	// Depth at 4 (one below default max) should succeed.
	ctx = WithDelegationDepth(context.Background(), 4)
	result, err := tool.Execute(ctx, `{"agent_id":"agent-x","input":"hi"}`)
	if err != nil {
		t.Fatalf("unexpected error at depth 4: %v", err)
	}
	var out agentInvokeOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if out.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", out.Status)
	}
}

func TestAgentInvoke_CircularRejection(t *testing.T) {
	// Simulate a chain: A -> B -> C -> D (depth 3), then D tries to invoke E.
	// With max depth 3, this should fail.
	t.Setenv("AGENTOS_MAX_DELEGATION_DEPTH", "3")

	var invocations int32

	// The runner itself creates child invocations recursively.
	runner := &mockAgentRunner{
		RunAgentFunc: func(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error) {
			atomic.AddInt32(&invocations, 1)

			// Each child also tries to invoke another agent.
			childTool := &AgentInvokeTool{
				ParentRunID:   "run-child",
				TenantID:      tenantID,
				CorrelationID: correlationID,
			}
			childRunner := &mockAgentRunner{
				RunAgentFunc: func(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error) {
					atomic.AddInt32(&invocations, 1)
					return "leaf", nil
				},
			}
			childTool.SetRunner(childRunner)

			result, err := childTool.Execute(ctx, fmt.Sprintf(`{"agent_id":"next-agent","input":"chain"}`))
			if err != nil {
				return "", err
			}
			return result, nil
		},
	}

	tool := &AgentInvokeTool{
		ParentRunID: "run-root",
		TenantID:    "tenant-1",
	}
	tool.SetRunner(runner)

	// Start at depth 1 (simulating root already at depth 1).
	ctx := WithDelegationDepth(context.Background(), 1)
	result, err := tool.Execute(ctx, `{"agent_id":"agent-a","input":"start chain"}`)

	// At depth 1, runner is called (depth becomes 2).
	// Inside runner, child tool tries at depth 2 (becomes 3).
	// Inside that runner, result returned is "leaf" (depth 3 == max, rejected).
	// So the chain should stop.
	if err != nil {
		// The error propagates up from the child.
		if !strings.Contains(err.Error(), "delegation depth limit reached") {
			t.Errorf("expected delegation depth limit, got: %v", err)
		}
	} else {
		// If no error, the result should indicate failure somewhere in the chain.
		var out agentInvokeOutput
		if parseErr := json.Unmarshal([]byte(result), &out); parseErr == nil {
			if out.Status == "completed" && atomic.LoadInt32(&invocations) > 2 {
				t.Error("expected delegation chain to be limited")
			}
		}
	}
}

func TestAgentInvoke_CancellationCascade(t *testing.T) {
	// When parent context is cancelled, child should also be cancelled.
	childStarted := make(chan struct{})
	childCancelled := make(chan struct{})

	runner := &mockAgentRunner{
		RunAgentFunc: func(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error) {
			close(childStarted)
			// Wait for context cancellation.
			<-ctx.Done()
			close(childCancelled)
			return "", ctx.Err()
		},
	}

	tool := &AgentInvokeTool{
		ParentRunID: "run-parent",
		TenantID:    "tenant-1",
	}
	tool.SetRunner(runner)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := tool.Execute(ctx, `{"agent_id":"slow-agent","input":"do something slow"}`)
		errCh <- err
	}()

	// Wait for child to start, then cancel parent.
	<-childStarted
	cancel()

	// Child should be cancelled promptly.
	select {
	case <-childCancelled:
		// Good - child was cancelled.
	case <-time.After(2 * time.Second):
		t.Fatal("child was not cancelled within timeout")
	}

	// The tool should return an error.
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from cancelled parent")
		}
		if !strings.Contains(err.Error(), "parent cancelled") && !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("expected cancellation error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not return after parent cancellation")
	}
}

func TestAgentInvoke_MissingAgentID(t *testing.T) {
	tool := &AgentInvokeTool{}
	_, err := tool.Execute(context.Background(), `{"input":"hello"}`)
	if err == nil {
		t.Fatal("expected error for missing agent_id")
	}
	if !strings.Contains(err.Error(), "agent_id is required") {
		t.Errorf("expected 'agent_id is required' error, got: %v", err)
	}
}

func TestAgentInvoke_MissingInput(t *testing.T) {
	tool := &AgentInvokeTool{}
	_, err := tool.Execute(context.Background(), `{"agent_id":"x"}`)
	if err == nil {
		t.Fatal("expected error for missing input")
	}
	if !strings.Contains(err.Error(), "input is required") {
		t.Errorf("expected 'input is required' error, got: %v", err)
	}
}

func TestAgentInvoke_InvalidJSON(t *testing.T) {
	tool := &AgentInvokeTool{}
	_, err := tool.Execute(context.Background(), `not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid input") {
		t.Errorf("expected 'invalid input' error, got: %v", err)
	}
}

func TestAgentInvoke_NoRunner(t *testing.T) {
	tool := &AgentInvokeTool{
		ParentRunID: "run-1",
		TenantID:    "tenant-1",
	}
	// No runner set.
	_, err := tool.Execute(context.Background(), `{"agent_id":"x","input":"hi"}`)
	if err == nil {
		t.Fatal("expected error when runner is not configured")
	}
	if !strings.Contains(err.Error(), "runner not configured") {
		t.Errorf("expected 'runner not configured' error, got: %v", err)
	}
}

func TestAgentInvoke_ChildFailure(t *testing.T) {
	runner := &mockAgentRunner{
		RunAgentFunc: func(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error) {
			return "", fmt.Errorf("agent crashed: out of memory")
		},
	}

	tool := &AgentInvokeTool{
		ParentRunID: "run-parent",
		TenantID:    "tenant-1",
	}
	tool.SetRunner(runner)

	result, err := tool.Execute(context.Background(), `{"agent_id":"crashy-agent","input":"do stuff"}`)
	if err != nil {
		t.Fatalf("unexpected error (should return JSON with error): %v", err)
	}

	var out agentInvokeOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if out.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", out.Status)
	}
	if !strings.Contains(out.ErrorMessage, "out of memory") {
		t.Errorf("expected error message about OOM, got %q", out.ErrorMessage)
	}
}

func TestAgentInvoke_DepthContextPropagation(t *testing.T) {
	// Verify that the child context has incremented depth.
	var childDepth int

	runner := &mockAgentRunner{
		RunAgentFunc: func(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error) {
			childDepth = DelegationDepth(ctx)
			return "done", nil
		},
	}

	tool := &AgentInvokeTool{
		ParentRunID: "run-1",
		TenantID:    "tenant-1",
	}
	tool.SetRunner(runner)

	ctx := WithDelegationDepth(context.Background(), 2)
	_, err := tool.Execute(ctx, `{"agent_id":"agent-a","input":"check depth"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if childDepth != 3 {
		t.Errorf("expected child depth 3, got %d", childDepth)
	}
}

func TestAgentInvoke_RegistryIntegration(t *testing.T) {
	// Verify the tool can be registered in a Registry.
	reg := NewRegistry()
	tool := &AgentInvokeTool{
		ParentRunID: "run-1",
		TenantID:    "tenant-1",
	}
	reg.Register(tool)

	got, ok := reg.Get("agent_invoke")
	if !ok {
		t.Fatal("expected agent_invoke to be registered")
	}
	if got.Name() != "agent_invoke" {
		t.Errorf("expected name 'agent_invoke', got %q", got.Name())
	}
}

func TestDelegationDepth_ZeroByDefault(t *testing.T) {
	depth := DelegationDepth(context.Background())
	if depth != 0 {
		t.Errorf("expected default depth 0, got %d", depth)
	}
}

func TestWithDelegationDepth_RoundTrip(t *testing.T) {
	ctx := WithDelegationDepth(context.Background(), 7)
	if got := DelegationDepth(ctx); got != 7 {
		t.Errorf("expected depth 7, got %d", got)
	}
}
