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

// ---------- WithAuth — OIDC validation branch ----------
//
// These tests mutate the package-level oidcValidator variable, so they run
// sequentially inside a parent test to avoid racing with other parallel tests
// that read the variable.

func TestWithAuth_OIDC(t *testing.T) {
	// Save and restore the package-level validator once for all sub-tests.
	saved := oidcValidator
	t.Cleanup(func() { oidcValidator = saved })

	t.Run("enabled_no_bearer_passes_through", func(t *testing.T) {
		oidcValidator = auth.NewOIDCValidator(auth.OIDCConfig{
			IssuerURL: "https://fake-issuer.example.com",
			Audience:  "test-aud",
		})

		called := false
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})

		// No Authorization header → BearerToken is empty → middleware should pass through.
		req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
		req.Header.Set("X-Tenant-Id", "tenant-1")
		rr := httptest.NewRecorder()
		WithAuth(inner).ServeHTTP(rr, req)

		if !called {
			t.Fatal("inner handler was not called; expected pass-through with no bearer token")
		}
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("enabled_invalid_bearer_returns_401", func(t *testing.T) {
		// Use an issuer URL that will fail OIDC discovery (no real server).
		oidcValidator = auth.NewOIDCValidator(auth.OIDCConfig{
			IssuerURL: "https://fake-issuer.invalid",
			Audience:  "test-aud",
		})

		called := false
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})

		req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
		req.Header.Set("Authorization", "Bearer some-garbage-token")
		rr := httptest.NewRecorder()
		WithAuth(inner).ServeHTTP(rr, req)

		if called {
			t.Fatal("inner handler should NOT be called when bearer token validation fails")
		}
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rr.Code)
		}
		body := strings.TrimSpace(rr.Body.String())
		if !strings.Contains(body, "Unauthorized") {
			t.Errorf("expected 'Unauthorized' in body, got %q", body)
		}
	})

	t.Run("disabled_bearer_token_ignored", func(t *testing.T) {
		oidcValidator = nil

		var gotAC auth.AuthContext
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ac, ok := auth.Get(r.Context())
			if !ok {
				t.Fatal("expected auth context")
			}
			gotAC = ac
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
		req.Header.Set("X-Tenant-Id", "tenant-abc")
		req.Header.Set("Authorization", "Bearer this-token-is-not-validated")
		rr := httptest.NewRecorder()
		WithAuth(inner).ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if gotAC.TenantID != "tenant-abc" {
			t.Errorf("TenantID = %q, want %q", gotAC.TenantID, "tenant-abc")
		}
		// BearerToken should be captured in the auth context (for other middleware).
		if gotAC.BearerToken != "this-token-is-not-validated" {
			t.Errorf("BearerToken = %q, want %q", gotAC.BearerToken, "this-token-is-not-validated")
		}
	})

	t.Run("sets_auth_context_fields", func(t *testing.T) {
		oidcValidator = nil

		var gotAC auth.AuthContext
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ac, ok := auth.Get(r.Context())
			if !ok {
				t.Fatal("expected auth context in request context")
			}
			gotAC = ac
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Tenant-Id", "t1")
		req.Header.Set("X-Principal-Id", "p1")
		req.Header.Set("X-Subject-Type", "service")
		req.Header.Set("X-Api-Key-Id", "key-1")
		req.Header.Set("X-Scopes", "read write")
		rr := httptest.NewRecorder()
		WithAuth(inner).ServeHTTP(rr, req)

		if gotAC.TenantID != "t1" {
			t.Errorf("TenantID = %q, want %q", gotAC.TenantID, "t1")
		}
		if gotAC.PrincipalID != "p1" {
			t.Errorf("PrincipalID = %q, want %q", gotAC.PrincipalID, "p1")
		}
		if gotAC.SubjectType != "service" {
			t.Errorf("SubjectType = %q, want %q", gotAC.SubjectType, "service")
		}
		if gotAC.APIKeyID != "key-1" {
			t.Errorf("APIKeyID = %q, want %q", gotAC.APIKeyID, "key-1")
		}
		if len(gotAC.Scopes) != 2 {
			t.Errorf("Scopes len = %d, want 2", len(gotAC.Scopes))
		}
	})
}

// ---------- ProtectMetrics — additional coverage ----------

func TestProtectMetrics_AuthEnabled_NoAuthContext_Returns401(t *testing.T) {
	os.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	defer os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")
	os.Unsetenv("AGENTOS_DEFAULT_TENANT")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called")
	})

	handler := ProtectMetrics(inner)

	// Request with no auth context at all (no WithAuth middleware ran).
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

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

func TestProtectMetrics_AuthEnabled_EmptyTenant_Returns401(t *testing.T) {
	os.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	defer os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")
	os.Unsetenv("AGENTOS_DEFAULT_TENANT")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called")
	})

	handler := ProtectMetrics(inner)

	// Auth context with empty tenant.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	ac := auth.AuthContext{TenantID: "", PrincipalID: "user-1"}
	ctx := auth.WithContext(req.Context(), ac)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestProtectMetrics_AuthDisabled_NoAuthContext_StillPasses(t *testing.T) {
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
}

// ---------- EnsureRequestID — additional coverage ----------

func TestEnsureRequestID_SetsResponseHeader_EvenOnError(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	EnsureRequestID(inner).ServeHTTP(rr, req)

	rid := rr.Header().Get("X-Request-Id")
	if rid == "" {
		t.Fatal("expected X-Request-Id on response even for error status")
	}
	if !strings.HasPrefix(rid, "req_") {
		t.Errorf("expected prefix 'req_', got %q", rid)
	}
}

func TestEnsureRequestID_PreservesExact_CustomID(t *testing.T) {
	t.Parallel()
	const customID = "custom-correlation-abc-123"

	var innerRID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerRID = r.Header.Get("X-Request-Id")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", customID)
	rr := httptest.NewRecorder()
	EnsureRequestID(inner).ServeHTTP(rr, req)

	if innerRID != customID {
		t.Errorf("inner saw X-Request-Id = %q, want %q", innerRID, customID)
	}
	if rr.Header().Get("X-Request-Id") != customID {
		t.Errorf("response X-Request-Id = %q, want %q", rr.Header().Get("X-Request-Id"), customID)
	}
}

func TestEnsureRequestID_RequestAndResponseMatch(t *testing.T) {
	t.Parallel()
	var reqRID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqRID = r.Header.Get("X-Request-Id")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	EnsureRequestID(inner).ServeHTTP(rr, req)

	respRID := rr.Header().Get("X-Request-Id")
	if reqRID == "" || respRID == "" {
		t.Fatal("expected both request and response to have X-Request-Id")
	}
	if reqRID != respRID {
		t.Errorf("request ID %q != response ID %q", reqRID, respRID)
	}
}
