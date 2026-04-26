package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

func TestServerUptime_Registration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	if err := RegisterServerUptimeTool(registry); err != nil {
		t.Fatalf("RegisterServerUptimeTool failed: %v", err)
	}

	tool := registry.Get("server_uptime")
	if tool == nil {
		t.Fatal("server_uptime tool not found in registry")
	}
	if tool.Name != "server_uptime" {
		t.Errorf("expected tool name %q, got %q", "server_uptime", tool.Name)
	}
	if tool.MinClearance != mcp.ClearancePublic {
		t.Errorf("expected ClearancePublic, got %d", tool.MinClearance)
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

func TestServerUptime_Success(t *testing.T) {
	// Create a fake /proc/uptime file.
	dir := t.TempDir()
	fakeUptime := filepath.Join(dir, "uptime")
	if err := os.WriteFile(fakeUptime, []byte("12345.67 98765.43\n"), 0644); err != nil {
		t.Fatalf("failed to write fake uptime: %v", err)
	}

	handler := makeServerUptimeHandler(fakeUptime)
	result, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	res, ok := result.(serverUptimeResult)
	if !ok {
		t.Fatalf("expected serverUptimeResult, got %T", result)
	}
	if res.UptimeSeconds != 12345.67 {
		t.Errorf("expected uptime 12345.67, got %f", res.UptimeSeconds)
	}
}

func TestServerUptime_FileNotFound(t *testing.T) {
	handler := makeServerUptimeHandler("/nonexistent/path/uptime")
	_, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestServerUptime_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	fakeUptime := filepath.Join(dir, "uptime")
	if err := os.WriteFile(fakeUptime, []byte("not-a-number 123\n"), 0644); err != nil {
		t.Fatalf("failed to write fake uptime: %v", err)
	}

	handler := makeServerUptimeHandler(fakeUptime)
	_, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
}

func TestServerUptime_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	fakeUptime := filepath.Join(dir, "uptime")
	if err := os.WriteFile(fakeUptime, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write fake uptime: %v", err)
	}

	handler := makeServerUptimeHandler(fakeUptime)
	_, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestServerUptime_ResponseSerializesToJSON(t *testing.T) {
	dir := t.TempDir()
	fakeUptime := filepath.Join(dir, "uptime")
	if err := os.WriteFile(fakeUptime, []byte("99.99 50.00\n"), 0644); err != nil {
		t.Fatalf("failed to write fake uptime: %v", err)
	}

	handler := makeServerUptimeHandler(fakeUptime)
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
	if _, ok := parsed["uptime_seconds"]; !ok {
		t.Error("missing key uptime_seconds in serialized response")
	}
}

func TestServerUptime_DuplicateRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	if err := RegisterServerUptimeTool(registry); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	if err := RegisterServerUptimeTool(registry); err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}
}
