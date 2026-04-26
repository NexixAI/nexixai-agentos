package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQuery_Success(t *testing.T) {
	promResponse := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [{"metric":{"__name__":"up"},"value":[1616000000,"1"]}]
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("expected path /api/v1/query, got %s", r.URL.Path)
		}
		q := r.URL.Query().Get("query")
		if q != "up" {
			t.Errorf("expected query=up, got %q", q)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(promResponse)) //nolint:errcheck // test helper
	}))
	defer srv.Close()

	client := NewHTTPPrometheusClient(srv.URL)
	result, err := client.Query(context.Background(), "up")
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if result.Status != "success" {
		t.Errorf("expected status %q, got %q", "success", result.Status)
	}
	if result.Data == nil {
		t.Fatal("expected non-nil Data")
	}

	// Verify Data is valid JSON containing the expected structure.
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatalf("failed to unmarshal Data: %v", err)
	}
	if _, ok := data["resultType"]; !ok {
		t.Error("expected resultType in Data")
	}
}

func TestQuery_InvalidPromQL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`)) //nolint:errcheck // test helper
	}))
	defer srv.Close()

	client := NewHTTPPrometheusClient(srv.URL)
	result, err := client.Query(context.Background(), "invalid{{{")
	if err == nil {
		t.Fatal("expected error for invalid PromQL, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result on error, got %+v", result)
	}
}

func TestGetAlerts_ActiveAlerts(t *testing.T) {
	alertsResponse := `{
		"status": "success",
		"data": {
			"alerts": [
				{
					"labels": {"alertname": "HighMemory", "severity": "critical", "instance": "node1:9090"},
					"state": "firing",
					"value": "95.2",
					"activeAt": "2026-03-25T10:00:00Z"
				},
				{
					"labels": {"alertname": "DiskFull", "severity": "warning", "instance": "node2:9090"},
					"state": "pending",
					"value": "88.0",
					"activeAt": "2026-03-25T11:00:00Z"
				}
			]
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/alerts" {
			t.Errorf("expected path /api/v1/alerts, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(alertsResponse)) //nolint:errcheck // test helper
	}))
	defer srv.Close()

	client := NewHTTPPrometheusClient(srv.URL)
	alerts, err := client.GetAlerts(context.Background())
	if err != nil {
		t.Fatalf("GetAlerts returned error: %v", err)
	}

	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}

	// First alert.
	if alerts[0].Name != "HighMemory" {
		t.Errorf("expected alert name %q, got %q", "HighMemory", alerts[0].Name)
	}
	if alerts[0].State != "firing" {
		t.Errorf("expected state %q, got %q", "firing", alerts[0].State)
	}
	if alerts[0].Value != "95.2" {
		t.Errorf("expected value %q, got %q", "95.2", alerts[0].Value)
	}
	if alerts[0].Labels["severity"] != "critical" {
		t.Errorf("expected severity %q, got %q", "critical", alerts[0].Labels["severity"])
	}
	expectedTime, _ := time.Parse(time.RFC3339, "2026-03-25T10:00:00Z")
	if !alerts[0].ActiveAt.Equal(expectedTime) {
		t.Errorf("expected activeAt %v, got %v", expectedTime, alerts[0].ActiveAt)
	}

	// Second alert.
	if alerts[1].Name != "DiskFull" {
		t.Errorf("expected alert name %q, got %q", "DiskFull", alerts[1].Name)
	}
	if alerts[1].State != "pending" {
		t.Errorf("expected state %q, got %q", "pending", alerts[1].State)
	}
}

func TestGetAlerts_NoAlerts(t *testing.T) {
	alertsResponse := `{
		"status": "success",
		"data": {
			"alerts": []
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(alertsResponse)) //nolint:errcheck // test helper
	}))
	defer srv.Close()

	client := NewHTTPPrometheusClient(srv.URL)
	alerts, err := client.GetAlerts(context.Background())
	if err != nil {
		t.Fatalf("GetAlerts returned error: %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alerts))
	}
}

func TestQuery_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the request context is cancelled (simulates slow server).
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := NewHTTPPrometheusClient(srv.URL)
	// Override with a very short timeout for testing.
	client.client.Timeout = 50 * time.Millisecond

	_, err := client.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestNewHTTPPrometheusClient_DefaultURL(t *testing.T) {
	// With no env var and no baseURL, should default to localhost:9090.
	t.Setenv("AGENTOS_PROMETHEUS_URL", "")
	client := NewHTTPPrometheusClient("")
	if client.baseURL != "http://localhost:9090" {
		t.Errorf("expected default base URL %q, got %q", "http://localhost:9090", client.baseURL)
	}
}

func TestNewHTTPPrometheusClient_EnvURL(t *testing.T) {
	t.Setenv("AGENTOS_PROMETHEUS_URL", "http://prom.example.com:9090")
	client := NewHTTPPrometheusClient("")
	if client.baseURL != "http://prom.example.com:9090" {
		t.Errorf("expected env URL %q, got %q", "http://prom.example.com:9090", client.baseURL)
	}
}

func TestNewHTTPPrometheusClient_ExplicitURL(t *testing.T) {
	t.Setenv("AGENTOS_PROMETHEUS_URL", "http://should-not-use.example.com:9090")
	client := NewHTTPPrometheusClient("http://explicit.example.com:9090")
	if client.baseURL != "http://explicit.example.com:9090" {
		t.Errorf("expected explicit URL %q, got %q", "http://explicit.example.com:9090", client.baseURL)
	}
}
