package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJSON(t *testing.T) {
	t.Run("writes_status_and_content_type", func(t *testing.T) {
		rec := httptest.NewRecorder()
		body := map[string]string{"hello": "world"}
		JSON(rec, http.StatusCreated, body)

		if rec.Code != http.StatusCreated {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
		}
		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}

		var got map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if got["hello"] != "world" {
			t.Errorf("body[hello] = %q, want %q", got["hello"], "world")
		}
	})
}

func TestError(t *testing.T) {
	t.Run("writes_error_format", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Error(rec, http.StatusBadRequest, "invalid_input", "bad request", "corr-123", false)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}

		var got map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		errObj, ok := got["error"].(map[string]any)
		if !ok {
			t.Fatalf("expected error object in response, got %v", got)
		}
		if errObj["code"] != "invalid_input" {
			t.Errorf("error.code = %v, want %q", errObj["code"], "invalid_input")
		}
		if errObj["message"] != "bad request" {
			t.Errorf("error.message = %v, want %q", errObj["message"], "bad request")
		}
		if errObj["retryable"] != false {
			t.Errorf("error.retryable = %v, want false", errObj["retryable"])
		}
		if got["correlation_id"] != "corr-123" {
			t.Errorf("correlation_id = %v, want %q", got["correlation_id"], "corr-123")
		}
	})
}

func TestCorrelationID(t *testing.T) {
	t.Run("returns_x_correlation_id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Correlation-Id", "abc-123")
		got := CorrelationID(req)
		if got != "abc-123" {
			t.Errorf("CorrelationID = %q, want %q", got, "abc-123")
		}
	})

	t.Run("falls_back_to_x_request_id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Request-Id", "req-456")
		got := CorrelationID(req)
		if got != "req-456" {
			t.Errorf("CorrelationID = %q, want %q", got, "req-456")
		}
	})

	t.Run("returns_empty_when_no_header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		got := CorrelationID(req)
		if got != "" {
			t.Errorf("CorrelationID = %q, want empty", got)
		}
	})

	t.Run("prefers_correlation_over_request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Correlation-Id", "corr")
		req.Header.Set("X-Request-Id", "req")
		got := CorrelationID(req)
		if got != "corr" {
			t.Errorf("CorrelationID = %q, want %q", got, "corr")
		}
	})
}
