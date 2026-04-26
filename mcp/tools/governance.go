package tools

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// AuditLogger writes immutable audit entries for agent decisions.
type AuditLogger interface {
	LogDecision(ctx context.Context, entry DecisionEntry) error
}

// DecisionEntry represents an immutable agent decision record.
type DecisionEntry struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	TenantID  string    `json:"tenant_id"`
	Decision  string    `json:"decision"`
	Reasoning string    `json:"reasoning"`
	CreatedAt time.Time `json:"created_at"`
}

// --- check_authorization tool ---

type checkAuthInput struct {
	ToolName string `json:"tool_name"`
}

type checkAuthOutput struct {
	Authorized bool   `json:"authorized"`
	Reason     string `json:"reason"`
}

var checkAuthSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"tool_name": {
			"type": "string",
			"description": "The name of the tool to check authorization for."
		}
	},
	"required": ["tool_name"],
	"additionalProperties": false
}`)

// --- log_decision tool ---

type logDecisionInput struct {
	Decision  string `json:"decision"`
	Reasoning string `json:"reasoning"`
}

type logDecisionOutput struct {
	ID        string `json:"id"`
	Logged    bool   `json:"logged"`
	Timestamp string `json:"timestamp"`
}

var logDecisionSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"decision": {
			"type": "string",
			"description": "The decision text to log."
		},
		"reasoning": {
			"type": "string",
			"description": "The reasoning behind the decision."
		}
	},
	"required": ["decision", "reasoning"],
	"additionalProperties": false
}`)

// --- request_elevation tool ---

type requestElevationInput struct {
	Reason        string `json:"reason"`
	RequestedTier int    `json:"requested_tier"`
}

type requestElevationOutput struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

var requestElevationSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"reason": {
			"type": "string",
			"description": "The reason for requesting elevated clearance."
		},
		"requested_tier": {
			"type": "integer",
			"description": "The requested clearance tier (0-4).",
			"minimum": 0,
			"maximum": 4
		}
	},
	"required": ["reason", "requested_tier"],
	"additionalProperties": false
}`)

// --- check_elevation_status tool ---

type checkElevationStatusInput struct {
	RequestID string `json:"request_id"`
}

type checkElevationStatusOutput struct {
	RequestID    string  `json:"request_id"`
	Status       string  `json:"status"`
	ApprovedTier *int    `json:"approved_tier,omitempty"`
	ExpiresAt    *string `json:"expires_at,omitempty"`
}

var checkElevationStatusSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"request_id": {
			"type": "string",
			"description": "The ID of the elevation request to check."
		}
	},
	"required": ["request_id"],
	"additionalProperties": false
}`)

// RegisterGovernanceTools registers check_authorization, log_decision,
// request_elevation, and check_elevation_status with the given tool registry.
func RegisterGovernanceTools(registry *mcp.ToolRegistry, clearanceStore mcp.ClearanceStore, auditLogger AuditLogger, elevationStore ...postgres.ElevationStore) error {
	if err := registry.Register(mcp.Tool{
		Name:         "check_authorization",
		Description:  "Check whether the calling agent is authorized to invoke a given tool.",
		InputSchema:  checkAuthSchema,
		MinClearance: mcp.ClearancePublic,
		Static:       true,
		Handler:      makeCheckAuthHandler(registry, clearanceStore),
	}); err != nil {
		return fmt.Errorf("register check_authorization: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "log_decision",
		Description:  "Log an immutable audit entry for an agent decision with reasoning.",
		InputSchema:  logDecisionSchema,
		MinClearance: mcp.ClearancePublic,
		Static:       true,
		Handler:      makeLogDecisionHandler(auditLogger),
	}); err != nil {
		return fmt.Errorf("register log_decision: %w", err)
	}

	// Register elevation tools if an ElevationStore is provided.
	if len(elevationStore) > 0 && elevationStore[0] != nil {
		es := elevationStore[0]

		if err := registry.Register(mcp.Tool{
			Name:         "request_elevation",
			Description:  "Request elevated clearance tier for human review. Returns a request ID for tracking.",
			InputSchema:  requestElevationSchema,
			MinClearance: mcp.ClearancePublic,
			Static:       true,
			Handler:      makeRequestElevationHandler(es),
		}); err != nil {
			return fmt.Errorf("register request_elevation: %w", err)
		}

		if err := registry.Register(mcp.Tool{
			Name:         "check_elevation_status",
			Description:  "Check the status of an elevation request by ID.",
			InputSchema:  checkElevationStatusSchema,
			MinClearance: mcp.ClearancePublic,
			Static:       true,
			Handler:      makeCheckElevationStatusHandler(es),
		}); err != nil {
			return fmt.Errorf("register check_elevation_status: %w", err)
		}
	}

	return nil
}

// makeCheckAuthHandler returns a handler that checks whether the calling
// agent has sufficient clearance for the named tool.
func makeCheckAuthHandler(registry *mcp.ToolRegistry, store mcp.ClearanceStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input checkAuthInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		if input.ToolName == "" {
			return nil, fmt.Errorf("tool_name is required")
		}

		// Look up the target tool.
		tool := registry.Get(input.ToolName)
		if tool == nil {
			return checkAuthOutput{
				Authorized: false,
				Reason:     fmt.Sprintf("tool %q not found", input.ToolName),
			}, nil
		}

		// Check the calling agent's clearance.
		rpcErr := mcp.AuthorizeToolCall(ctx, tool, store)
		if rpcErr != nil {
			return checkAuthOutput{
				Authorized: false,
				Reason:     rpcErr.Message,
			}, nil
		}

		return checkAuthOutput{
			Authorized: true,
			Reason:     "agent has sufficient clearance",
		}, nil
	}
}

// makeLogDecisionHandler returns a handler that writes an immutable audit
// entry via the AuditLogger.
func makeLogDecisionHandler(logger AuditLogger) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input logDecisionInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		if input.Decision == "" {
			return nil, fmt.Errorf("decision is required")
		}
		if input.Reasoning == "" {
			return nil, fmt.Errorf("reasoning is required")
		}

		// Extract identity from MCP auth context.
		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		entryID, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("generate entry ID: %w", err)
		}

		now := time.Now().UTC()
		entry := DecisionEntry{
			ID:        entryID,
			AgentID:   ac.AgentID,
			TenantID:  ac.TenantID,
			Decision:  input.Decision,
			Reasoning: input.Reasoning,
			CreatedAt: now,
		}

		if err := logger.LogDecision(ctx, entry); err != nil {
			slog.Error("mcp/governance: failed to log decision",
				"agent_id", ac.AgentID,
				"error", err,
			)
			return nil, fmt.Errorf("failed to log decision: %w", err)
		}

		slog.Info("mcp/governance: decision logged",
			"id", entryID,
			"agent_id", ac.AgentID,
		)

		return logDecisionOutput{
			ID:        entryID,
			Logged:    true,
			Timestamp: now.Format(time.RFC3339),
		}, nil
	}
}

// makeRequestElevationHandler returns a handler that creates a pending
// elevation request for human review.
func makeRequestElevationHandler(store postgres.ElevationStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input requestElevationInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		if input.Reason == "" {
			return nil, fmt.Errorf("reason is required")
		}
		if input.RequestedTier < 0 || input.RequestedTier > 4 {
			return nil, fmt.Errorf("requested_tier must be 0-4, got %d", input.RequestedTier)
		}

		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		reqID, err := store.RequestElevation(ctx, ac.AgentID, ac.TenantID, input.RequestedTier, input.Reason)
		if err != nil {
			slog.Error("mcp/governance: failed to create elevation request",
				"agent_id", ac.AgentID,
				"error", err,
			)
			return nil, fmt.Errorf("failed to create elevation request: %w", err)
		}

		slog.Info("mcp/governance: elevation request created",
			"request_id", reqID,
			"agent_id", ac.AgentID,
			"requested_tier", input.RequestedTier,
		)

		return requestElevationOutput{
			RequestID: reqID,
			Status:    "pending",
		}, nil
	}
}

// makeCheckElevationStatusHandler returns a handler that checks the status
// of an elevation request by ID.
func makeCheckElevationStatusHandler(store postgres.ElevationStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input checkElevationStatusInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		if input.RequestID == "" {
			return nil, fmt.Errorf("request_id is required")
		}

		req, err := store.GetElevationStatus(ctx, input.RequestID)
		if err != nil {
			return nil, fmt.Errorf("elevation request not found: %w", err)
		}

		out := checkElevationStatusOutput{
			RequestID: req.ID,
			Status:    req.Status,
		}

		if req.Status == "approved" {
			out.ApprovedTier = &req.RequestedTier
			if req.ExpiresAt != nil {
				ts := req.ExpiresAt.Format(time.RFC3339)
				out.ExpiresAt = &ts
			}
		}

		return out, nil
	}
}

// newUUID generates a UUID v4 string using crypto/rand.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Set version 4 and variant bits per RFC 4122.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
