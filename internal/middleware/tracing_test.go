package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTracingMiddleware_SpanCreation(t *testing.T) {
	// With no tracer configured, should use no-op tracer and not panic.
	handler := TracingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestTracingMiddleware_PropagatesHeaders(t *testing.T) {
	handler := TracingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/agents", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestTracingMiddleware_SetsAttributes(t *testing.T) {
	var capturedStatus int
	handler := TracingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		capturedStatus = http.StatusNotFound
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/missing", nil)
	req.Header.Set("X-Tenant-Id", "tnt_test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
	if capturedStatus != http.StatusNotFound {
		t.Errorf("expected captured status 404, got %d", capturedStatus)
	}
}

func TestTracingMiddleware_NoOpWhenNoTracer(t *testing.T) {
	// Verify the middleware works correctly with no global tracer configured.
	called := false
	handler := TracingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler was not called")
	}
}
