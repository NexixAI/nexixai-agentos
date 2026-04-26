package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte("test-secret-key-for-oidc-tests-32bytes!")

// testKeyFunc returns the HMAC test secret for HS256 tokens.
func testKeyFunc(token *jwt.Token) (any, error) {
	return testSecret, nil
}

// makeSignedJWT creates an HS256-signed JWT for testing.
func makeSignedJWT(claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(testSecret)
	if err != nil {
		panic(err)
	}
	return signed
}

// makeUnsignedJWT creates an alg:none JWT (should always be rejected).
func makeUnsignedJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + encodedPayload + "."
}

func newTestOIDC() *OIDCValidator {
	return newTestValidator(OIDCConfig{
		IssuerURL:   "https://auth.example.com",
		Audience:    "agentos",
		TenantClaim: "tenant_id",
	}, testKeyFunc, []string{"HS256"})
}

func TestOIDCValidator_ValidSignedToken(t *testing.T) {
	v := newTestOIDC()
	token := makeSignedJWT(jwt.MapClaims{
		"iss":       "https://auth.example.com",
		"aud":       "agentos",
		"sub":       "user-123",
		"tenant_id": "tnt_test",
		"exp":       float64(time.Now().Add(time.Hour).Unix()),
	})

	claims, err := v.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims["tenant_id"] != "tnt_test" {
		t.Errorf("expected tenant_id=tnt_test, got %v", claims["tenant_id"])
	}
	if claims["sub"] != "user-123" {
		t.Errorf("expected sub=user-123, got %v", claims["sub"])
	}
}

func TestOIDCValidator_ExpiredToken(t *testing.T) {
	v := newTestOIDC()
	token := makeSignedJWT(jwt.MapClaims{
		"iss": "https://auth.example.com",
		"aud": "agentos",
		"exp": float64(time.Now().Add(-time.Hour).Unix()),
	})

	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestOIDCValidator_MissingExp(t *testing.T) {
	v := newTestOIDC()
	token := makeSignedJWT(jwt.MapClaims{
		"iss": "https://auth.example.com",
		"aud": "agentos",
	})

	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for missing exp claim")
	}
}

func TestOIDCValidator_InvalidSignature(t *testing.T) {
	v := newTestOIDC()
	// Sign with a different key.
	wrongKey := []byte("wrong-secret-key-not-matching!!!!!")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "https://auth.example.com",
		"aud": "agentos",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	signed, _ := token.SignedString(wrongKey)

	_, err := v.ValidateToken(context.Background(), signed)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestOIDCValidator_AlgNone(t *testing.T) {
	v := newTestOIDC()
	token := makeUnsignedJWT(map[string]any{
		"iss": "https://auth.example.com",
		"aud": "agentos",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})

	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for alg:none token")
	}
}

func TestOIDCValidator_WrongIssuer(t *testing.T) {
	v := newTestOIDC()
	token := makeSignedJWT(jwt.MapClaims{
		"iss": "https://evil.com",
		"aud": "agentos",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})

	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestOIDCValidator_WrongAudience(t *testing.T) {
	v := newTestOIDC()
	token := makeSignedJWT(jwt.MapClaims{
		"iss": "https://auth.example.com",
		"aud": "wrong-audience",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})

	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for wrong audience")
	}
}

func TestOIDCValidator_AudienceArray(t *testing.T) {
	v := newTestOIDC()
	token := makeSignedJWT(jwt.MapClaims{
		"iss":       "https://auth.example.com",
		"aud":       []string{"other-app", "agentos"},
		"tenant_id": "tnt_test",
		"exp":       float64(time.Now().Add(time.Hour).Unix()),
	})

	claims, err := v.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims["tenant_id"] != "tnt_test" {
		t.Errorf("expected tenant_id=tnt_test, got %v", claims["tenant_id"])
	}
}

func TestOIDCValidator_NotYetValid(t *testing.T) {
	v := newTestOIDC()
	token := makeSignedJWT(jwt.MapClaims{
		"iss": "https://auth.example.com",
		"aud": "agentos",
		"nbf": float64(time.Now().Add(time.Hour).Unix()),
		"exp": float64(time.Now().Add(2 * time.Hour).Unix()),
	})

	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for not-yet-valid token")
	}
}

func TestOIDCValidator_ExtractTenant(t *testing.T) {
	tests := []struct {
		name        string
		tenantClaim string
		claims      map[string]any
		expect      string
	}{
		{"configured claim", "org_id", map[string]any{"org_id": "org-123"}, "org-123"},
		{"fallback tenant_id", "custom", map[string]any{"tenant_id": "tnt-456"}, "tnt-456"},
		{"fallback tid", "custom", map[string]any{"tid": "tid-789"}, "tid-789"},
		{"no tenant", "custom", map[string]any{"sub": "user"}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := NewOIDCValidator(OIDCConfig{TenantClaim: tc.tenantClaim})
			got := v.ExtractTenantFromClaims(tc.claims)
			if got != tc.expect {
				t.Errorf("expected %q, got %q", tc.expect, got)
			}
		})
	}
}

func TestOIDCConfig_Enabled(t *testing.T) {
	if (OIDCConfig{}).Enabled() {
		t.Error("empty config should not be enabled")
	}
	if !(OIDCConfig{IssuerURL: "https://auth.example.com"}).Enabled() {
		t.Error("config with issuer should be enabled")
	}
}

func TestOIDCValidator_MalformedToken(t *testing.T) {
	v := newTestOIDC()
	_, err := v.ValidateToken(context.Background(), "not-a-jwt")
	if err == nil {
		t.Fatal("expected error for malformed token")
	}
}
