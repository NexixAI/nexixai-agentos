package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/httpx"
)

// contextKey is an unexported struct type used as a context value key to
// avoid collisions with keys from other packages (v9.0 M-7).
type contextKey struct{}

var authContextKey = contextKey{}

type AuthContext struct {
	TenantID         string   `json:"tenant_id"`
	PrincipalID      string   `json:"principal_id,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	SubjectType      string   `json:"subject_type,omitempty"`
	APIKeyID         string   `json:"api_key_id,omitempty"`
	Role             string   `json:"role,omitempty"`
	BearerToken      string   `json:"-"`
	TokenTenantID    string   `json:"-"`
	TokenPrincipalID string   `json:"-"`
	// Validated is true when identity was established via a cryptographically
	// verified credential (API key, OIDC token, mTLS). When false, header-
	// derived identity (X-Tenant-Id, X-Agent-ID, etc.) MUST NOT be trusted
	// in production. Only AGENTOS_DEV_MODE=true permits unvalidated headers.
	Validated bool `json:"-"`
}

// DevMode returns true when AGENTOS_DEV_MODE is set. In dev mode, header-
// derived identity is accepted without cryptographic verification.
func DevMode() bool {
	return os.Getenv("AGENTOS_DEV_MODE") == "true"
}

// FromRequest derives auth context from headers and (optionally) a bearer token payload.
// v1.02 policy: tenant_id is primarily derived from auth context; an X-Tenant-Id header may be accepted in dev/demo.
func FromRequest(r *http.Request) AuthContext {
	headerTenant := strings.TrimSpace(r.Header.Get("X-Tenant-Id"))
	headerPrincipal := strings.TrimSpace(r.Header.Get("X-Principal-Id"))
	scopes := parseScopes(r.Header.Get("X-Scopes"))
	subjectType := strings.TrimSpace(r.Header.Get("X-Subject-Type"))
	apiKey := strings.TrimSpace(r.Header.Get("X-Api-Key-Id"))

	ac := AuthContext{
		TenantID:    headerTenant,
		PrincipalID: headerPrincipal,
		Scopes:      scopes,
		SubjectType: subjectType,
		APIKeyID:    apiKey,
	}

	// Store raw bearer token for downstream middleware (OIDC, federation JWT)
	// to verify cryptographically. Do NOT extract claims here — unverified
	// claims must never populate auth context fields.
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
		token := strings.TrimSpace(authz[len("bearer "):])
		if token != "" {
			ac.BearerToken = token
		}
	}

	return ac
}

func WithContext(ctx context.Context, ac AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey, ac)
}

func Get(ctx context.Context) (AuthContext, bool) {
	v := ctx.Value(authContextKey)
	if v == nil {
		return AuthContext{}, false
	}
	ac, ok := v.(AuthContext)
	return ac, ok
}

// DefaultTenant enables "works out of the box" local/dev deployments without a full identity system yet.
func DefaultTenant() string {
	return strings.TrimSpace(os.Getenv("AGENTOS_DEFAULT_TENANT"))
}

var (
	ErrTenantRequired = errors.New("tenant_id required")
	ErrTenantMismatch = errors.New("tenant_id mismatch between token and header")
)

// RequireTenant returns the resolved tenant_id or an error when missing or mismatched.
func RequireTenant(ac AuthContext) (string, error) {
	tokenTenant := strings.TrimSpace(ac.TokenTenantID)
	headerTenant := strings.TrimSpace(ac.TenantID)

	if tokenTenant != "" && headerTenant != "" && tokenTenant != headerTenant {
		return "", ErrTenantMismatch
	}

	if tokenTenant != "" {
		return tokenTenant, nil
	}
	if headerTenant != "" {
		return headerTenant, nil
	}
	if dt := DefaultTenant(); dt != "" {
		return dt, nil
	}
	return "", ErrTenantRequired
}

// ResolveTenantHTTP extracts the tenant ID from the auth context, writing
// an appropriate HTTP error response and returning false when the tenant
// cannot be determined. This is the canonical shared implementation used by
// agentorchestrator, modelpolicy, and federation (v9.0 L-8).
func ResolveTenantHTTP(w http.ResponseWriter, r *http.Request, ac AuthContext) (string, bool) {
	tenantID, err := RequireTenant(ac)
	if err != nil {
		code := http.StatusUnauthorized
		errCode := "unauthorized"
		msg := err.Error()
		retryable := false
		if errors.Is(err, ErrTenantMismatch) {
			code = http.StatusBadRequest
			errCode = "tenant_mismatch"
		}
		httpx.Error(w, code, errCode, msg, httpx.CorrelationID(r), retryable)
		return "", false
	}
	return tenantID, true
}

func parseScopes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, ",", " ")
	parts := strings.Fields(raw)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}


