package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshHandler_Success(t *testing.T) {
	// Mock IdP that returns a new access token.
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode IdP request: %v", err)
		}
		if body["grant_type"] != "refresh_token" {
			t.Errorf("expected grant_type=refresh_token, got %q", body["grant_type"])
		}
		if body["refresh_token"] != "valid-refresh-token" {
			t.Errorf("unexpected refresh_token: %q", body["refresh_token"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]string{
			"access_token": "new-access-token",
			"token_type":   "Bearer",
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to encode IdP response: %v", err)
		}
	}))
	defer idp.Close()

	t.Setenv("AGENTOS_OIDC_TOKEN_ENDPOINT", idp.URL)

	handler := RefreshHandler(idp.Client())

	reqBody, err := json.Marshal(RefreshRequest{RefreshToken: "valid-refresh-token"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["access_token"] != "new-access-token" {
		t.Errorf("expected access_token=new-access-token, got %q", resp["access_token"])
	}
}

func TestRefreshHandler_IdPRejection(t *testing.T) {
	// Mock IdP that rejects the refresh token.
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		resp := map[string]string{"error": "invalid_grant"}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to encode IdP error: %v", err)
		}
	}))
	defer idp.Close()

	t.Setenv("AGENTOS_OIDC_TOKEN_ENDPOINT", idp.URL)

	handler := RefreshHandler(idp.Client())

	reqBody, err := json.Marshal(RefreshRequest{RefreshToken: "expired-token"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp)
	}
	if errObj["code"] != "refresh_denied" {
		t.Errorf("expected code=refresh_denied, got %q", errObj["code"])
	}
}

func TestRefreshHandler_OIDCDisabled(t *testing.T) {
	// Ensure the env var is unset (OIDC disabled).
	t.Setenv("AGENTOS_OIDC_TOKEN_ENDPOINT", "")

	handler := RefreshHandler(nil)

	reqBody, err := json.Marshal(RefreshRequest{RefreshToken: "some-token"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp)
	}
	if errObj["code"] != "not_implemented" {
		t.Errorf("expected code=not_implemented, got %q", errObj["code"])
	}
}

func TestRefreshHandler_MissingRefreshToken(t *testing.T) {
	t.Setenv("AGENTOS_OIDC_TOKEN_ENDPOINT", "http://idp.example.com/token")

	handler := RefreshHandler(nil)

	reqBody, err := json.Marshal(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRefreshHandler_MethodNotAllowed(t *testing.T) {
	t.Setenv("AGENTOS_OIDC_TOKEN_ENDPOINT", "http://idp.example.com/token")

	handler := RefreshHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/refresh", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}
