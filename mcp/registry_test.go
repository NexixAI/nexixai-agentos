package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
)

func dummyHandler(_ context.Context, _ json.RawMessage) (any, error) {
	return "ok", nil
}

func TestRegister(t *testing.T) {
	r := NewToolRegistry()

	tool := Tool{
		Name:        "test-tool",
		Description: "a test tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler:     dummyHandler,
		Static:      true,
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}

	got := r.Get("test-tool")
	if got == nil {
		t.Fatal("Get() returned nil after Register")
	}
	if got.Name != "test-tool" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "test-tool")
	}
	if got.Description != "a test tool" {
		t.Errorf("Get().Description = %q, want %q", got.Description, "a test tool")
	}
	if !got.Static {
		t.Error("Get().Static = false, want true")
	}
}

func TestRegisterEmptyName(t *testing.T) {
	r := NewToolRegistry()
	err := r.Register(Tool{Handler: dummyHandler})
	if err == nil {
		t.Fatal("Register() with empty name should return error")
	}
}

func TestRegisterNilHandler(t *testing.T) {
	r := NewToolRegistry()
	err := r.Register(Tool{Name: "no-handler"})
	if err == nil {
		t.Fatal("Register() with nil handler should return error")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r := NewToolRegistry()

	tool := Tool{
		Name:    "dup-tool",
		Handler: dummyHandler,
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("first Register() returned error: %v", err)
	}

	err := r.Register(tool)
	if err == nil {
		t.Fatal("duplicate Register() should return error")
	}
}

func TestUnregister(t *testing.T) {
	r := NewToolRegistry()

	tool := Tool{
		Name:    "rm-tool",
		Handler: dummyHandler,
	}
	if err := r.Register(tool); err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}

	if err := r.Unregister("rm-tool"); err != nil {
		t.Fatalf("Unregister() returned error: %v", err)
	}

	if got := r.Get("rm-tool"); got != nil {
		t.Error("Get() should return nil after Unregister")
	}
}

func TestUnregisterNotFound(t *testing.T) {
	r := NewToolRegistry()
	err := r.Unregister("nonexistent")
	if err == nil {
		t.Fatal("Unregister() for unknown tool should return error")
	}
}

func TestGetNotFound(t *testing.T) {
	r := NewToolRegistry()
	if got := r.Get("nonexistent"); got != nil {
		t.Errorf("Get() for unknown tool should return nil, got %v", got)
	}
}

func TestListStaticAndDynamic(t *testing.T) {
	r := NewToolRegistry()

	staticTool := Tool{
		Name:    "static-tool",
		Handler: dummyHandler,
		Static:  true,
	}
	dynamicTool := Tool{
		Name:    "dynamic-tool",
		Handler: dummyHandler,
		Static:  false,
	}

	if err := r.Register(staticTool); err != nil {
		t.Fatalf("Register(static) returned error: %v", err)
	}
	if err := r.Register(dynamicTool); err != nil {
		t.Fatalf("Register(dynamic) returned error: %v", err)
	}

	tools := r.List()
	if len(tools) != 2 {
		t.Fatalf("List() returned %d tools, want 2", len(tools))
	}

	// Sort for deterministic comparison.
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	if tools[0].Name != "dynamic-tool" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "dynamic-tool")
	}
	if tools[0].Static {
		t.Error("dynamic tool should have Static=false")
	}
	if tools[1].Name != "static-tool" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "static-tool")
	}
	if !tools[1].Static {
		t.Error("static tool should have Static=true")
	}
}

func TestListEmpty(t *testing.T) {
	r := NewToolRegistry()
	tools := r.List()
	if len(tools) != 0 {
		t.Errorf("List() on empty registry returned %d tools, want 0", len(tools))
	}
}
