package middleware

import (
	"net/http"

	"github.com/NexixAI/nexixai-agentos/internal/id"
)

// EnsureRequestID sets X-Request-Id if missing and echoes it on the response.
func EnsureRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = id.New("req")
			r.Header.Set("X-Request-Id", rid)
		}
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(w, r)
	})
}
