package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSendRunCompletedEvent_Success(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := WebhookConfig{URL: srv.URL, Timeout: 5 * time.Second}
	event := RunCompletedEvent{
		TenantID:     "tnt_test",
		RunID:        "run_123",
		Model:        "gpt-4o",
		InputTokens:  100,
		OutputTokens: 200,
		DurationMs:   1500,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}

	SendRunCompletedEvent(context.Background(), cfg, event)

	if received.Load() != 1 {
		t.Errorf("expected 1 delivery, got %d", received.Load())
	}
}

func TestSendRunCompletedEvent_RetryOn500(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := WebhookConfig{URL: srv.URL, Timeout: 2 * time.Second}
	event := RunCompletedEvent{
		TenantID: "tnt_test",
		RunID:    "run_retry",
	}

	// Use a short context to keep test fast — but context must outlive retries.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	SendRunCompletedEvent(ctx, cfg, event)

	if attempts.Load() < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempts.Load())
	}
}

func TestSendRunCompletedEvent_EmptyURL(t *testing.T) {
	// Should be a no-op with empty URL.
	cfg := WebhookConfig{URL: "", Timeout: 5 * time.Second}
	event := RunCompletedEvent{RunID: "run_noop"}

	// No panic, no error.
	SendRunCompletedEvent(context.Background(), cfg, event)
}

func TestSendRunCompletedEvent_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := WebhookConfig{URL: srv.URL, Timeout: 100 * time.Millisecond}
	event := RunCompletedEvent{RunID: "run_timeout"}

	// Cancel context quickly so retries also abort.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Should not hang — the short timeout + context should cause it to give up.
	SendRunCompletedEvent(ctx, cfg, event)
}
