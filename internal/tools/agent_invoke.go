package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
)

// delegationDepthKey is the context key for tracking invocation chain depth.
type delegationDepthKey struct{}

// DefaultMaxDelegationDepth is the default maximum allowed delegation depth.
const DefaultMaxDelegationDepth = 5

// maxDelegationDepth returns the configured max depth from the environment,
// falling back to DefaultMaxDelegationDepth.
func maxDelegationDepth() int {
	if v := os.Getenv("AGENTOS_MAX_DELEGATION_DEPTH"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
		slog.Warn("invalid AGENTOS_MAX_DELEGATION_DEPTH, using default",
			"value", v, "default", DefaultMaxDelegationDepth)
	}
	return DefaultMaxDelegationDepth
}

// DelegationDepth returns the current delegation depth from context.
func DelegationDepth(ctx context.Context) int {
	v, _ := ctx.Value(delegationDepthKey{}).(int)
	return v
}

// WithDelegationDepth returns a new context with the given delegation depth.
func WithDelegationDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, delegationDepthKey{}, depth)
}

// AgentRunner is the interface that the orchestrator must implement to
// allow the agent_invoke tool to start child runs.
type AgentRunner interface {
	// RunAgent starts a child run for the given agent with the given input.
	// It inherits tenant_id and correlation_id from the parent context.
	// parentRunID links the child back to its parent.
	// It blocks until the child run completes and returns the output.
	RunAgent(ctx context.Context, agentID, input, parentRunID, tenantID, correlationID string) (string, error)
}

// AgentInvokeTool delegates execution to another agent as a child run.
type AgentInvokeTool struct {
	mu     sync.RWMutex
	runner AgentRunner

	// ParentRunID is the run_id of the parent run invoking this tool.
	ParentRunID string
	// TenantID is inherited from the parent run.
	TenantID string
	// CorrelationID is inherited from the parent run.
	CorrelationID string
}

func (t *AgentInvokeTool) Name() string { return "agent_invoke" }

func (t *AgentInvokeTool) Description() string {
	return "Delegate a task to another agent. The child run inherits tenant and correlation context from the parent."
}

func (t *AgentInvokeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": map[string]any{"type": "string", "description": "The ID of the agent to invoke"},
			"input":    map[string]any{"type": "string", "description": "The input text to pass to the agent"},
		},
		"required": []string{"agent_id", "input"},
	}
}

type agentInvokeInput struct {
	AgentID string `json:"agent_id"`
	Input   string `json:"input"`
}

type agentInvokeOutput struct {
	RunID        string `json:"run_id,omitempty"`
	ParentRunID  string `json:"parent_run_id"`
	AgentID      string `json:"agent_id"`
	Output       string `json:"output"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error,omitempty"`
}

// SetRunner sets the AgentRunner used to dispatch child runs.
func (t *AgentInvokeTool) SetRunner(r AgentRunner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runner = r
}

func (t *AgentInvokeTool) getRunner() AgentRunner {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.runner
}

func (t *AgentInvokeTool) Execute(ctx context.Context, input string) (string, error) {
	var in agentInvokeInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if in.AgentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}
	if in.Input == "" {
		return "", fmt.Errorf("input is required")
	}

	// Check delegation depth to prevent circular/unbounded delegation.
	currentDepth := DelegationDepth(ctx)
	maxDepth := maxDelegationDepth()
	if currentDepth >= maxDepth {
		slog.Warn("agent delegation depth limit reached",
			"current_depth", currentDepth,
			"max_depth", maxDepth,
			"agent_id", in.AgentID,
			"parent_run_id", t.ParentRunID,
		)
		return "", fmt.Errorf("delegation depth limit reached (%d/%d): cannot invoke agent %q",
			currentDepth, maxDepth, in.AgentID)
	}

	runner := t.getRunner()
	if runner == nil {
		return "", fmt.Errorf("agent runner not configured")
	}

	// Increment depth for the child context.
	childCtx := WithDelegationDepth(ctx, currentDepth+1)

	slog.Info("invoking child agent",
		"agent_id", in.AgentID,
		"parent_run_id", t.ParentRunID,
		"tenant_id", t.TenantID,
		"depth", currentDepth+1,
	)

	result, err := runner.RunAgent(childCtx, in.AgentID, in.Input, t.ParentRunID, t.TenantID, t.CorrelationID)
	if err != nil {
		// Check if parent context was cancelled (cascade).
		if ctx.Err() != nil {
			return "", fmt.Errorf("parent cancelled: %w", ctx.Err())
		}
		out := agentInvokeOutput{
			ParentRunID:  t.ParentRunID,
			AgentID:      in.AgentID,
			Status:       "failed",
			ErrorMessage: err.Error(),
		}
		data, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return "", fmt.Errorf("child agent %q failed: %w", in.AgentID, err)
		}
		return string(data), nil
	}

	out := agentInvokeOutput{
		ParentRunID: t.ParentRunID,
		AgentID:     in.AgentID,
		Output:      result,
		Status:      "completed",
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("error encoding response: %w", err)
	}
	return string(data), nil
}
