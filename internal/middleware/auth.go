package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
)

// oidcValidator is the package-level OIDC validator, initialized once.
var oidcValidator *auth.OIDCValidator

func init() {
	cfg := auth.LoadOIDCConfig()
	if cfg.Enabled() {
		oidcValidator = auth.NewOIDCValidator(cfg)
		slog.Info("OIDC validation enabled", "issuer", cfg.IssuerURL)
	}
}

func WithAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac := auth.FromRequest(r)

		// If OIDC is configured and a bearer token is present, validate it.
		// Skip OIDC validation for API keys — they are handled by APIKeyMiddleware.
		if oidcValidator != nil && ac.BearerToken != "" && !auth.IsAPIKey(ac.BearerToken) {
			claims, err := oidcValidator.ValidateToken(r.Context(), ac.BearerToken)
			if err != nil {
				slog.Warn("OIDC token validation failed", "error", err,
					"path", r.URL.Path, "method", r.Method)
				// Fail closed: bearer token present but invalid → 401.
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			} else {
				ac.Validated = true
				// Extract tenant from OIDC claims if not already set.
				if ac.TenantID == "" {
					if tenant := oidcValidator.ExtractTenantFromClaims(claims); tenant != "" {
						ac.TenantID = tenant
						ac.TokenTenantID = tenant
					}
				}
				// Extract principal from standard claims.
				if ac.PrincipalID == "" {
					if sub, ok := claims["sub"].(string); ok && sub != "" {
						ac.PrincipalID = strings.TrimSpace(sub)
						ac.TokenPrincipalID = ac.PrincipalID
					}
				}
				// Extract scopes from OIDC token.
				if len(ac.Scopes) == 0 {
					if scope, ok := claims["scope"].(string); ok {
						ac.Scopes = strings.Fields(scope)
					}
				}
			}
		}

		r = r.WithContext(auth.WithContext(r.Context(), ac))
		next.ServeHTTP(w, r)
	})
}
