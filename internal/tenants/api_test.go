package tenants

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// mockTenantStore implements postgres.TenantStore for unit testing.
type mockTenantStore struct {
	createFn    func(ctx context.Context, t postgres.Tenant) error
	getFn       func(ctx context.Context, id string) (*postgres.Tenant, error)
	updateFn    func(ctx context.Context, id string, u postgres.TenantUpdate) error
	deleteFn    func(ctx context.Context, id string) error
	listFn      func(ctx context.Context) ([]postgres.Tenant, error)
	addMemberFn func(ctx context.Context, m postgres.TenantMember) error
}

func (m *mockTenantStore) Create(ctx context.Context, t postgres.Tenant) error {
	if m.createFn != nil {
		return m.createFn(ctx, t)
	}
	return nil
}

func (m *mockTenantStore) Get(ctx context.Context, id string) (*postgres.Tenant, error) {
	if m.getFn != nil {
		return m.getFn(ctx, id)
	}
	return nil, postgres.ErrTenantNotFound
}

func (m *mockTenantStore) Update(ctx context.Context, id string, u postgres.TenantUpdate) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, id, u)
	}
	return nil
}

func (m *mockTenantStore) Delete(ctx context.Context, id string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, id)
	}
	return nil
}

func (m *mockTenantStore) List(ctx context.Context) ([]postgres.Tenant, error) {
	if m.listFn != nil {
		return m.listFn(ctx)
	}
	return nil, nil
}

func (m *mockTenantStore) AddMember(ctx context.Context, mem postgres.TenantMember) error {
	if m.addMemberFn != nil {
		return m.addMemberFn(ctx, mem)
	}
	return nil
}

func withAuthContext(r *http.Request) *http.Request {
	ac := auth.AuthContext{
		TenantID:    "tnt_test",
		PrincipalID: "user_creator",
	}
	ctx := auth.WithContext(r.Context(), ac)
	return r.WithContext(ctx)
}

func TestHandleCreate_Success(t *testing.T) {
	var createdTenant postgres.Tenant
	var addedMember postgres.TenantMember

	store := &mockTenantStore{
		createFn: func(_ context.Context, tnt postgres.Tenant) error {
			createdTenant = tnt
			return nil
		},
		addMemberFn: func(_ context.Context, m postgres.TenantMember) error {
			addedMember = m
			return nil
		},
	}

	api := NewAPI(store)

	body := `{"name":"Acme","slug":"acme","plan":"starter"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", bytes.NewBufferString(body))
	req = withAuthContext(req)
	w := httptest.NewRecorder()

	api.HandleCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp tenantResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "Acme" {
		t.Errorf("name = %q, want %q", resp.Name, "Acme")
	}
	if resp.Slug != "acme" {
		t.Errorf("slug = %q, want %q", resp.Slug, "acme")
	}
	if resp.Plan != "starter" {
		t.Errorf("plan = %q, want %q", resp.Plan, "starter")
	}
	if len(resp.TenantID) < 10 {
		t.Errorf("tenant_id looks too short: %q", resp.TenantID)
	}
	if createdTenant.Slug != "acme" {
		t.Errorf("store.Create slug = %q, want %q", createdTenant.Slug, "acme")
	}
	if addedMember.Role != "owner" {
		t.Errorf("member role = %q, want %q", addedMember.Role, "owner")
	}
	if addedMember.PrincipalID != "user_creator" {
		t.Errorf("member principal = %q, want %q", addedMember.PrincipalID, "user_creator")
	}
}

func TestHandleCreate_SlugConflict(t *testing.T) {
	store := &mockTenantStore{
		createFn: func(_ context.Context, _ postgres.Tenant) error {
			return postgres.ErrSlugConflict
		},
	}

	api := NewAPI(store)

	body := `{"name":"Acme","slug":"acme","plan":"starter"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", bytes.NewBufferString(body))
	req = withAuthContext(req)
	w := httptest.NewRecorder()

	api.HandleCreate(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandleCreate_MissingFields(t *testing.T) {
	store := &mockTenantStore{}
	api := NewAPI(store)

	tests := []struct {
		name string
		body string
	}{
		{"missing_name", `{"slug":"acme","plan":"starter"}`},
		{"missing_slug", `{"name":"Acme","plan":"starter"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/tenants", bytes.NewBufferString(tt.body))
			req = withAuthContext(req)
			w := httptest.NewRecorder()

			api.HandleCreate(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func TestHandleCreate_NoAuth(t *testing.T) {
	store := &mockTenantStore{}
	api := NewAPI(store)

	body := `{"name":"Acme","slug":"acme"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", bytes.NewBufferString(body))
	// No auth context
	w := httptest.NewRecorder()

	api.HandleCreate(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleGet_Success(t *testing.T) {
	now := time.Now().UTC()
	store := &mockTenantStore{
		getFn: func(_ context.Context, id string) (*postgres.Tenant, error) {
			if id == "tnt_acme_12345678" {
				return &postgres.Tenant{
					TenantID:  id,
					Name:      "Acme",
					Slug:      "acme",
					Plan:      "starter",
					CreatedAt: now,
					UpdatedAt: now,
				}, nil
			}
			return nil, postgres.ErrTenantNotFound
		},
	}

	api := NewAPI(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/tnt_acme_12345678", nil)
	req = withAuthContext(req)
	w := httptest.NewRecorder()

	api.HandleGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp tenantResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TenantID != "tnt_acme_12345678" {
		t.Errorf("tenant_id = %q, want %q", resp.TenantID, "tnt_acme_12345678")
	}
}

func TestHandleGet_NotFound(t *testing.T) {
	store := &mockTenantStore{
		getFn: func(_ context.Context, _ string) (*postgres.Tenant, error) {
			return nil, postgres.ErrTenantNotFound
		},
	}

	api := NewAPI(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/nonexistent", nil)
	req = withAuthContext(req)
	w := httptest.NewRecorder()

	api.HandleGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpdate_Success(t *testing.T) {
	now := time.Now().UTC()
	store := &mockTenantStore{
		updateFn: func(_ context.Context, _ string, _ postgres.TenantUpdate) error {
			return nil
		},
		getFn: func(_ context.Context, id string) (*postgres.Tenant, error) {
			return &postgres.Tenant{
				TenantID:  id,
				Name:      "Updated",
				Slug:      "acme",
				Plan:      "pro",
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		},
	}

	api := NewAPI(store)

	body := `{"name":"Updated","plan":"pro"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/tenants/tnt_acme_12345678", bytes.NewBufferString(body))
	req = withAuthContext(req)
	w := httptest.NewRecorder()

	api.HandleUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp tenantResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "Updated" {
		t.Errorf("name = %q, want %q", resp.Name, "Updated")
	}
}

func TestHandleUpdate_NotFound(t *testing.T) {
	store := &mockTenantStore{
		updateFn: func(_ context.Context, _ string, _ postgres.TenantUpdate) error {
			return postgres.ErrTenantNotFound
		},
	}

	api := NewAPI(store)

	body := `{"name":"Updated"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/tenants/tnt_nonexistent", bytes.NewBufferString(body))
	req = withAuthContext(req)
	w := httptest.NewRecorder()

	api.HandleUpdate(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpdate_InternalError(t *testing.T) {
	store := &mockTenantStore{
		updateFn: func(_ context.Context, _ string, _ postgres.TenantUpdate) error {
			return errors.New("db connection lost")
		},
	}

	api := NewAPI(store)

	body := `{"name":"x"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/tenants/tnt_test_1234", bytes.NewBufferString(body))
	req = withAuthContext(req)
	w := httptest.NewRecorder()

	api.HandleUpdate(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
