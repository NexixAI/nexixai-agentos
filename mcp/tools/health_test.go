package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

func TestGetHealth_AllHealthy(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("expected path /models, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[]}`)) //nolint:errcheck // test helper
	}))
	defer vllm.Close()

	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck // test helper
	}))
	defer classifier.Close()

	registry := mcp.NewToolRegistry()
	if err := RegisterHealthTools(registry, vllm.URL, classifier.URL); err != nil {
		t.Fatalf("RegisterHealthTools failed: %v", err)
	}

	tool := registry.Get("get_health")
	if tool == nil {
		t.Fatal("get_health tool not found in registry")
	}

	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	resp, ok := result.(healthResponse)
	if !ok {
		t.Fatalf("expected healthResponse, got %T", result)
	}

	if resp.AgentOS.Status != "healthy" {
		t.Errorf("expected agentos status %q, got %q", "healthy", resp.AgentOS.Status)
	}
	if resp.VLLM.Status != "healthy" {
		t.Errorf("expected vllm status %q, got %q", "healthy", resp.VLLM.Status)
	}
	if resp.Classifier.Status != "healthy" {
		t.Errorf("expected classifier status %q, got %q", "healthy", resp.Classifier.Status)
	}
	if resp.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if resp.VLLM.LatencyMs < 0 {
		t.Errorf("expected non-negative vllm latency, got %d", resp.VLLM.LatencyMs)
	}
	if resp.Classifier.LatencyMs < 0 {
		t.Errorf("expected non-negative classifier latency, got %d", resp.Classifier.LatencyMs)
	}
}

func TestGetHealth_VLLMDown(t *testing.T) {
	// No vLLM server — use a URL that will refuse connections.
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck // test helper
	}))
	defer classifier.Close()

	registry := mcp.NewToolRegistry()
	// Use a closed server to simulate vLLM being down.
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	vllmURL := vllm.URL
	vllm.Close() // close immediately to simulate "down"

	if err := RegisterHealthTools(registry, vllmURL, classifier.URL); err != nil {
		t.Fatalf("RegisterHealthTools failed: %v", err)
	}

	tool := registry.Get("get_health")
	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	resp, ok := result.(healthResponse)
	if !ok {
		t.Fatalf("expected healthResponse, got %T", result)
	}

	if resp.AgentOS.Status != "healthy" {
		t.Errorf("expected agentos status %q, got %q", "healthy", resp.AgentOS.Status)
	}
	if resp.VLLM.Status != "unhealthy" {
		t.Errorf("expected vllm status %q, got %q", "unhealthy", resp.VLLM.Status)
	}
	if resp.VLLM.Error == "" {
		t.Error("expected non-empty vllm error message")
	}
	if resp.Classifier.Status != "healthy" {
		t.Errorf("expected classifier status %q, got %q", "healthy", resp.Classifier.Status)
	}
}

func TestGetHealth_ClassifierDown(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[]}`)) //nolint:errcheck // test helper
	}))
	defer vllm.Close()

	// Use a closed server to simulate classifier being down.
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	classifierURL := classifier.URL
	classifier.Close() // close immediately to simulate "down"

	registry := mcp.NewToolRegistry()
	if err := RegisterHealthTools(registry, vllm.URL, classifierURL); err != nil {
		t.Fatalf("RegisterHealthTools failed: %v", err)
	}

	tool := registry.Get("get_health")
	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	resp, ok := result.(healthResponse)
	if !ok {
		t.Fatalf("expected healthResponse, got %T", result)
	}

	if resp.AgentOS.Status != "healthy" {
		t.Errorf("expected agentos status %q, got %q", "healthy", resp.AgentOS.Status)
	}
	if resp.VLLM.Status != "healthy" {
		t.Errorf("expected vllm status %q, got %q", "healthy", resp.VLLM.Status)
	}
	if resp.Classifier.Status != "unhealthy" {
		t.Errorf("expected classifier status %q, got %q", "unhealthy", resp.Classifier.Status)
	}
	if resp.Classifier.Error == "" {
		t.Error("expected non-empty classifier error message")
	}
}

func TestGetHealth_VLLMServerError(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer vllm.Close()

	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer classifier.Close()

	registry := mcp.NewToolRegistry()
	if err := RegisterHealthTools(registry, vllm.URL, classifier.URL); err != nil {
		t.Fatalf("RegisterHealthTools failed: %v", err)
	}

	tool := registry.Get("get_health")
	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	resp, ok := result.(healthResponse)
	if !ok {
		t.Fatalf("expected healthResponse, got %T", result)
	}

	if resp.VLLM.Status != "unhealthy" {
		t.Errorf("expected vllm status %q, got %q", "unhealthy", resp.VLLM.Status)
	}
	if resp.VLLM.Error == "" {
		t.Error("expected non-empty vllm error for 500 response")
	}
	if resp.Classifier.Status != "healthy" {
		t.Errorf("expected classifier status %q, got %q", "healthy", resp.Classifier.Status)
	}
}

func TestGetHealth_ResponseSerializesToJSON(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer vllm.Close()

	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer classifier.Close()

	registry := mcp.NewToolRegistry()
	if err := RegisterHealthTools(registry, vllm.URL, classifier.URL); err != nil {
		t.Fatalf("RegisterHealthTools failed: %v", err)
	}

	tool := registry.Get("get_health")
	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result JSON: %v", err)
	}

	for _, key := range []string{"agentos", "vllm", "classifier", "timestamp"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing key %q in serialized response", key)
		}
	}
}

func TestGetHealth_ToolRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	if err := RegisterHealthTools(registry, "http://localhost:8000", "http://localhost:8001"); err != nil {
		t.Fatalf("RegisterHealthTools failed: %v", err)
	}

	tool := registry.Get("get_health")
	if tool == nil {
		t.Fatal("get_health tool not found in registry")
	}

	if tool.Name != "get_health" {
		t.Errorf("expected tool name %q, got %q", "get_health", tool.Name)
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

	// Verify InputSchema is valid JSON.
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("invalid InputSchema JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected InputSchema type %q, got %v", "object", schema["type"])
	}
}

func TestGetHealth_DuplicateRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	if err := RegisterHealthTools(registry, "http://localhost:8000", "http://localhost:8001"); err != nil {
		t.Fatalf("first RegisterHealthTools failed: %v", err)
	}
	if err := RegisterHealthTools(registry, "http://localhost:8000", "http://localhost:8001"); err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}
}
