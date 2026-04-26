package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/NexixAI/nexixai-agentos/internal/agents"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- spawn_agent tool ---

type spawnAgentInput struct {
	Directive    string   `json:"directive"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
}

type spawnAgentOutput struct {
	AgentID   string `json:"agent_id"`
	ParentID  string `json:"parent_id"`
	TenantID  string `json:"tenant_id"`
	Status    string `json:"status"`
	Directive string `json:"directive"`
}

var spawnAgentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"directive": {
			"type": "string",
			"description": "The directive (task description) for the spawned agent."
		},
		"allowed_tools": {
			"type": "array",
			"items": { "type": "string" },
			"description": "Optional list of tool names the spawned agent is allowed to use."
		}
	},
	"required": ["directive"],
	"additionalProperties": false
}`)

// --- message_agent tool ---

type messageAgentInput struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

type messageAgentOutput struct {
	Delivered bool   `json:"delivered"`
	ToAgentID string `json:"to_agent_id"`
}

var messageAgentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"agent_id": {
			"type": "string",
			"description": "The ID of the agent to send the message to."
		},
		"message": {
			"type": "string",
			"description": "The message content to deliver."
		}
	},
	"required": ["agent_id", "message"],
	"additionalProperties": false
}`)

// --- get_agent_status tool ---

type getAgentStatusInput struct {
	AgentID string `json:"agent_id"`
}

type getAgentStatusOutput struct {
	AgentID      string          `json:"agent_id"`
	ParentID     string          `json:"parent_id"`
	TenantID     string          `json:"tenant_id"`
	Status       string          `json:"status"`
	Directive    string          `json:"directive"`
	AllowedTools []string        `json:"allowed_tools"`
	Messages     []agents.Message `json:"messages"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

var getAgentStatusSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"agent_id": {
			"type": "string",
			"description": "The ID of the agent to get status for."
		}
	},
	"required": ["agent_id"],
	"additionalProperties": false
}`)

// RegisterAgentTools registers spawn_agent, message_agent, and get_agent_status
// with the given tool registry.
func RegisterAgentTools(registry *mcp.ToolRegistry, mgr agents.AgentManager) error {
	if err := registry.Register(mcp.Tool{
		Name:         "spawn_agent",
		Description:  "Spawn a new child agent with a directive and optional tool allowlist. The child inherits the caller's tenant.",
		InputSchema:  spawnAgentSchema,
		MinClearance: mcp.ClearanceExecute,
		Static:       true,
		Handler:      makeSpawnAgentHandler(mgr),
	}); err != nil {
		return fmt.Errorf("register spawn_agent: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "message_agent",
		Description:  "Send a message to another agent within the same tenant.",
		InputSchema:  messageAgentSchema,
		MinClearance: mcp.ClearanceExecute,
		Static:       true,
		Handler:      makeMessageAgentHandler(mgr),
	}); err != nil {
		return fmt.Errorf("register message_agent: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "get_agent_status",
		Description:  "Get the current status, messages, and metadata of an agent.",
		InputSchema:  getAgentStatusSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeGetAgentStatusHandler(mgr),
	}); err != nil {
		return fmt.Errorf("register get_agent_status: %w", err)
	}

	return nil
}

// makeSpawnAgentHandler returns a handler that spawns a child agent.
func makeSpawnAgentHandler(mgr agents.AgentManager) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input spawnAgentInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.Directive == "" {
			return nil, fmt.Errorf("directive is required")
		}

		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		agent, err := mgr.SpawnAgent(ctx, ac.AgentID, ac.TenantID, input.Directive, input.AllowedTools)
		if err != nil {
			slog.Error("mcp/agents: spawn failed",
				"parent_id", ac.AgentID,
				"tenant_id", ac.TenantID,
				"error", err,
			)
			return nil, fmt.Errorf("failed to spawn agent: %w", err)
		}

		slog.Info("mcp/agents: spawned",
			"agent_id", agent.ID,
			"parent_id", ac.AgentID,
			"tenant_id", ac.TenantID,
		)

		return spawnAgentOutput{
			AgentID:   agent.ID,
			ParentID:  agent.ParentID,
			TenantID:  agent.TenantID,
			Status:    string(agent.Status),
			Directive: agent.Directive,
		}, nil
	}
}

// makeMessageAgentHandler returns a handler that sends a message to an agent.
func makeMessageAgentHandler(mgr agents.AgentManager) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input messageAgentInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.AgentID == "" {
			return nil, fmt.Errorf("agent_id is required")
		}
		if input.Message == "" {
			return nil, fmt.Errorf("message is required")
		}

		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		if err := mgr.SendMessage(ctx, ac.AgentID, input.AgentID, input.Message); err != nil {
			slog.Error("mcp/agents: message failed",
				"from_id", ac.AgentID,
				"to_id", input.AgentID,
				"error", err,
			)
			return nil, fmt.Errorf("failed to send message: %w", err)
		}

		slog.Info("mcp/agents: message sent",
			"from_id", ac.AgentID,
			"to_id", input.AgentID,
		)

		return messageAgentOutput{
			Delivered: true,
			ToAgentID: input.AgentID,
		}, nil
	}
}

// makeGetAgentStatusHandler returns a handler that retrieves agent status.
func makeGetAgentStatusHandler(mgr agents.AgentManager) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input getAgentStatusInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.AgentID == "" {
			return nil, fmt.Errorf("agent_id is required")
		}

		// Auth context is checked for identity but get_agent_status is
		// ClearanceInternal (T1), so any authenticated agent can query.
		if _, ok := mcp.GetMCPAuth(ctx); !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		agent, err := mgr.GetStatus(ctx, input.AgentID)
		if err != nil {
			slog.Error("mcp/agents: get status failed",
				"agent_id", input.AgentID,
				"error", err,
			)
			return nil, fmt.Errorf("failed to get agent status: %w", err)
		}

		tools := agent.AllowedTools
		if tools == nil {
			tools = []string{}
		}
		msgs := agent.Messages
		if msgs == nil {
			msgs = []agents.Message{}
		}

		return getAgentStatusOutput{
			AgentID:      agent.ID,
			ParentID:     agent.ParentID,
			TenantID:     agent.TenantID,
			Status:       string(agent.Status),
			Directive:    agent.Directive,
			AllowedTools: tools,
			Messages:     msgs,
			CreatedAt:    agent.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:    agent.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		}, nil
	}
}
