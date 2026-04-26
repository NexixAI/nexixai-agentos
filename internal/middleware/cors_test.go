package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddleware_PreflightReturns204(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://example.com"}}
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for preflight")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/agents", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("expected origin 'https://example.com', got %q", got)
	}
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com", "https://admin.example.com"}}
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("expected origin header, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected credentials=true, got %q", got)
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://allowed.com"}}
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS origin header for disallowed origin, got %q", got)
	}
}

func TestCORSMiddleware_WildcardRejection(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"*"}}
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://anything.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Wildcard with credentials should result in no CORS headers.
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS headers with wildcard+credentials, got origin=%q", got)
	}
}

func TestCORSMiddleware_EmptyConfig(t *testing.T) {
	cfg := CORSConfig{}
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS headers with empty config, got %q", got)
	}
}

func TestCORSMiddleware_MaxAge(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://example.com"}}
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("expected max-age 86400, got %q", got)
	}
}

func TestCORSMiddleware_CredentialsHeader(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://example.com"}}
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected Access-Control-Allow-Credentials=true, got %q", got)
	}
}

func TestParseCORSConfig_CommaSeparated(t *testing.T) {
	cfg := ParseCORSConfig("https://a.com, https://b.com, https://c.com")
	if len(cfg.AllowedOrigins) != 3 {
		t.Fatalf("expected 3 origins, got %d", len(cfg.AllowedOrigins))
	}
	expected := []string{"https://a.com", "https://b.com", "https://c.com"}
	for i, want := range expected {
		if cfg.AllowedOrigins[i] != want {
			t.Errorf("origin[%d]: expected %q, got %q", i, want, cfg.AllowedOrigins[i])
		}
	}
}

func TestParseCORSConfig_Empty(t *testing.T) {
	cfg := ParseCORSConfig("")
	if len(cfg.AllowedOrigins) != 0 {
		t.Errorf("expected 0 origins for empty input, got %d", len(cfg.AllowedOrigins))
	}
}

func TestCORSMiddleware_NoOriginHeader(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://example.com"}}
	called := false
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	// No Origin header set.
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should be called for requests without Origin header")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS headers without Origin, got %q", got)
	}
}
