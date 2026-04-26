package middleware

import (
	"errors"
	"net/http"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
)

// ProtectMetrics enforces tenant auth on /metrics if AGENTOS_METRICS_REQUIRE_AUTH=1.
func ProtectMetrics(next http.Handler) http.Handler {
	if !metrics.MetricsRequireAuth() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, _ := auth.Get(r.Context())
		if _, err := auth.RequireTenant(ac); err != nil {
			code := http.StatusUnauthorized
			errCode := "unauthorized"
			if errors.Is(err, auth.ErrTenantMismatch) {
				code = http.StatusBadRequest
				errCode = "tenant_mismatch"
			}
			httpx.Error(w, code, errCode, err.Error(), httpx.CorrelationID(r), false)
			return
		}
		next.ServeHTTP(w, r)
	})
}
