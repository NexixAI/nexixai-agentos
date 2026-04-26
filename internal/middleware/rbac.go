package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// RBACMemberStore is the interface consumed by RBACMiddleware.
// It is satisfied by postgres.PgMemberStore (and by test doubles).
type RBACMemberStore interface {
	GetRole(ctx context.Context, tenantID, principalID string) (string, error)
	CountMembers(ctx context.Context, tenantID string) (int, error)
	SetRole(ctx context.Context, tenantID, principalID, role string) error
}

// RBACMiddleware returns middleware that enforces role-based access control.
// If store is nil the middleware is a no-op pass-through (allows tests and
// dev deployments to run without a member store).
func RBACMiddleware(store RBACMemberStore, requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no store configured: pass through in dev mode, deny in prod.
			if store == nil {
				if auth.DevMode() {
					next.ServeHTTP(w, r)
					return
				}
				slog.Error("rbac: store is nil in production mode — denying request",
					"path", r.URL.Path, "method", r.Method)
				httpx.Error(w, http.StatusForbidden, "forbidden",
					"RBAC not configured", httpx.CorrelationID(r), false)
				return
			}

			ac, ok := auth.Get(r.Context())
			if !ok || ac.PrincipalID == "" {
				slog.Warn("rbac: denied, no principal in auth context",
					"path", r.URL.Path, "method", r.Method)
				httpx.Error(w, http.StatusForbidden, "forbidden",
					"authentication required", httpx.CorrelationID(r), false)
				return
			}

			tenantID, err := auth.RequireTenant(ac)
			if err != nil {
				slog.Warn("rbac: denied, tenant resolution failed",
					"error", err, "path", r.URL.Path)
				httpx.Error(w, http.StatusForbidden, "forbidden",
					"tenant required", httpx.CorrelationID(r), false)
				return
			}

			// If the auth context already has a role (e.g. from API key middleware),
			// use it directly instead of querying the member store.
			role := ac.Role
			if role == "" {
				role, err = store.GetRole(r.Context(), tenantID, ac.PrincipalID)
				if err != nil {
					if errors.Is(err, postgres.ErrMemberNotFound) {
						// First-user bootstrap: if tenant has 0 members, auto-assign owner.
						count, countErr := store.CountMembers(r.Context(), tenantID)
						if countErr != nil {
							slog.Error("rbac: denied, count members failed",
								"error", countErr, "tenant_id", tenantID)
							httpx.Error(w, http.StatusForbidden, "forbidden",
								"access denied", httpx.CorrelationID(r), false)
							return
						}
						if count == 0 {
							if setErr := store.SetRole(r.Context(), tenantID, ac.PrincipalID, string(auth.RoleOwner)); setErr != nil {
								slog.Error("rbac: denied, auto-assign owner failed",
									"error", setErr, "tenant_id", tenantID, "principal_id", ac.PrincipalID)
								httpx.Error(w, http.StatusForbidden, "forbidden",
									"access denied", httpx.CorrelationID(r), false)
								return
							}
							slog.Info("rbac: first-user bootstrap, assigned owner",
								"tenant_id", tenantID, "principal_id", ac.PrincipalID)
							role = string(auth.RoleOwner)
						} else {
							slog.Warn("rbac: denied, no membership row",
								"tenant_id", tenantID, "principal_id", ac.PrincipalID,
								"path", r.URL.Path)
							httpx.Error(w, http.StatusForbidden, "forbidden",
								"not a member of this tenant", httpx.CorrelationID(r), false)
							return
						}
					} else {
						// DB error → fail closed.
						slog.Error("rbac: denied, member store error",
							"error", err, "tenant_id", tenantID,
							"principal_id", ac.PrincipalID)
						httpx.Error(w, http.StatusForbidden, "forbidden",
							"access denied", httpx.CorrelationID(r), false)
						return
					}
				}
			}

			if !auth.HasPermission(auth.Role(role), auth.Role(requiredRole)) {
				slog.Warn("rbac: denied, insufficient role",
					"tenant_id", tenantID, "principal_id", ac.PrincipalID,
					"user_role", role, "required_role", requiredRole,
					"path", r.URL.Path)
				httpx.Error(w, http.StatusForbidden, "forbidden",
					"insufficient permissions", httpx.CorrelationID(r), false)
				return
			}

			// Enrich auth context with role for downstream handlers.
			ac.Role = role
			r = r.WithContext(auth.WithContext(r.Context(), ac))
			next.ServeHTTP(w, r)
		})
	}
}
