package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	internalAuth "github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// RBACAPIKeyStore is the interface consumed by APIKeyMiddleware.
// It is satisfied by postgres.PgAPIKeyStore (and by test doubles).
type RBACAPIKeyStore interface {
	GetByPrefix(ctx context.Context, tenantID, prefix string) (*postgres.APIKeyRecord, error)
}

// APIKeyPrefixLookup is an optional interface for stores that support
// looking up keys by prefix alone (without requiring a tenant_id).
// This enables API key auth without a pre-existing tenant context.
type APIKeyPrefixLookup interface {
	LookupByPrefix(ctx context.Context, prefix string) (*postgres.APIKeyRecord, error)
}

// APIKeyMiddleware returns middleware that authenticates requests bearing
// an AgentOS API key (Authorization: Bearer aos_live_*).
//
// Fail-closed policy (v8.2 #9): if the token looks like an API key
// (has the "aos_live_" prefix) but fails validation for any reason
// (malformed, not found, wrong hash, revoked, expired), the middleware
// returns 401 Unauthorized immediately — it does NOT pass through.
//
// If the token is NOT an API key it passes through to the next handler
// (letting OIDC or other auth middleware try).
// If NO token is present, it passes through.
// If store is nil the middleware is a no-op pass-through.
func APIKeyMiddleware(store RBACAPIKeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store == nil {
				if internalAuth.DevMode() {
					next.ServeHTTP(w, r)
					return
				}
				slog.Error("apikey: store is nil in production mode — denying API key auth",
					"path", r.URL.Path)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			ac, _ := internalAuth.Get(r.Context())
			token := ac.BearerToken

			// No token or not an API key → pass through for OIDC to handle.
			if !internalAuth.IsAPIKey(token) {
				next.ServeHTTP(w, r)
				return
			}

			// From here on, we know the token looks like an API key.
			// Any validation failure must fail closed (401).

			prefix := internalAuth.ExtractPrefix(token)
			if prefix == "" {
				// Malformed API key (right prefix, body too short) → 401.
				slog.Warn("apikey: malformed key (prefix extraction failed)",
					"token_prefix", token[:min(len(token), 20)])
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Look up the API key. If a tenant is already known (from header
			// or OIDC), use the scoped lookup. Otherwise, fall back to
			// prefix-only lookup — the API key itself provides the tenant.
			var rec *postgres.APIKeyRecord
			var err error
			tenantID, tenantErr := internalAuth.RequireTenant(ac)
			if tenantErr == nil {
				rec, err = store.GetByPrefix(r.Context(), tenantID, prefix)
			} else if lookup, ok := store.(APIKeyPrefixLookup); ok {
				rec, err = lookup.LookupByPrefix(r.Context(), prefix)
			} else {
				// No tenant and no prefix-only lookup → 401.
				slog.Warn("apikey: no tenant context and no prefix-only lookup available",
					"prefix", prefix)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			if err != nil {
				slog.Debug("apikey: key not found by prefix",
					"prefix", prefix, "error", err)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Validate bcrypt hash.
			if !internalAuth.ValidateKey(token, rec.KeyHash) {
				slog.Warn("apikey: hash mismatch",
					"prefix", prefix, "tenant_id", rec.TenantID, "key_id", rec.KeyID)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Check revocation.
			if rec.RevokedAt != nil {
				slog.Warn("apikey: key revoked",
					"key_id", rec.KeyID, "tenant_id", rec.TenantID)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Check expiry.
			if rec.ExpiresAt != nil && rec.ExpiresAt.Before(time.Now()) {
				slog.Warn("apikey: key expired",
					"key_id", rec.KeyID, "tenant_id", rec.TenantID,
					"expires_at", rec.ExpiresAt)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Valid API key: populate auth context.
			ac.TenantID = rec.TenantID
			ac.PrincipalID = rec.KeyID
			ac.Role = rec.Role
			ac.APIKeyID = rec.KeyID
			ac.Validated = true
			r = r.WithContext(internalAuth.WithContext(r.Context(), ac))
			next.ServeHTTP(w, r)
		})
	}
}
