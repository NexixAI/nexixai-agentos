package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewChecker_NoChecks_Healthy(t *testing.T) {
	c := NewChecker("v1.025")
	resp := c.Run(context.Background())

	if resp.Status != StatusHealthy {
		t.Fatalf("expected status %q, got %q", StatusHealthy, resp.Status)
	}
	if resp.Version != "v1.025" {
		t.Fatalf("expected version %q, got %q", "v1.025", resp.Version)
	}
	if len(resp.Checks) != 0 {
		t.Fatalf("expected 0 checks, got %d", len(resp.Checks))
	}
}

func TestChecker_AllPassing_Healthy200(t *testing.T) {
	c := NewChecker("v1.025")
	c.Register(Check{
		Name:     "db",
		Required: true,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusHealthy, LatencyMs: 1}
		},
	})
	c.Register(Check{
		Name:     "cache",
		Required: false,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusHealthy, LatencyMs: 2}
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	c.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != StatusHealthy {
		t.Fatalf("expected status %q, got %q", StatusHealthy, resp.Status)
	}
	if len(resp.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(resp.Checks))
	}
}

func TestChecker_RequiredFailing_Unhealthy503(t *testing.T) {
	c := NewChecker("v1.025")
	c.Register(Check{
		Name:     "db",
		Required: true,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusUnhealthy, Error: "connection refused"}
		},
	})
	c.Register(Check{
		Name:     "cache",
		Required: false,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusHealthy}
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	c.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != StatusUnhealthy {
		t.Fatalf("expected status %q, got %q", StatusUnhealthy, resp.Status)
	}
	if resp.Checks["db"].Error != "connection refused" {
		t.Fatalf("expected db error %q, got %q", "connection refused", resp.Checks["db"].Error)
	}
}

func TestChecker_OptionalFailing_Degraded200(t *testing.T) {
	c := NewChecker("v1.025")
	c.Register(Check{
		Name:     "db",
		Required: true,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusHealthy}
		},
	})
	c.Register(Check{
		Name:     "cache",
		Required: false,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusUnhealthy, Error: "cache down"}
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	c.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != StatusDegraded {
		t.Fatalf("expected status %q, got %q", StatusDegraded, resp.Status)
	}
}

func TestChecker_JSONStructure(t *testing.T) {
	c := NewChecker("v1.025")
	c.Register(Check{
		Name:     "store",
		Required: true,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusHealthy, LatencyMs: 5}
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	c.Handler().ServeHTTP(rec, req)

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode raw JSON: %v", err)
	}

	// Verify top-level keys exist
	for _, key := range []string{"status", "version", "checks"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing top-level key %q", key)
		}
	}

	// Verify status and version values
	var status string
	json.Unmarshal(raw["status"], &status)
	if status != "healthy" {
		t.Fatalf("expected status %q, got %q", "healthy", status)
	}

	var version string
	json.Unmarshal(raw["version"], &version)
	if version != "v1.025" {
		t.Fatalf("expected version %q, got %q", "v1.025", version)
	}

	// Verify checks structure
	var checks map[string]json.RawMessage
	json.Unmarshal(raw["checks"], &checks)
	if _, ok := checks["store"]; !ok {
		t.Fatalf("missing check %q in checks map", "store")
	}
}

func TestChecker_ReadyHandler_NotReady_503(t *testing.T) {
	c := NewChecker("v1.07")
	// Not calling SetReady(true), so default is not ready.

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503 when not ready, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != StatusUnhealthy {
		t.Fatalf("expected status %q, got %q", StatusUnhealthy, resp.Status)
	}
	if resp.Checks["startup"].Error != "service starting" {
		t.Fatalf("expected startup error, got %q", resp.Checks["startup"].Error)
	}
}

func TestChecker_ReadyHandler_Ready_200(t *testing.T) {
	c := NewChecker("v1.07")
	c.Register(Check{
		Name:     "db",
		Required: true,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusHealthy, LatencyMs: 1}
		},
	})
	c.SetReady(true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 when ready, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != StatusHealthy {
		t.Fatalf("expected status %q, got %q", StatusHealthy, resp.Status)
	}
}

func TestChecker_ReadyHandler_ReadyButUnhealthy_503(t *testing.T) {
	c := NewChecker("v1.07")
	c.Register(Check{
		Name:     "db",
		Required: true,
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{Status: StatusUnhealthy, Error: "db down"}
		},
	})
	c.SetReady(true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503 when ready but unhealthy, got %d", rec.Code)
	}
}

func TestChecker_ReadyHandler_MethodNotAllowed(t *testing.T) {
	c := NewChecker("v1.07")
	c.SetReady(true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/ready", nil)
	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected HTTP 405, got %d", rec.Code)
	}
}

func TestChecker_SetReady_Toggle(t *testing.T) {
	c := NewChecker("v1.07")

	// Initially not ready.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	c.ReadyHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 initially, got %d", rec.Code)
	}

	// Set ready.
	c.SetReady(true)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	c.ReadyHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after SetReady(true), got %d", rec.Code)
	}

	// Set not ready again.
	c.SetReady(false)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	c.ReadyHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after SetReady(false), got %d", rec.Code)
	}
}
