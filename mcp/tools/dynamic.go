package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- discover_tools ---

// discoverToolsResponse is the structured result returned by discover_tools.
type discoverToolsResponse struct {
	Tools []discoveredTool `json:"tools"`
	Count int              `json:"count"`
}

// discoveredTool is a single tool entry in the discover_tools response.
type discoveredTool struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	MinClearance int    `json:"min_clearance"`
	Static       bool   `json:"static"`
}

var discoverToolsSchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

// --- get_tool_schema ---

type getToolSchemaInput struct {
	ToolName string `json:"tool_name"`
}

type getToolSchemaResponse struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

var getToolSchemaSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"tool_name": {
			"type": "string",
			"description": "The name of the tool to retrieve the schema for."
		}
	},
	"required": ["tool_name"],
	"additionalProperties": false
}`)

// --- invoke_tool ---

type invokeToolInput struct {
	ToolName string          `json:"tool_name"`
	Params   json.RawMessage `json:"params"`
}

var invokeToolSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"tool_name": {
			"type": "string",
			"description": "The name of the tool to invoke."
		},
		"params": {
			"type": "object",
			"description": "The parameters to pass to the tool."
		}
	},
	"required": ["tool_name", "params"],
	"additionalProperties": false
}`)

// --- list_capabilities ---

// capabilityCategory describes a category of tools in the registry.
type capabilityCategory struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
	Active   bool   `json:"active"`
}

type listCapabilitiesResponse struct {
	Categories []capabilityCategory `json:"categories"`
}

var listCapabilitiesSchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

// knownCategories is the canonical order of tool categories.
var knownCategories = []string{
	"chat",
	"health",
	"governance",
	"sandbox",
	"http",
	"memory",
	"knowledge",
	"observe",
	"facets",
	"dynamic",
}

// toolCategoryPrefixes maps tool names (or prefixes) to categories.
// The mapping is intentionally explicit; unknown tools fall into an "other"
// bucket (which is included only if non-empty).
var toolCategoryMap = map[string]string{
	"chat_completion":     "chat",
	"list_models":         "chat",
	"get_active_model":    "chat",
	"get_health":          "health",
	"check_authorization": "governance",
	"log_decision":        "governance",
	"execute_code":        "sandbox",
	"fetch_url":           "http",
	"http_request":        "http",
	"ping_url":            "http",
	"memory_store":        "memory",
	"memory_recall":       "memory",
	"index_document":      "knowledge",
	"search_knowledge":    "knowledge",
	"query_metrics":       "observe",
	"get_alerts":          "observe",
	"get_container_status": "observe",
	"get_audit_log":       "observe",
	"classify_prompt":     "facets",
	"list_facets":         "facets",
	"register_facet":      "facets",
	"unregister_facet":    "facets",
	"discover_tools":      "dynamic",
	"get_tool_schema":     "dynamic",
	"invoke_tool":         "dynamic",
	"list_capabilities":   "dynamic",
}

// RegisterDynamicTools registers discover_tools, get_tool_schema, invoke_tool,
// and list_capabilities with the given registry. The registry reference is
// captured so these tools reflect the current registry state at call time.
func RegisterDynamicTools(registry *mcp.ToolRegistry, clearanceStore mcp.ClearanceStore) error {
	if err := registry.Register(mcp.Tool{
		Name:         "discover_tools",
		Description:  "Returns all tools the calling agent is authorized to use, filtered by the agent's clearance tier.",
		InputSchema:  discoverToolsSchema,
		MinClearance: mcp.ClearancePublic,
		Static:       true,
		Handler:      makeDiscoverToolsHandler(registry, clearanceStore),
	}); err != nil {
		return fmt.Errorf("register discover_tools: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "get_tool_schema",
		Description:  "Returns the full JSON schema for the named tool.",
		InputSchema:  getToolSchemaSchema,
		MinClearance: mcp.ClearancePublic,
		Static:       true,
		Handler:      makeGetToolSchemaHandler(registry),
	}); err != nil {
		return fmt.Errorf("register get_tool_schema: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "invoke_tool",
		Description:  "Looks up a tool by name, checks authorization, and invokes it. Meta-tool for dispatching to other tools.",
		InputSchema:  invokeToolSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeInvokeToolHandler(registry, clearanceStore),
	}); err != nil {
		return fmt.Errorf("register invoke_tool: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "list_capabilities",
		Description:  "Returns a summary of all capability categories with tool counts and active status.",
		InputSchema:  listCapabilitiesSchema,
		MinClearance: mcp.ClearancePublic,
		Static:       true,
		Handler:      makeListCapabilitiesHandler(registry),
	}); err != nil {
		return fmt.Errorf("register list_capabilities: %w", err)
	}

	return nil
}

// makeDiscoverToolsHandler returns a handler that lists all tools the calling
// agent is authorized to use. Authorization is determined by comparing the
// agent's clearance tier (from MCP auth context) against each tool's MinClearance.
func makeDiscoverToolsHandler(registry *mcp.ToolRegistry, store mcp.ClearanceStore) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		tier, err := store.GetClearance(ctx, ac.AgentID)
		if err != nil {
			slog.Warn("mcp/dynamic: discover_tools clearance lookup failed",
				"agent_id", ac.AgentID,
				"error", err,
			)
			return nil, fmt.Errorf("clearance lookup failed: %w", err)
		}

		allTools := registry.List()
		var visible []discoveredTool
		for _, t := range allTools {
			if tier >= t.MinClearance {
				visible = append(visible, discoveredTool{
					Name:         t.Name,
					Description:  t.Description,
					MinClearance: int(t.MinClearance),
					Static:       t.Static,
				})
			}
		}

		// Sort by name for deterministic output.
		sort.Slice(visible, func(i, j int) bool {
			return visible[i].Name < visible[j].Name
		})

		slog.Info("mcp/dynamic: discover_tools",
			"agent_id", ac.AgentID,
			"agent_tier", tier,
			"visible_count", len(visible),
		)

		return discoverToolsResponse{
			Tools: visible,
			Count: len(visible),
		}, nil
	}
}

// makeGetToolSchemaHandler returns a handler that retrieves the full JSON
// schema for a named tool.
func makeGetToolSchemaHandler(registry *mcp.ToolRegistry) mcp.ToolHandler {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var input getToolSchemaInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		if input.ToolName == "" {
			return nil, fmt.Errorf("tool_name is required")
		}

		tool := registry.Get(input.ToolName)
		if tool == nil {
			return nil, fmt.Errorf("tool %q not found", input.ToolName)
		}

		return getToolSchemaResponse{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}, nil
	}
}

// makeInvokeToolHandler returns a meta-tool handler that dispatches to another
// tool by name. It checks BOTH invoke_tool's own clearance (T1, enforced at
// registration) AND the target tool's clearance against the calling agent.
func makeInvokeToolHandler(registry *mcp.ToolRegistry, store mcp.ClearanceStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input invokeToolInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		if input.ToolName == "" {
			return nil, fmt.Errorf("tool_name is required")
		}

		tool := registry.Get(input.ToolName)
		if tool == nil {
			return nil, fmt.Errorf("tool %q not found", input.ToolName)
		}

		// Check the calling agent's clearance against the TARGET tool.
		rpcErr := mcp.AuthorizeToolCall(ctx, tool, store)
		if rpcErr != nil {
			slog.Info("mcp/dynamic: invoke_tool denied for target",
				"target_tool", input.ToolName,
				"reason", rpcErr.Message,
			)
			return nil, fmt.Errorf("insufficient clearance for %q: %s", input.ToolName, rpcErr.Message)
		}

		slog.Info("mcp/dynamic: invoke_tool dispatching",
			"target_tool", input.ToolName,
		)

		return tool.Handler(ctx, input.Params)
	}
}

// makeListCapabilitiesHandler returns a handler that summarizes tool categories.
func makeListCapabilitiesHandler(registry *mcp.ToolRegistry) mcp.ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		allTools := registry.List()

		// Count tools per category.
		counts := make(map[string]int)
		for _, t := range allTools {
			cat, ok := toolCategoryMap[t.Name]
			if !ok {
				cat = "other"
			}
			counts[cat]++
		}

		// Build response in canonical order.
		var categories []capabilityCategory
		for _, cat := range knownCategories {
			count := counts[cat]
			categories = append(categories, capabilityCategory{
				Category: cat,
				Count:    count,
				Active:   count > 0,
			})
		}

		// Include "other" if there are uncategorized tools.
		if other := counts["other"]; other > 0 {
			categories = append(categories, capabilityCategory{
				Category: "other",
				Count:    other,
				Active:   true,
			})
		}

		return listCapabilitiesResponse{
			Categories: categories,
		}, nil
	}
}

// categoryOf returns the category for a given tool name. Exported for testing.
func categoryOf(name string) string {
	if cat, ok := toolCategoryMap[name]; ok {
		return cat
	}
	// Fallback: use prefix before the first underscore.
	if idx := strings.IndexByte(name, '_'); idx > 0 {
		return name[:idx]
	}
	return "other"
}
