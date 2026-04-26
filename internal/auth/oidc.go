package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// OIDCConfig holds OIDC provider configuration.
type OIDCConfig struct {
	IssuerURL   string // OIDC issuer URL (e.g., https://auth.example.com)
	Audience    string // Expected audience claim
	TenantClaim string // JWT claim containing tenant ID (default: "tenant_id")
}

// LoadOIDCConfig reads OIDC configuration from environment variables.
func LoadOIDCConfig() OIDCConfig {
	tenantClaim := strings.TrimSpace(os.Getenv("AGENTOS_OIDC_TENANT_CLAIM"))
	if tenantClaim == "" {
		tenantClaim = "tenant_id"
	}
	return OIDCConfig{
		IssuerURL:   strings.TrimSpace(os.Getenv("AGENTOS_OIDC_ISSUER_URL")),
		Audience:    strings.TrimSpace(os.Getenv("AGENTOS_OIDC_AUDIENCE")),
		TenantClaim: tenantClaim,
	}
}

// Enabled returns true if OIDC is configured.
func (c OIDCConfig) Enabled() bool {
	return c.IssuerURL != ""
}

// productionAlgorithms lists the asymmetric signing algorithms accepted in production.
var productionAlgorithms = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
}

// OIDCValidator validates JWT tokens against an OIDC provider's JWKS endpoint.
type OIDCValidator struct {
	cfg          OIDCConfig
	mu           sync.RWMutex
	keyFunc      jwt.Keyfunc
	validMethods []string
	httpClient   *http.Client
}

// NewOIDCValidator creates a validator for the given OIDC configuration.
// It performs OIDC discovery and JWKS initialization on first use (lazy).
func NewOIDCValidator(cfg OIDCConfig) *OIDCValidator {
	return &OIDCValidator{
		cfg:          cfg,
		validMethods: productionAlgorithms,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// newTestValidator creates a validator with a custom key function for testing.
// This skips OIDC discovery and JWKS initialization.
func newTestValidator(cfg OIDCConfig, kf jwt.Keyfunc, methods []string) *OIDCValidator {
	return &OIDCValidator{
		cfg:          cfg,
		keyFunc:      kf,
		validMethods: methods,
	}
}

// oidcDiscoveryResponse is the .well-known/openid-configuration response.
type oidcDiscoveryResponse struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// ValidateToken validates a JWT with cryptographic signature verification.
// Returns the parsed claims if valid, or an error if invalid.
func (v *OIDCValidator) ValidateToken(ctx context.Context, tokenStr string) (map[string]any, error) {
	if err := v.ensureKeyFunc(ctx); err != nil {
		return nil, fmt.Errorf("OIDC not initialized: %w", err)
	}

	// Parse WITH cryptographic signature verification.
	// jwt.WithExpirationRequired rejects tokens without an exp claim.
	// jwt.WithValidMethods restricts accepted signing algorithms.
	// jwt.WithIssuer validates the iss claim.
	token, err := jwt.Parse(tokenStr, v.keyFunc,
		jwt.WithValidMethods(v.validMethods),
		jwt.WithIssuer(v.cfg.IssuerURL),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("token is not valid")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims format")
	}

	// Validate audience if configured.
	if v.cfg.Audience != "" {
		if err := v.validateAudience(claims); err != nil {
			return nil, err
		}
	}

	result := make(map[string]any, len(claims))
	for k, val := range claims {
		result[k] = val
	}
	return result, nil
}

// ensureKeyFunc lazily initializes the JWKS key function via OIDC discovery.
func (v *OIDCValidator) ensureKeyFunc(ctx context.Context) error {
	v.mu.RLock()
	if v.keyFunc != nil {
		v.mu.RUnlock()
		return nil
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.keyFunc != nil {
		return nil
	}

	jwksURL, err := v.discoverJWKS(ctx)
	if err != nil {
		return fmt.Errorf("OIDC discovery failed: %w", err)
	}

	k, err := keyfunc.NewDefault([]string{jwksURL})
	if err != nil {
		return fmt.Errorf("JWKS client init failed: %w", err)
	}
	v.keyFunc = k.Keyfunc
	return nil
}

// discoverJWKS performs OIDC discovery to find the JWKS URI.
func (v *OIDCValidator) discoverJWKS(ctx context.Context) (string, error) {
	discoveryURL := strings.TrimRight(v.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("returned status %d", resp.StatusCode)
	}

	var disc oidcDiscoveryResponse
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return "", fmt.Errorf("decode failed: %w", err)
	}

	slog.Info("OIDC discovery complete", "issuer", disc.Issuer, "jwks_uri", disc.JWKSURI)
	return disc.JWKSURI, nil
}

func (v *OIDCValidator) validateAudience(claims jwt.MapClaims) error {
	switch aud := claims["aud"].(type) {
	case string:
		if aud != v.cfg.Audience {
			return fmt.Errorf("audience mismatch: got %q, expected %q", aud, v.cfg.Audience)
		}
	case []any:
		found := false
		for _, a := range aud {
			if s, ok := a.(string); ok && s == v.cfg.Audience {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("audience %q not found in token", v.cfg.Audience)
		}
	default:
		return fmt.Errorf("missing audience claim")
	}
	return nil
}

// ExtractTenantFromClaims extracts the tenant ID from OIDC JWT claims.
func (v *OIDCValidator) ExtractTenantFromClaims(claims map[string]any) string {
	// Try the configured tenant claim.
	if val, ok := claims[v.cfg.TenantClaim].(string); ok && val != "" {
		return val
	}
	// Fallback to common claim names.
	for _, key := range []string{"tenant_id", "tid", "org_id", "organization_id"} {
		if val, ok := claims[key].(string); ok && val != "" {
			return val
		}
	}
	return ""
}
