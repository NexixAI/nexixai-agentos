package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

func Error(w http.ResponseWriter, status int, code, message, correlationID string, retryable bool) {
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryable,
		},
		"correlation_id": correlationID,
	})
}

func CorrelationID(r *http.Request) string {
	// Prefer X-Correlation-Id; fallback to X-Request-Id; else empty.
	if v := r.Header.Get("X-Correlation-Id"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Request-Id"); v != "" {
		return v
	}
	return ""
}

func RequestIDHeader(w http.ResponseWriter, id string) {
	if id != "" {
		w.Header().Set("X-Request-Id", id)
	}
}

