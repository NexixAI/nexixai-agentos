package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// mockMemberStore is a test double for RBACMemberStore.
type mockMemberStore struct {
	roles   map[string]string // key: "tenantID|principalID"
	counts  map[string]int    // key: tenantID
	err     error             // injected error for GetRole
	setErr  error             // injected error for SetRole
	setCalled bool
}

func newMockMemberStore() *mockMemberStore {
	return &mockMemberStore{
		roles:  make(map[string]string),
		counts: make(map[string]int),
	}
}

func (m *mockMemberStore) GetRole(_ context.Context, tenantID, principalID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	key := tenantID + "|" + principalID
	role, ok := m.roles[key]
	if !ok {
		return "", postgres.ErrMemberNotFound
	}
	return role, nil
}

func (m *mockMemberStore) CountMembers(_ context.Context, tenantID string) (int, error) {
	return m.counts[tenantID], nil
}

func (m *mockMemberStore) SetRole(_ context.Context, tenantID, principalID, role string) error {
	m.setCalled = true
	if m.setErr != nil {
		return m.setErr
	}
	m.roles[tenantID+"|"+principalID] = role
	return nil
}

func makeRBACRequest(tenantID, principalID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	ac := auth.AuthContext{TenantID: tenantID, PrincipalID: principalID}
	return req.WithContext(auth.WithContext(req.Context(), ac))
}

func TestRBACMiddleware_SufficientRole_Passes(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()
	store.roles["tnt_1|usr_1"] = "developer"

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := RBACMiddleware(store, "viewer")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_1", "usr_1"))

	if !called {
		t.Fatal("expected inner handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRBACMiddleware_InsufficientRole_403(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()
	store.roles["tnt_1|usr_1"] = "viewer"

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner should not be called")
	})

	mw := RBACMiddleware(store, "developer")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_1", "usr_1"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestRBACMiddleware_DBError_403(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()
	store.err = errors.New("db connection lost")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner should not be called")
	})

	mw := RBACMiddleware(store, "viewer")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_1", "usr_1"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on DB error, got %d", rec.Code)
	}
}

func TestRBACMiddleware_FirstUserBootstrap(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()
	// No roles set, count is 0 → should auto-assign owner.
	store.counts["tnt_new"] = 0

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ac, _ := auth.Get(r.Context())
		if ac.Role != "owner" {
			t.Errorf("expected role=owner after bootstrap, got %q", ac.Role)
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := RBACMiddleware(store, "owner")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_new", "usr_first"))

	if !called {
		t.Fatal("expected inner handler to be called for first-user bootstrap")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after bootstrap, got %d", rec.Code)
	}
	if !store.setCalled {
		t.Fatal("expected SetRole to be called for bootstrap")
	}
}

func TestRBACMiddleware_NoMembers_ButTenantHasMembers_403(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()
	// Tenant has members, but this principal is not one of them.
	store.counts["tnt_full"] = 5

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner should not be called")
	})

	mw := RBACMiddleware(store, "viewer")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_full", "usr_outsider"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestRBACMiddleware_NilPrincipal_403(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner should not be called")
	})

	mw := RBACMiddleware(store, "viewer")
	rec := httptest.NewRecorder()
	// Principal is empty.
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_1", ""))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for nil principal, got %d", rec.Code)
	}
}

func TestRBACMiddleware_NilStore_PassThrough(t *testing.T) {
	t.Setenv("AGENTOS_DEV_MODE", "true") // nil store pass-through only in dev mode

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := RBACMiddleware(nil, "owner")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_1", "usr_1"))

	if !called {
		t.Fatal("expected pass-through with nil store")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRBACMiddleware_BootstrapSetRoleError_403(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()
	// Tenant has 0 members (bootstrap path), but SetRole fails.
	store.counts["tnt_broken"] = 0
	store.setErr = errors.New("disk full")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner should not be called when SetRole fails")
	})

	mw := RBACMiddleware(store, "viewer")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, makeRBACRequest("tnt_broken", "usr_unlucky"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when SetRole fails during bootstrap, got %d", rec.Code)
	}
}

func TestRBACMiddleware_NoTenant_403(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner should not be called without tenant")
	})

	// Request with principal but no tenant.
	req := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	ac := auth.AuthContext{PrincipalID: "usr_orphan"}
	req = req.WithContext(auth.WithContext(req.Context(), ac))

	mw := RBACMiddleware(store, "viewer")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing tenant, got %d", rec.Code)
	}
}

func TestRBACMiddleware_RoleFromAuthContext_SkipsMemberLookup(t *testing.T) {
	t.Parallel()
	store := newMockMemberStore()
	// No roles in the store; role comes from auth context (e.g. API key).

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	ac := auth.AuthContext{TenantID: "tnt_1", PrincipalID: "key_abc", Role: "admin"}
	req = req.WithContext(auth.WithContext(req.Context(), ac))

	mw := RBACMiddleware(store, "developer")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected inner handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
