package tenants

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

func TestHandleDelete_MissingConfirmHeader(t *testing.T) {
	store := &mockTenantStore{
		getFn: func(_ context.Context, _ string) (*postgres.Tenant, error) {
			return &postgres.Tenant{
				TenantID:  "tnt_acme_12345678",
				Name:      "Acme",
				Slug:      "acme",
				Plan:      "starter",
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}, nil
		},
	}

	api := NewDeleteAPI(store, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/tnt_acme_12345678", nil)
	req = withAuthContext(req)
	// No X-Confirm-Delete header
	w := httptest.NewRecorder()

	api.HandleDelete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleDelete_WrongConfirmHeader(t *testing.T) {
	store := &mockTenantStore{
		getFn: func(_ context.Context, _ string) (*postgres.Tenant, error) {
			return &postgres.Tenant{
				TenantID:  "tnt_acme_12345678",
				Name:      "Acme",
				Slug:      "acme",
				Plan:      "starter",
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}, nil
		},
	}

	api := NewDeleteAPI(store, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/tnt_acme_12345678", nil)
	req = withAuthContext(req)
	req.Header.Set("X-Confirm-Delete", "DELETE wrong_id")
	w := httptest.NewRecorder()

	api.HandleDelete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleDelete_Success202(t *testing.T) {
	deleted := false
	store := &mockTenantStore{
		getFn: func(_ context.Context, id string) (*postgres.Tenant, error) {
			return &postgres.Tenant{
				TenantID:  id,
				Name:      "Acme",
				Slug:      "acme",
				Plan:      "starter",
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}, nil
		},
		deleteFn: func(_ context.Context, _ string) error {
			deleted = true
			return nil
		},
	}

	api := NewDeleteAPI(store, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/tnt_acme_12345678", nil)
	req = withAuthContext(req)
	req.Header.Set("X-Confirm-Delete", "DELETE tnt_acme_12345678")
	w := httptest.NewRecorder()

	api.HandleDelete(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusAccepted, w.Body.String())
	}
	if !deleted {
		t.Error("expected soft-delete to be called")
	}
}

func TestHandleDelete_NoAuth(t *testing.T) {
	store := &mockTenantStore{}
	api := NewDeleteAPI(store, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/tnt_acme_12345678", nil)
	// No auth context
	req.Header.Set("X-Confirm-Delete", "DELETE tnt_acme_12345678")
	w := httptest.NewRecorder()

	api.HandleDelete(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleDelete_TenantNotFound(t *testing.T) {
	store := &mockTenantStore{
		getFn: func(_ context.Context, _ string) (*postgres.Tenant, error) {
			return nil, postgres.ErrTenantNotFound
		},
	}

	api := NewDeleteAPI(store, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/tnt_nonexistent", nil)
	ac := auth.AuthContext{PrincipalID: "user_test"}
	ctx := auth.WithContext(req.Context(), ac)
	req = req.WithContext(ctx)
	req.Header.Set("X-Confirm-Delete", "DELETE tnt_nonexistent")
	w := httptest.NewRecorder()

	api.HandleDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
