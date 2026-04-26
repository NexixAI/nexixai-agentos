package federation

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTVerifyValidToken(t *testing.T) {
	// Generate test RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	verifier := &JWTVerifier{publicKey: &privateKey.PublicKey}

	// Create a valid token
	claims := jwtClaimsPayload{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user@example.com",
			Issuer:    "agentos",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		TenantID:    "tenant_123",
		PrincipalID: "principal_456",
	}

	token := createTestTokenJWT(t, claims, privateKey)

	result, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if result.TenantID != "tenant_123" {
		t.Errorf("expected tenant_id tenant_123, got %s", result.TenantID)
	}
	if result.PrincipalID != "principal_456" {
		t.Errorf("expected principal_id principal_456, got %s", result.PrincipalID)
	}
	if result.Subject != "user@example.com" {
		t.Errorf("expected subject user@example.com, got %s", result.Subject)
	}
}

func TestJWTVerifyExpiredToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	verifier := &JWTVerifier{publicKey: &privateKey.PublicKey}

	claims := jwtClaimsPayload{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
		TenantID: "tenant_123",
	}

	token := createTestTokenJWT(t, claims, privateKey)

	_, err = verifier.Verify(token)
	if err != ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestJWTVerifyInvalidSignature(t *testing.T) {
	// Generate two different key pairs
	privateKey1, _ := rsa.GenerateKey(rand.Reader, 2048)
	privateKey2, _ := rsa.GenerateKey(rand.Reader, 2048)

	// Verifier uses key 2's public key
	verifier := &JWTVerifier{publicKey: &privateKey2.PublicKey}

	// Token signed with key 1
	claims := jwtClaimsPayload{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		TenantID: "tenant_123",
	}

	token := createTestTokenJWT(t, claims, privateKey1)

	_, err := verifier.Verify(token)
	if err == nil {
		t.Error("expected signature verification to fail")
	}
}

func TestJWTVerifyMalformedToken(t *testing.T) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	verifier := &JWTVerifier{publicKey: &privateKey.PublicKey}

	testCases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no dots", "invalidtoken"},
		{"one dot", "part1.part2"},
		{"too many parts", "a.b.c.d"},
		{"invalid base64 header", "!!!.abc.def"},
		{"invalid base64 payload", "eyJhbGciOiJSUzI1NiJ9.!!!.def"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifier.Verify(tc.token)
			if err != ErrInvalidToken {
				t.Errorf("expected ErrInvalidToken for %s, got %v", tc.name, err)
			}
		})
	}
}

func TestJWTVerifyECDSA(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}

	verifier := &JWTVerifier{publicKey: &privateKey.PublicKey}

	claims := jwtClaimsPayload{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		TenantID: "tenant_ecdsa",
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token, err := tok.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	result, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if result.TenantID != "tenant_ecdsa" {
		t.Errorf("expected tenant_id tenant_ecdsa, got %s", result.TenantID)
	}
}

func TestJWTMiddlewarePassthrough(t *testing.T) {
	// Dev mode: nil verifier passes through only with explicit opt-in.
	os.Setenv("AGENTOS_FED_AUTH_DISABLED", "1")
	defer os.Unsetenv("AGENTOS_FED_AUTH_DISABLED")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := JWTMiddleware(nil, handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK with nil verifier in dev mode, got %d", rec.Code)
	}
}

func TestJWTMiddlewareNilVerifierRejectsWithoutDevMode(t *testing.T) {
	os.Unsetenv("AGENTOS_FED_AUTH_DISABLED")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := JWTMiddleware(nil, handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 with nil verifier and no dev mode, got %d", rec.Code)
	}
}

func TestJWTMiddlewareMissingToken(t *testing.T) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	verifier := &JWTVerifier{publicKey: &privateKey.PublicKey}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := JWTMiddleware(verifier, handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without token, got %d", rec.Code)
	}
}

func TestJWTMiddlewareSetsHeaders(t *testing.T) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	verifier := &JWTVerifier{publicKey: &privateKey.PublicKey}

	var capturedTenantID, capturedPrincipalID, capturedSubject string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTenantID = r.Header.Get("X-JWT-Tenant-Id")
		capturedPrincipalID = r.Header.Get("X-JWT-Principal-Id")
		capturedSubject = r.Header.Get("X-JWT-Subject")
		w.WriteHeader(http.StatusOK)
	})

	wrapped := JWTMiddleware(verifier, handler)

	claims := jwtClaimsPayload{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "sub_test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		TenantID:    "t_test",
		PrincipalID: "p_test",
	}
	token := createTestTokenJWT(t, claims, privateKey)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if capturedTenantID != "t_test" {
		t.Errorf("expected X-JWT-Tenant-Id t_test, got %s", capturedTenantID)
	}
	if capturedPrincipalID != "p_test" {
		t.Errorf("expected X-JWT-Principal-Id p_test, got %s", capturedPrincipalID)
	}
	if capturedSubject != "sub_test" {
		t.Errorf("expected X-JWT-Subject sub_test, got %s", capturedSubject)
	}
}

// Helper function to create a test RSA-signed JWT token using golang-jwt
func createTestTokenJWT(t *testing.T, claims jwtClaimsPayload, privateKey *rsa.PrivateKey) string {
	t.Helper()

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token, err := tok.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return token
}
