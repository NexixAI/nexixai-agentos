package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// mockAPIKeyStore is a test double for RBACAPIKeyStore.
type mockAPIKeyStore struct {
	record *postgres.APIKeyRecord
	err    error
}

func (m *mockAPIKeyStore) GetByPrefix(_ context.Context, _, _ string) (*postgres.APIKeyRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.record == nil {
		return nil, postgres.ErrAPIKeyNotFound
	}
	return m.record, nil
}

func makeAPIKeyRequest(tenantID, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	ac := auth.AuthContext{TenantID: tenantID, BearerToken: token}
	return req.WithContext(auth.WithContext(req.Context(), ac))
}

func TestAPIKeyMiddleware_ValidKey(t *testing.T) {
	t.Parallel()

	// Generate a real key for proper bcrypt validation.
	_, fullKey, prefix, keyHash, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	store := &mockAPIKeyStore{
		record: &postgres.APIKeyRecord{
			KeyID:     "key_test123",
			TenantID:  "tnt_1",
			KeyHash:   keyHash,
			KeyPrefix: prefix,
			Name:      "test",
			Role:      "developer",
			CreatedBy: "usr_1",
			CreatedAt: time.Now(),
		},
	}

	var gotAC auth.AuthContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAC, _ = auth.Get(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", fullKey))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotAC.PrincipalID != "key_test123" {
		t.Errorf("expected PrincipalID=key_test123, got %q", gotAC.PrincipalID)
	}
	if gotAC.Role != "developer" {
		t.Errorf("expected Role=developer, got %q", gotAC.Role)
	}
	if gotAC.APIKeyID != "key_test123" {
		t.Errorf("expected APIKeyID=key_test123, got %q", gotAC.APIKeyID)
	}
}

func TestAPIKeyMiddleware_MalformedKey_401(t *testing.T) {
	t.Parallel()

	store := &mockAPIKeyStore{}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	// Has aos_live_ prefix but body is too short for prefix extraction.
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", "<AGENTOS_KEY>"))

	if called {
		t.Fatal("malformed API key should not pass through to next handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyMiddleware_NotFound_401(t *testing.T) {
	t.Parallel()

	store := &mockAPIKeyStore{
		record: nil, // will return ErrAPIKeyNotFound
	}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	// Valid format but not in store.
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", "<AGENTOS_KEY>"))

	if called {
		t.Fatal("unknown API key should not pass through to next handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyMiddleware_WrongHash_401(t *testing.T) {
	t.Parallel()

	_, fullKey, prefix, _, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	store := &mockAPIKeyStore{
		record: &postgres.APIKeyRecord{
			KeyID:     "key_wrong",
			TenantID:  "tnt_1",
			KeyHash:   "$2a$10$wronghashwronghashwronghashwronghashwronghashwrong",
			KeyPrefix: prefix,
			Role:      "developer",
			CreatedBy: "usr_1",
			CreatedAt: time.Now(),
		},
	}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", fullKey))

	if called {
		t.Fatal("wrong hash should not pass through to next handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyMiddleware_RevokedKey_401(t *testing.T) {
	t.Parallel()

	_, fullKey, prefix, keyHash, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	revoked := time.Now().Add(-30 * time.Minute)
	store := &mockAPIKeyStore{
		record: &postgres.APIKeyRecord{
			KeyID:     "key_revoked",
			TenantID:  "tnt_1",
			KeyHash:   keyHash,
			KeyPrefix: prefix,
			Role:      "developer",
			CreatedBy: "usr_1",
			CreatedAt: time.Now(),
			RevokedAt: &revoked,
		},
	}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", fullKey))

	if called {
		t.Fatal("revoked API key should not pass through to next handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyMiddleware_ExpiredKey_401(t *testing.T) {
	t.Parallel()

	_, fullKey, prefix, keyHash, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	expired := time.Now().Add(-1 * time.Hour)
	store := &mockAPIKeyStore{
		record: &postgres.APIKeyRecord{
			KeyID:     "key_expired",
			TenantID:  "tnt_1",
			KeyHash:   keyHash,
			KeyPrefix: prefix,
			Role:      "developer",
			CreatedBy: "usr_1",
			CreatedAt: time.Now(),
			ExpiresAt: &expired,
		},
	}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", fullKey))

	if called {
		t.Fatal("expired API key should not pass through to next handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyMiddleware_NonAPIKeyBearer_PassThrough(t *testing.T) {
	t.Parallel()

	store := &mockAPIKeyStore{}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	// Regular JWT token, not an API key.
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", "eyJhbGciOiJSUzI1NiJ9.payload.signature"))

	if !called {
		t.Fatal("expected pass-through for non-API-key bearer")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAPIKeyMiddleware_NoToken_PassThrough(t *testing.T) {
	t.Parallel()

	store := &mockAPIKeyStore{}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(store)
	rec := httptest.NewRecorder()
	// Empty bearer token — no auth token at all.
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", ""))

	if !called {
		t.Fatal("expected pass-through when no token is present")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAPIKeyMiddleware_NilStore_PassThrough(t *testing.T) {
	t.Setenv("AGENTOS_DEV_MODE", "true") // nil store pass-through only in dev mode
	// Cannot use t.Parallel() with t.Setenv

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := APIKeyMiddleware(nil)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeAPIKeyRequest("tnt_1", "<AGENTOS_KEY>"))

	if !called {
		t.Fatal("expected pass-through with nil store")
	}
}
