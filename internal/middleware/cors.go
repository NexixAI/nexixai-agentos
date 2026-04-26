package middleware

import (
	"log/slog"
	"net/http"
	"strings"
)

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	AllowedOrigins []string
}

// ParseCORSConfig creates a CORSConfig from the AGENTOS_CORS_ORIGINS
// environment variable value (comma-separated origins).
func ParseCORSConfig(origins string) CORSConfig {
	if strings.TrimSpace(origins) == "" {
		return CORSConfig{}
	}
	parts := strings.Split(origins, ",")
	var allowed []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			allowed = append(allowed, p)
		}
	}
	return CORSConfig{AllowedOrigins: allowed}
}

// CORSMiddleware returns HTTP middleware that handles CORS headers.
// If no origins are configured, no CORS headers are added (pass through).
// Wildcard "*" is rejected when credentials are true (always true for
// explicitly listed origins) to comply with browser security requirements.
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
	if len(cfg.AllowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	// Check for wildcard + credentials conflict.
	hasWildcard := false
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			hasWildcard = true
			break
		}
	}
	if hasWildcard {
		slog.Error("CORS misconfiguration: wildcard '*' is not allowed with credentials=true; CORS headers will not be sent")
		return func(next http.Handler) http.Handler { return next }
	}

	// Build lookup set for O(1) origin matching.
	originSet := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		originSet[o] = struct{}{}
	}

	const (
		allowMethods = "GET, POST, PUT, DELETE, OPTIONS"
		allowHeaders = "Authorization, Content-Type, X-Tenant-Id, X-Request-Id, X-Correlation-Id"
		maxAge       = "86400"
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if _, ok := originSet[origin]; !ok {
				// Origin not allowed; do not set any CORS headers.
				next.ServeHTTP(w, r)
				return
			}

			// Origin is allowed — set CORS headers.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", allowMethods)
			w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
			w.Header().Set("Access-Control-Max-Age", maxAge)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")

			// Handle preflight.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
