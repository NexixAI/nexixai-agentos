package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// Registry holds registered tools and dispatches execution.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a Registry with all built-in tools registered.
func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	r.Register(&HTTPFetchTool{})
	r.Register(&JSONExtractTool{})
	r.Register(&TextTruncateTool{})
	r.Register(&ShellExecTool{})
	r.Register(&FileReadTool{})
	r.Register(&FileWriteTool{})
	r.Register(&RegexMatchTool{})
	r.Register(&MathEvalTool{})
	r.Register(&AgentInvokeTool{})
	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// GetToolDefs returns OpenAI-format tool definitions. If names is nil or
// empty, all registered tools are returned.
func (r *Registry) GetToolDefs(names []string) []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(names) == 0 {
		defs := make([]ToolDef, 0, len(r.tools))
		for _, t := range r.tools {
			defs = append(defs, toolDef(t))
		}
		return defs
	}

	defs := make([]ToolDef, 0, len(names))
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			defs = append(defs, toolDef(t))
		}
	}
	return defs
}

// Execute dispatches a tool call by name. It always returns a string result
// (never panics). Errors are returned as JSON strings for the model.
func (r *Registry) Execute(ctx context.Context, name string, arguments string) string {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()

	if !ok {
		return fmt.Sprintf(`{"error":"unknown_tool","tool":%q}`, name)
	}

	// Apply default timeout if the context has no deadline.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	result, err := tool.Execute(ctx, arguments)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf(`{"error":"tool_timeout","tool":%q}`, name)
		}
		return fmt.Sprintf(`{"error":"tool_error","message":%q}`, err.Error())
	}
	return result
}

// RegisterCustomTools returns a new Registry clone that includes the given
// custom tools. Returns an error if any custom tool name collides with a
// built-in tool.
func (r *Registry) RegisterCustomTools(customTools []types.CustomTool) (*Registry, error) {
	r.mu.RLock()
	clone := &Registry{tools: make(map[string]Tool, len(r.tools)+len(customTools))}
	for name, tool := range r.tools {
		clone.tools[name] = tool
	}
	builtinNames := make(map[string]bool, len(r.tools))
	for name := range r.tools {
		builtinNames[name] = true
	}
	r.mu.RUnlock()

	for _, ct := range customTools {
		if builtinNames[ct.Name] {
			return nil, fmt.Errorf("custom tool %q collides with built-in tool", ct.Name)
		}
		clone.tools[ct.Name] = NewWebhookTool(ct)
	}
	return clone, nil
}

func toolDef(t Tool) ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFuncDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.InputSchema(),
		},
	}
}
