package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireTenantPrefersTokenAndRejectsMismatch(t *testing.T) {
	ac := AuthContext{TenantID: "tnt_header", TokenTenantID: "tnt_token"}
	if _, err := RequireTenant(ac); err == nil || !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch, got %v", err)
	}
}

func TestRequireTenantFromBearerToken(t *testing.T) {
	ac := AuthContext{TokenTenantID: "tnt_token", TokenPrincipalID: "usr_token"}
	tenant, err := RequireTenant(ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "tnt_token" {
		t.Fatalf("unexpected tenant: %s", tenant)
	}
}

func TestFromRequestStoresBearerTokenWithoutExtractingClaims(t *testing.T) {
	token := makeJWT(map[string]any{
		"tenant_id":    "tnt_jwt",
		"principal_id": "usr_jwt",
	})

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ac := FromRequest(req)

	// Bearer token should be stored for downstream middleware to verify.
	if ac.BearerToken != token {
		t.Fatalf("expected bearer token to be stored, got empty")
	}
	// Claims must NOT be extracted from unverified tokens.
	if ac.TokenTenantID != "" {
		t.Fatalf("expected empty TokenTenantID (no unverified extraction), got %s", ac.TokenTenantID)
	}
	if ac.TokenPrincipalID != "" {
		t.Fatalf("expected empty TokenPrincipalID (no unverified extraction), got %s", ac.TokenPrincipalID)
	}
	if ac.TenantID != "" {
		t.Fatalf("expected empty TenantID (no header set, no unverified extraction), got %s", ac.TenantID)
	}
}

func makeJWT(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return strings.Join([]string{"hdr", payload, "sig"}, ".")
}
