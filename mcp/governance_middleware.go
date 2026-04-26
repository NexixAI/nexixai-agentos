package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/NexixAI/nexixai-agentos/internal/governance"
)

// GovernanceMiddleware returns a function that wraps a ToolHandler with
// governance policy evaluation. Before the handler executes, it calls
// engine.Check() to decide whether the tool call is allowed. After execution
// it calls engine.RecordResult() so the circuit breaker can track outcomes.
//
// The agent identity is extracted from the MCP auth context (MCPAuthContext).
// If no auth context is present, the call is denied (fail-closed).
func GovernanceMiddleware(engine *governance.Engine) func(handler ToolHandler) ToolHandler {
	return func(handler ToolHandler) ToolHandler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			// Extract agent identity from the MCP auth context.
			ac, ok := GetMCPAuth(ctx)
			if !ok {
				slog.Warn("governance: denied — missing auth context")
				return nil, fmt.Errorf("governance denied: missing auth context")
			}
			agentID := ac.AgentID
			if agentID == "" {
				slog.Warn("governance: denied — empty agent ID")
				return nil, fmt.Errorf("governance denied: empty agent ID")
			}

			// Parse the tool name from params. The middleware receives
			// the tool arguments (the inner "arguments" field), not the
			// full ToolCallParams. We need the tool name passed separately.
			// Since the middleware wraps per-tool handlers, we extract the
			// tool name from context or pass it through. For now, we parse
			// the arguments as a generic map for the governance engine.
			var args map[string]any
			if len(params) > 0 {
				if err := json.Unmarshal(params, &args); err != nil {
					// If params aren't a JSON object, pass nil args.
					args = nil
				}
			}

			// The tool name must be passed via context so the middleware
			// knows which tool is being called. Use GovernanceToolName.
			toolName := getGovernanceToolName(ctx)
			if toolName == "" {
				slog.Warn("governance: denied — missing tool name in context")
				return nil, fmt.Errorf("governance denied: missing tool name in context")
			}

			// Evaluate governance policy.
			decision, err := engine.Check(ctx, agentID, toolName, args)
			if err != nil {
				// Fail-closed: any error in governance evaluation results in deny.
				slog.Error("governance: check error — denying",
					"agent", agentID,
					"tool", toolName,
					"error", err,
				)
				return nil, fmt.Errorf("governance denied: internal error")
			}

			if decision.Verdict == governance.Deny {
				slog.Warn("governance: denied",
					"agent", agentID,
					"tool", toolName,
					"reason", decision.Reason,
					"domain", decision.Domain,
				)
				return nil, fmt.Errorf("governance denied: %s", decision.Reason)
			}

			slog.Debug("governance: allowed",
				"agent", agentID,
				"tool", toolName,
			)

			// Call the actual tool handler.
			result, handlerErr := handler(ctx, params)

			// Record result for circuit breaker tracking.
			engine.RecordResult(agentID, handlerErr == nil)

			return result, handlerErr
		}
	}
}

// governanceToolNameKey is the context key for the tool name used by governance middleware.
type governanceToolNameKey struct{}

// WithGovernanceToolName stores the tool name on the context for use by the governance middleware.
func WithGovernanceToolName(ctx context.Context, toolName string) context.Context {
	return context.WithValue(ctx, governanceToolNameKey{}, toolName)
}

// getGovernanceToolName retrieves the tool name from the context.
func getGovernanceToolName(ctx context.Context) string {
	v := ctx.Value(governanceToolNameKey{})
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
