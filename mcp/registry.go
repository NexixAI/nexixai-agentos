package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// ToolHandler is the function signature for a tool implementation.
type ToolHandler func(ctx context.Context, params json.RawMessage) (any, error)

// Tool is a registered tool in the MCP tool registry.
type Tool struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	Handler      ToolHandler
	MinClearance ClearanceTier
	Static       bool   // true = built-in, false = dynamically registered
	Category     string // "observe" or "action" for split rate limiting; defaults to "action"
}

// ToolRegistry manages the set of tools available via the MCP server.
// It is safe for concurrent use.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*Tool),
	}
}

// Register adds a tool to the registry. It returns an error if a tool
// with the same name is already registered.
func (r *ToolRegistry) Register(t Tool) error {
	if t.Name == "" {
		return fmt.Errorf("mcp: tool name must not be empty")
	}
	if t.Handler == nil {
		return fmt.Errorf("mcp: tool %q must have a handler", t.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[t.Name]; exists {
		return fmt.Errorf("mcp: tool %q is already registered", t.Name)
	}

	r.tools[t.Name] = &t
	slog.Info("mcp: tool registered", "tool", t.Name, "static", t.Static)
	return nil
}

// Unregister removes a tool from the registry by name. It returns an error
// if no tool with that name exists.
func (r *ToolRegistry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return fmt.Errorf("mcp: tool %q not found", name)
	}

	delete(r.tools, name)
	slog.Info("mcp: tool unregistered", "tool", name)
	return nil
}

// Get returns the tool with the given name, or nil if not found.
func (r *ToolRegistry) Get(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List returns all registered tools (both static and dynamic).
// The returned slice is a snapshot; mutations to the registry after
// List returns are not reflected.
func (r *ToolRegistry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, *t)
	}
	return out
}
