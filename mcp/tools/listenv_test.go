package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

func TestListenv_Registration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	if err := RegisterListenvTool(registry); err != nil {
		t.Fatalf("RegisterListenvTool failed: %v", err)
	}

	tool := registry.Get("list_env")
	if tool == nil {
		t.Fatal("list_env tool not found in registry")
	}
	if tool.Name != "list_env" {
		t.Errorf("expected tool name %q, got %q", "list_env", tool.Name)
	}
	if tool.MinClearance != mcp.ClearanceAdmin {
		t.Errorf("expected ClearanceAdmin, got %d", tool.MinClearance)
	}
	if !tool.Static {
		t.Error("expected tool to be static")
	}
	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("invalid InputSchema JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected InputSchema type %q, got %v", "object", schema["type"])
	}
}

func TestListenv_Success(t *testing.T) {
	fakeEnviron := func() []string {
		return []string{
			"HOME=/home/user",
			"PATH=/usr/bin",
			"GOPATH=/home/user/go",
		}
	}

	handler := makeListenvHandler(fakeEnviron)
	result, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	res, ok := result.(listenvResult)
	if !ok {
		t.Fatalf("expected listenvResult, got %T", result)
	}
	if res.Count != 3 {
		t.Errorf("expected count 3, got %d", res.Count)
	}

	// Names should be sorted.
	expected := []string{"GOPATH", "HOME", "PATH"}
	if len(res.Names) != len(expected) {
		t.Fatalf("expected %d names, got %d", len(expected), len(res.Names))
	}
	for i, name := range expected {
		if res.Names[i] != name {
			t.Errorf("expected names[%d]=%q, got %q", i, name, res.Names[i])
		}
	}
}

func TestListenv_NoValues(t *testing.T) {
	// Verify that values are NOT included in the output.
	fakeEnviron := func() []string {
		return []string{"SECRET_KEY=supersecret123"}
	}

	handler := makeListenvHandler(fakeEnviron)
	result, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, "supersecret123") {
		t.Error("result JSON contains the env value — only names should be returned")
	}

	res := result.(listenvResult)
	if len(res.Names) != 1 || res.Names[0] != "SECRET_KEY" {
		t.Errorf("expected [SECRET_KEY], got %v", res.Names)
	}
}

func TestListenv_EmptyEnvironment(t *testing.T) {
	fakeEnviron := func() []string { return nil }

	handler := makeListenvHandler(fakeEnviron)
	_, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty environment, got nil")
	}
}

func TestListenv_SkipsMalformedEntries(t *testing.T) {
	fakeEnviron := func() []string {
		return []string{
			"GOOD=value",
			"noequals",
			"=emptykey",
			"ALSO_GOOD=another",
		}
	}

	handler := makeListenvHandler(fakeEnviron)
	result, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	res := result.(listenvResult)
	if res.Count != 2 {
		t.Errorf("expected 2 valid names, got %d: %v", res.Count, res.Names)
	}
}

func TestListenv_ResponseSerializesToJSON(t *testing.T) {
	fakeEnviron := func() []string {
		return []string{"A=1", "B=2"}
	}

	handler := makeListenvHandler(fakeEnviron)
	result, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result JSON: %v", err)
	}
	if _, ok := parsed["names"]; !ok {
		t.Error("missing key 'names' in serialized response")
	}
	if _, ok := parsed["count"]; !ok {
		t.Error("missing key 'count' in serialized response")
	}
}

func TestListenv_DuplicateRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	if err := RegisterListenvTool(registry); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	if err := RegisterListenvTool(registry); err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}
}

