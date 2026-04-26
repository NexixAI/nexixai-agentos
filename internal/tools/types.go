package tools

import "context"

// Tool is the interface that all tools must implement.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, input string) (string, error)
}

// ToolDef is the OpenAI-format tool definition sent to model providers.
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function ToolFuncDef `json:"function"`
}

// ToolFuncDef describes a tool's function signature for the model.
type ToolFuncDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
