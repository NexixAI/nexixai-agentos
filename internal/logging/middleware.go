package logging

import (
	"net/http"
	"time"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// RequestContextMiddleware generates a request_id, extracts X-Tenant-ID,
// stores both in the request context, and logs the request start and
// completion with method, path, status, and duration.
func RequestContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := NewRequestID()
		tid := r.Header.Get("X-Tenant-ID")

		ctx := WithRequestID(r.Context(), rid)
		ctx = WithTenantID(ctx, tid)
		r = r.WithContext(ctx)

		log := FromContext(ctx)

		log.Info("request started",
			"method", r.Method,
			"path", r.URL.Path,
		)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(rec, r)

		log.Info("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
