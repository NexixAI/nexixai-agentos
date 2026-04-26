package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// mockPrometheusClient is a test double for metrics.PrometheusClient.
type mockPrometheusClient struct {
	queryResult *metrics.QueryResult
	queryErr    error
	alerts      []metrics.Alert
	alertsErr   error

	// Captured calls for verification.
	lastQuery string
}

func (m *mockPrometheusClient) Query(_ context.Context, promql string) (*metrics.QueryResult, error) {
	m.lastQuery = promql
	return m.queryResult, m.queryErr
}

func (m *mockPrometheusClient) GetAlerts(_ context.Context) ([]metrics.Alert, error) {
	return m.alerts, m.alertsErr
}

// defaultDeps creates ObserveDeps for tests that only need the prom client.
func defaultDeps(prom *mockPrometheusClient) ObserveDeps {
	return ObserveDeps{
		PromClient:   prom,
		PortainerURL: "https://localhost:9443/api",
		PortainerKey: "test-key",
		AuditStore:   NewInMemoryAuditStore(100),
	}
}

func TestQueryMetrics_DispatchesToClient(t *testing.T) {
	mock := &mockPrometheusClient{
		queryResult: &metrics.QueryResult{
			Status: "success",
			Data:   json.RawMessage(`{"resultType":"vector","result":[]}`),
		},
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, defaultDeps(mock)); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("query_metrics")
	if tool == nil {
		t.Fatal("query_metrics tool not found in registry")
	}

	params := json.RawMessage(`{"query":"up"}`)
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if mock.lastQuery != "up" {
		t.Errorf("expected query %q dispatched to client, got %q", "up", mock.lastQuery)
	}

	qr, ok := result.(*metrics.QueryResult)
	if !ok {
		t.Fatalf("expected *metrics.QueryResult, got %T", result)
	}
	if qr.Status != "success" {
		t.Errorf("expected status %q, got %q", "success", qr.Status)
	}
}

func TestQueryMetrics_MissingQuery(t *testing.T) {
	mock := &mockPrometheusClient{}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, defaultDeps(mock)); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("query_metrics")

	// Empty JSON object — no "query" field.
	params := json.RawMessage(`{}`)
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing query parameter, got nil")
	}
}

func TestGetAlerts_ReturnsAlerts(t *testing.T) {
	mock := &mockPrometheusClient{
		alerts: []metrics.Alert{
			{
				Name:     "HighCPU",
				State:    "firing",
				Labels:   map[string]string{"alertname": "HighCPU", "severity": "critical"},
				Value:    "92.5",
				ActiveAt: time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC),
			},
		},
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, defaultDeps(mock)); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("get_alerts")
	if tool == nil {
		t.Fatal("get_alerts tool not found in registry")
	}

	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	alerts, ok := result.([]metrics.Alert)
	if !ok {
		t.Fatalf("expected []metrics.Alert, got %T", result)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Name != "HighCPU" {
		t.Errorf("expected alert name %q, got %q", "HighCPU", alerts[0].Name)
	}
	if alerts[0].State != "firing" {
		t.Errorf("expected state %q, got %q", "firing", alerts[0].State)
	}
}

func TestObserveTools_ClearanceAdmin(t *testing.T) {
	mock := &mockPrometheusClient{}
	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, defaultDeps(mock)); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tests := []struct {
		name     string
		toolName string
	}{
		{"query_metrics clearance", "query_metrics"},
		{"get_alerts clearance", "get_alerts"},
		{"get_container_status clearance", "get_container_status"},
		{"get_audit_log clearance", "get_audit_log"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool := registry.Get(tc.toolName)
			if tool == nil {
				t.Fatalf("tool %q not found in registry", tc.toolName)
			}
			if tool.MinClearance != mcp.ClearanceInternal {
				t.Errorf("expected ClearanceInternal (%d), got %d", mcp.ClearanceInternal, tool.MinClearance)
			}
			if !tool.Static {
				t.Error("expected tool to be static")
			}
		})
	}
}

func TestObserveTools_Registration(t *testing.T) {
	mock := &mockPrometheusClient{}
	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, defaultDeps(mock)); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	for _, toolName := range []string{"query_metrics", "get_alerts", "get_container_status", "get_audit_log"} {
		tool := registry.Get(toolName)
		if tool == nil {
			t.Errorf("tool %q not found in registry", toolName)
			continue
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", toolName)
		}

		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Errorf("tool %q has invalid InputSchema JSON: %v", toolName, err)
		}
		if schema["type"] != "object" {
			t.Errorf("tool %q InputSchema type expected %q, got %v", toolName, "object", schema["type"])
		}
	}
}

func TestObserveTools_DuplicateRegistration(t *testing.T) {
	mock := &mockPrometheusClient{}
	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, defaultDeps(mock)); err != nil {
		t.Fatalf("first RegisterObserveTools failed: %v", err)
	}
	if err := RegisterObserveTools(registry, defaultDeps(mock)); err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}
}

// --- get_container_status tests ---

func TestGetContainerStatus_ReturnsContainer(t *testing.T) {
	containers := []portainerContainer{
		{
			Names:   []string{"/nginx-proxy"},
			State:   "running",
			Status:  "Up 3 days",
			Image:   "nginx:1.25",
			Created: 1711411200, // 2024-03-26T00:00:00Z
			Ports:   json.RawMessage(`[{"PrivatePort":80,"PublicPort":8080,"Type":"tcp"}]`),
		},
		{
			Names:   []string{"/redis"},
			State:   "exited",
			Status:  "Exited (0) 2 hours ago",
			Image:   "redis:7",
			Created: 1711411200,
			Ports:   json.RawMessage(`[]`),
		},
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-portainer-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/endpoints/3/docker/containers/json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(containers); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}))
	defer srv.Close()

	// Use the test server's TLS client to bypass self-signed cert.
	origClient := portainerHTTPClient
	portainerHTTPClient = srv.Client()
	defer func() { portainerHTTPClient = origClient }()

	deps := ObserveDeps{
		PromClient:   &mockPrometheusClient{},
		PortainerURL: srv.URL,
		PortainerKey: "test-portainer-key",
		AuditStore:   NewInMemoryAuditStore(100),
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, deps); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("get_container_status")
	if tool == nil {
		t.Fatal("get_container_status tool not found in registry")
	}

	params := json.RawMessage(`{"container_name":"nginx-proxy"}`)
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	csr, ok := result.(containerStatusResult)
	if !ok {
		t.Fatalf("expected containerStatusResult, got %T", result)
	}
	if csr.Name != "nginx-proxy" {
		t.Errorf("expected name %q, got %q", "nginx-proxy", csr.Name)
	}
	if csr.State != "running" {
		t.Errorf("expected state %q, got %q", "running", csr.State)
	}
	if csr.Status != "Up 3 days" {
		t.Errorf("expected status %q, got %q", "Up 3 days", csr.Status)
	}
	if csr.Image != "nginx:1.25" {
		t.Errorf("expected image %q, got %q", "nginx:1.25", csr.Image)
	}
}

func TestGetContainerStatus_NotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return empty container list.
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	origClient := portainerHTTPClient
	portainerHTTPClient = srv.Client()
	defer func() { portainerHTTPClient = origClient }()

	deps := ObserveDeps{
		PromClient:   &mockPrometheusClient{},
		PortainerURL: srv.URL,
		PortainerKey: "test-key",
		AuditStore:   NewInMemoryAuditStore(100),
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, deps); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("get_container_status")
	params := json.RawMessage(`{"container_name":"nonexistent"}`)
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for container not found, got nil")
	}
	if got := err.Error(); got != `get_container_status: container "nonexistent" not found` {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestGetContainerStatus_MissingName(t *testing.T) {
	deps := ObserveDeps{
		PromClient:   &mockPrometheusClient{},
		PortainerURL: "https://localhost:9443/api",
		PortainerKey: "key",
		AuditStore:   NewInMemoryAuditStore(100),
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, deps); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("get_container_status")
	params := json.RawMessage(`{}`)
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing container_name, got nil")
	}
}

// --- get_audit_log tests ---

func TestGetAuditLog_ReturnsEntries(t *testing.T) {
	now := time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC)
	store := NewInMemoryAuditStore(100)
	_ = store.LogDecision(context.Background(), DecisionEntry{
		ID: "1", AgentID: "agent-1", TenantID: "tnt-1",
		Decision: "runs.create", Reasoning: "created run", CreatedAt: now,
	})
	_ = store.LogDecision(context.Background(), DecisionEntry{
		ID: "2", AgentID: "agent-2", TenantID: "tnt-1",
		Decision: "runs.delete", Reasoning: "deleted run", CreatedAt: now.Add(-time.Hour),
	})

	deps := ObserveDeps{
		PromClient:   &mockPrometheusClient{},
		PortainerURL: "https://localhost:9443/api",
		PortainerKey: "key",
		AuditStore:   store,
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, deps); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("get_audit_log")
	if tool == nil {
		t.Fatal("get_audit_log tool not found in registry")
	}

	params := json.RawMessage(`{}`)
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	entries, ok := result.([]AuditEntry)
	if !ok {
		t.Fatalf("expected []AuditEntry, got %T", result)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestGetAuditLog_WithFilters(t *testing.T) {
	store := NewInMemoryAuditStore(100)
	_ = store.LogDecision(context.Background(), DecisionEntry{
		ID: "1", AgentID: "agent-1", TenantID: "tnt-1",
		Decision: "tools.invoke", Reasoning: "query_metrics", CreatedAt: time.Now(),
	})

	deps := ObserveDeps{
		PromClient:   &mockPrometheusClient{},
		PortainerURL: "https://localhost:9443/api",
		PortainerKey: "key",
		AuditStore:   store,
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, deps); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("get_audit_log")

	params := json.RawMessage(`{"agent_id":"agent-1","tenant_id":"tnt-1","limit":10}`)
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	entries, ok := result.([]AuditEntry)
	if !ok {
		t.Fatalf("expected []AuditEntry, got %T", result)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestGetAuditLog_NilStore(t *testing.T) {
	deps := ObserveDeps{
		PromClient:   &mockPrometheusClient{},
		PortainerURL: "https://localhost:9443/api",
		PortainerKey: "key",
		AuditStore:   nil,
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterObserveTools(registry, deps); err != nil {
		t.Fatalf("RegisterObserveTools failed: %v", err)
	}

	tool := registry.Get("get_audit_log")
	params := json.RawMessage(`{}`)
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for nil audit store, got nil")
	}
}
