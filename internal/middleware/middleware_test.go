package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
)

// ---------- WithAuth tests ----------

func TestWithAuth_ExtractsHeaderTenant(t *testing.T) {
	t.Parallel()
	var got auth.AuthContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, ok := auth.Get(r.Context())
		if !ok {
			t.Fatal("expected auth context in request context")
		}
		got = ac
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Id", "tenant-abc")
	req.Header.Set("X-Principal-Id", "user-42")
	req.Header.Set("X-Subject-Type", "human")
	req.Header.Set("X-Api-Key-Id", "key-99")
	req.Header.Set("X-Scopes", "read,write")

	rr := httptest.NewRecorder()
	WithAuth(inner).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got.TenantID != "tenant-abc" {
		t.Errorf("TenantID = %q, want %q", got.TenantID, "tenant-abc")
	}
	if got.PrincipalID != "user-42" {
		t.Errorf("PrincipalID = %q, want %q", got.PrincipalID, "user-42")
	}
	if got.SubjectType != "human" {
		t.Errorf("SubjectType = %q, want %q", got.SubjectType, "human")
	}
	if got.APIKeyID != "key-99" {
		t.Errorf("APIKeyID = %q, want %q", got.APIKeyID, "key-99")
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "read" || got.Scopes[1] != "write" {
		t.Errorf("Scopes = %v, want [read write]", got.Scopes)
	}
}

func TestWithAuth_NoHeaders_EmptyContext(t *testing.T) {
	t.Parallel()
	var got auth.AuthContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, ok := auth.Get(r.Context())
		if !ok {
			t.Fatal("expected auth context in request context")
		}
		got = ac
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	WithAuth(inner).ServeHTTP(rr, req)

	if got.TenantID != "" {
		t.Errorf("TenantID = %q, want empty", got.TenantID)
	}
	if got.PrincipalID != "" {
		t.Errorf("PrincipalID = %q, want empty", got.PrincipalID)
	}
}

func TestWithAuth_CallsNext(t *testing.T) {
	t.Parallel()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodPost, "/foo", nil)
	rr := httptest.NewRecorder()
	WithAuth(inner).ServeHTTP(rr, req)

	if !called {
		t.Fatal("inner handler was not called")
	}
	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusTeapot)
	}
}

// ---------- EnsureRequestID tests ----------

func TestEnsureRequestID_GeneratesWhenMissing(t *testing.T) {
	t.Parallel()
	var reqID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID = r.Header.Get("X-Request-Id")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	EnsureRequestID(inner).ServeHTTP(rr, req)

	if reqID == "" {
		t.Fatal("expected X-Request-Id to be set on request")
	}
	if !strings.HasPrefix(reqID, "req_") {
		t.Errorf("expected request ID to start with 'req_', got %q", reqID)
	}
	// Response header should echo the same value.
	respID := rr.Header().Get("X-Request-Id")
	if respID != reqID {
		t.Errorf("response X-Request-Id = %q, want %q", respID, reqID)
	}
}

func TestEnsureRequestID_PreservesExisting(t *testing.T) {
	t.Parallel()
	var reqID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID = r.Header.Get("X-Request-Id")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "my-custom-id")
	rr := httptest.NewRecorder()
	EnsureRequestID(inner).ServeHTTP(rr, req)

	if reqID != "my-custom-id" {
		t.Errorf("request X-Request-Id = %q, want %q", reqID, "my-custom-id")
	}
	if rr.Header().Get("X-Request-Id") != "my-custom-id" {
		t.Errorf("response X-Request-Id = %q, want %q", rr.Header().Get("X-Request-Id"), "my-custom-id")
	}
}

func TestEnsureRequestID_UniquePerRequest(t *testing.T) {
	t.Parallel()
	ids := make(map[string]bool)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		EnsureRequestID(inner).ServeHTTP(rr, req)
		rid := rr.Header().Get("X-Request-Id")
		if ids[rid] {
			t.Fatalf("duplicate request ID generated: %q", rid)
		}
		ids[rid] = true
	}
}

// ---------- ProtectMetrics tests ----------

func TestProtectMetrics_AuthDisabled_PassesThrough(t *testing.T) {
	// Explicitly disable auth (default is now true/restrictive).
	os.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "0")
	defer os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := ProtectMetrics(inner)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("expected inner handler to be called when auth is disabled")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestProtectMetrics_AuthEnabled_NoTenant_Returns401(t *testing.T) {
	os.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	defer os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")
	// Also clear any default tenant.
	os.Unsetenv("AGENTOS_DEFAULT_TENANT")

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := ProtectMetrics(inner)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Fatal("inner handler should NOT be called when tenant is missing")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	// Verify JSON error body.
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' key in response")
	}
	if errObj["code"] != "unauthorized" {
		t.Errorf("error code = %q, want %q", errObj["code"], "unauthorized")
	}
}

func TestProtectMetrics_AuthEnabled_WithTenant_PassesThrough(t *testing.T) {
	os.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	defer os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := ProtectMetrics(inner)

	// Build a request with auth context already set (as WithAuth would do).
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	ac := auth.AuthContext{TenantID: "tenant-xyz"}
	ctx := auth.WithContext(req.Context(), ac)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("expected inner handler to be called with valid tenant")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestProtectMetrics_AuthEnabled_TenantMismatch_Returns400(t *testing.T) {
	os.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	defer os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called on mismatch")
	})

	handler := ProtectMetrics(inner)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	ac := auth.AuthContext{
		TenantID:      "header-tenant",
		TokenTenantID: "token-tenant",
	}
	ctx := auth.WithContext(req.Context(), ac)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' key in response")
	}
	if errObj["code"] != "tenant_mismatch" {
		t.Errorf("error code = %q, want %q", errObj["code"], "tenant_mismatch")
	}
}
