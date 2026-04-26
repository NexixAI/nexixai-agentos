package federation

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken      = errors.New("invalid token")
	ErrTokenExpired      = errors.New("token expired")
	ErrInvalidSignature  = errors.New("invalid signature")
	ErrUnsupportedAlg    = errors.New("unsupported algorithm")
	ErrPublicKeyNotFound = errors.New("public key not found")
)

// JWTClaims represents the claims extracted from a JWT token.
type JWTClaims struct {
	TenantID    string `json:"tenant_id"`
	PrincipalID string `json:"principal_id"`
	Subject     string `json:"sub"`
	Issuer      string `json:"iss"`
	Audience    string `json:"aud"`
	ExpiresAt   int64  `json:"exp"`
	IssuedAt    int64  `json:"iat"`
}

// JWTVerifier verifies JWT tokens using a public key.
type JWTVerifier struct {
	publicKey crypto.PublicKey
}

// NewJWTVerifier creates a JWT verifier from environment configuration.
// Returns (nil, nil) only if AGENTOS_FED_AUTH_DISABLED=1 is explicitly set (dev mode).
// Returns an error if the public key is configured but cannot be loaded.
func NewJWTVerifier() (*JWTVerifier, error) {
	// Explicit dev mode opt-in — the only way to get nil verifier without error.
	if strings.TrimSpace(os.Getenv("AGENTOS_FED_AUTH_DISABLED")) == "1" {
		return nil, nil
	}

	publicKeyPath := strings.TrimSpace(os.Getenv("AGENTOS_FED_JWT_PUBLIC_KEY"))
	if publicKeyPath == "" {
		// No key configured and dev mode not explicitly enabled — fail closed.
		return nil, errors.New("AGENTOS_FED_JWT_PUBLIC_KEY not set; set AGENTOS_FED_AUTH_DISABLED=1 for dev mode")
	}

	keyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key %s: %w", publicKeyPath, err)
	}

	publicKey, err := parsePublicKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	return &JWTVerifier{publicKey: publicKey}, nil
}

// parsePublicKey parses a PEM-encoded public key.
func parsePublicKey(data []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	switch block.Type {
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return nil, errors.New("unsupported key type: " + block.Type)
	}
}

// jwtClaimsPayload is the full claims type used for parsing with golang-jwt.
type jwtClaimsPayload struct {
	jwt.RegisteredClaims
	TenantID    string `json:"tenant_id"`
	PrincipalID string `json:"principal_id"`
}

// Verify verifies a JWT token and extracts claims.
func (v *JWTVerifier) Verify(tokenStr string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwtClaimsPayload{}, func(token *jwt.Token) (any, error) {
		return v.publicKey, nil
	}, jwt.WithValidMethods([]string{"RS256", "ES256"}))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		if errors.Is(err, jwt.ErrTokenSignatureInvalid) {
			return nil, ErrInvalidSignature
		}
		if errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, ErrInvalidToken
		}
		return nil, ErrInvalidToken
	}

	parsed, ok := token.Claims.(*jwtClaimsPayload)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	claims := &JWTClaims{
		TenantID:    parsed.TenantID,
		PrincipalID: parsed.PrincipalID,
		Subject:     parsed.Subject,
		Issuer:      parsed.Issuer,
	}

	if len(parsed.Audience) > 0 {
		claims.Audience = parsed.Audience[0]
	}

	if parsed.ExpiresAt != nil {
		claims.ExpiresAt = parsed.ExpiresAt.Unix()
	}

	if parsed.IssuedAt != nil {
		claims.IssuedAt = parsed.IssuedAt.Unix()
	}

	return claims, nil
}

// JWTMiddleware returns an HTTP middleware that verifies JWT tokens.
// Passes through only if AGENTOS_FED_AUTH_DISABLED=1 is explicitly set.
// If verifier is nil without explicit dev mode, all requests are rejected.
func JWTMiddleware(verifier *JWTVerifier, next http.Handler) http.Handler {
	devMode := strings.TrimSpace(os.Getenv("AGENTOS_FED_AUTH_DISABLED")) == "1"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if verifier == nil {
			if devMode {
				next.ServeHTTP(w, r)
				return
			}
			httpx.Error(w, http.StatusServiceUnavailable, "auth_unavailable", "federation auth not configured", httpx.CorrelationID(r), false)
			return
		}

		// Extract bearer token
		authHeader := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			token = strings.TrimSpace(authHeader[7:])
		}

		if token == "" {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing bearer token", httpx.CorrelationID(r), false)
			return
		}

		// Verify token
		claims, err := verifier.Verify(token)
		if err != nil {
			errMsg := "invalid token"
			if errors.Is(err, ErrTokenExpired) {
				errMsg = "token expired"
			}
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", errMsg, httpx.CorrelationID(r), false)
			return
		}

		// Add claims to headers for downstream processing
		if claims.TenantID != "" {
			r.Header.Set("X-JWT-Tenant-Id", claims.TenantID)
		}
		if claims.PrincipalID != "" {
			r.Header.Set("X-JWT-Principal-Id", claims.PrincipalID)
		}
		if claims.Subject != "" {
			r.Header.Set("X-JWT-Subject", claims.Subject)
		}

		next.ServeHTTP(w, r)
	})
}
