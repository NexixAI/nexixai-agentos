package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// makePromResponse builds a Prometheus instant-query JSON response body
// with a single vector result containing the given value.
func makePromResponse(value string) []byte {
	resp := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result": []map[string]any{
				{
					"metric": map[string]string{},
					"value":  [2]any{1717000000.0, value},
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func makePromEmpty() []byte {
	resp := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result":     []any{},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestGPUVRAMCheck_HighVRAM_ZeroRequests_Degraded(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if q != "" && callCount == 1 {
			// First call: VRAM utilization = 0.95 (95%)
			w.Write(makePromResponse("0.95"))
		} else {
			// Second call: active requests = 0
			w.Write(makePromResponse("0"))
		}
	}))
	defer srv.Close()

	check := GPUVRAMCheck("gpu_vram", false, srv.URL)
	result := check.Fn(context.Background())

	if result.Status != StatusDegraded {
		t.Fatalf("expected status %q, got %q (error: %s)", StatusDegraded, result.Status, result.Error)
	}
	if result.Error == "" {
		t.Fatal("expected non-empty error message for degraded check")
	}
}

func TestGPUVRAMCheck_HighVRAM_ActiveRequests_Healthy(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// VRAM utilization = 0.95
			w.Write(makePromResponse("0.95"))
		} else {
			// Active requests = 5
			w.Write(makePromResponse("5"))
		}
	}))
	defer srv.Close()

	check := GPUVRAMCheck("gpu_vram", false, srv.URL)
	result := check.Fn(context.Background())

	if result.Status != StatusHealthy {
		t.Fatalf("expected status %q, got %q (error: %s)", StatusHealthy, result.Status, result.Error)
	}
}

func TestGPUVRAMCheck_LowVRAM_Healthy(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// VRAM utilization = 0.50
			w.Write(makePromResponse("0.50"))
		} else {
			// Active requests = 0
			w.Write(makePromResponse("0"))
		}
	}))
	defer srv.Close()

	check := GPUVRAMCheck("gpu_vram", false, srv.URL)
	result := check.Fn(context.Background())

	if result.Status != StatusHealthy {
		t.Fatalf("expected status %q, got %q (error: %s)", StatusHealthy, result.Status, result.Error)
	}
}

func TestGPUVRAMCheck_PrometheusDown_Degraded(t *testing.T) {
	check := GPUVRAMCheck("gpu_vram", false, "http://127.0.0.1:1") // unreachable
	result := check.Fn(context.Background())

	if result.Status != StatusDegraded {
		t.Fatalf("expected status %q when prometheus unreachable, got %q", StatusDegraded, result.Status)
	}
}

func TestGPUVRAMCheck_NoVRAMData_Degraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(makePromEmpty())
	}))
	defer srv.Close()

	check := GPUVRAMCheck("gpu_vram", false, srv.URL)
	result := check.Fn(context.Background())

	if result.Status != StatusDegraded {
		t.Fatalf("expected status %q when no VRAM data, got %q", StatusDegraded, result.Status)
	}
}

func TestGPUVRAMCheck_RequestsQueryFails_HighVRAM_Degraded(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// VRAM = 0.95
			w.Write(makePromResponse("0.95"))
		} else {
			// Requests query returns empty (no vLLM metrics)
			w.Write(makePromEmpty())
		}
	}))
	defer srv.Close()

	check := GPUVRAMCheck("gpu_vram", false, srv.URL)
	result := check.Fn(context.Background())

	if result.Status != StatusDegraded {
		t.Fatalf("expected status %q, got %q (error: %s)", StatusDegraded, result.Status, result.Error)
	}
}

func TestGPUVRAMCheck_RequestsQueryFails_LowVRAM_Healthy(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// VRAM = 0.50
			w.Write(makePromResponse("0.50"))
		} else {
			// Requests query returns empty
			w.Write(makePromEmpty())
		}
	}))
	defer srv.Close()

	check := GPUVRAMCheck("gpu_vram", false, srv.URL)
	result := check.Fn(context.Background())

	if result.Status != StatusHealthy {
		t.Fatalf("expected status %q, got %q (error: %s)", StatusHealthy, result.Status, result.Error)
	}
}

func TestQueryPrometheusScalar_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(makePromResponse("42.5"))
	}))
	defer srv.Close()

	val, err := queryPrometheusScalar(context.Background(), srv.Client(), srv.URL, "up")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 42.5 {
		t.Fatalf("expected 42.5, got %f", val)
	}
}

func TestQueryPrometheusScalar_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(makePromEmpty())
	}))
	defer srv.Close()

	_, err := queryPrometheusScalar(context.Background(), srv.Client(), srv.URL, "missing_metric")
	if err == nil {
		t.Fatal("expected error for empty result, got nil")
	}
}
