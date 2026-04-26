package tenants

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// API holds the tenant CRUD HTTP handlers.
type API struct {
	store postgres.TenantStore
}

// NewAPI creates a new tenant API with the given store.
func NewAPI(store postgres.TenantStore) *API {
	return &API{store: store}
}

// createRequest is the JSON body for POST /v1/tenants.
type createRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	Plan string `json:"plan"`
}

// tenantResponse is the JSON response for a tenant object.
type tenantResponse struct {
	TenantID  string  `json:"tenant_id"`
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Plan      string  `json:"plan"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	DeletedAt *string `json:"deleted_at,omitempty"`
}

func toResponse(t *postgres.Tenant) tenantResponse {
	resp := tenantResponse{
		TenantID:  t.TenantID,
		Name:      t.Name,
		Slug:      t.Slug,
		Plan:      t.Plan,
		CreatedAt: t.CreatedAt.Format(time.RFC3339),
		UpdatedAt: t.UpdatedAt.Format(time.RFC3339),
	}
	if t.DeletedAt != nil {
		s := t.DeletedAt.Format(time.RFC3339)
		resp.DeletedAt = &s
	}
	return resp
}

// HandleCreate handles POST /v1/tenants.
func (a *API) HandleCreate(w http.ResponseWriter, r *http.Request) {
	cid := httpx.CorrelationID(r)

	ac, ok := auth.Get(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing auth context", cid, false)
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid request body", cid, false)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	req.Plan = strings.TrimSpace(req.Plan)

	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "name is required", cid, false)
		return
	}
	if req.Slug == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "slug is required", cid, false)
		return
	}
	if req.Plan == "" {
		req.Plan = "starter"
	}

	tenantID := "tnt_" + req.Slug + "_" + randomHex(8)

	now := time.Now().UTC()
	tenant := postgres.Tenant{
		TenantID:  tenantID,
		Name:      req.Name,
		Slug:      req.Slug,
		Plan:      req.Plan,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := a.store.Create(r.Context(), tenant); err != nil {
		if err == postgres.ErrSlugConflict {
			httpx.Error(w, http.StatusConflict, "slug_conflict", "tenant slug already exists", cid, false)
			return
		}
		slog.Error("failed to create tenant", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create tenant", cid, true)
		return
	}

	// Add creating principal as owner in tenant_members.
	principalID := ac.PrincipalID
	if principalID == "" {
		principalID = "system"
	}
	if err := a.store.AddMember(r.Context(), postgres.TenantMember{
		TenantID:    tenantID,
		PrincipalID: principalID,
		Role:        "owner",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		slog.Error("failed to add owner member", "error", err, "tenant_id", tenantID, "principal_id", principalID)
		// Tenant was created but member failed — log and return tenant anyway.
	}

	slog.Info("tenant created", "tenant_id", tenantID, "slug", req.Slug, "principal_id", principalID)
	httpx.JSON(w, http.StatusCreated, toResponse(&tenant))
}

// HandleGet handles GET /v1/tenants/{tenant_id}.
func (a *API) HandleGet(w http.ResponseWriter, r *http.Request) {
	cid := httpx.CorrelationID(r)

	_, ok := auth.Get(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing auth context", cid, false)
		return
	}

	tenantID := extractTenantID(r)
	if tenantID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "tenant_id is required", cid, false)
		return
	}

	tenant, err := a.store.Get(r.Context(), tenantID)
	if err != nil {
		if err == postgres.ErrTenantNotFound {
			httpx.Error(w, http.StatusNotFound, "not_found", "tenant not found", cid, false)
			return
		}
		slog.Error("failed to get tenant", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to get tenant", cid, true)
		return
	}

	httpx.JSON(w, http.StatusOK, toResponse(tenant))
}

// HandleUpdate handles PUT /v1/tenants/{tenant_id}.
func (a *API) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	cid := httpx.CorrelationID(r)

	_, ok := auth.Get(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing auth context", cid, false)
		return
	}

	tenantID := extractTenantID(r)
	if tenantID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "tenant_id is required", cid, false)
		return
	}

	var body struct {
		Name *string `json:"name"`
		Plan *string `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid request body", cid, false)
		return
	}

	updates := postgres.TenantUpdate{
		Name: body.Name,
		Plan: body.Plan,
	}

	if err := a.store.Update(r.Context(), tenantID, updates); err != nil {
		if err == postgres.ErrTenantNotFound {
			httpx.Error(w, http.StatusNotFound, "not_found", "tenant not found", cid, false)
			return
		}
		slog.Error("failed to update tenant", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update tenant", cid, true)
		return
	}

	// Re-fetch to return updated state.
	tenant, err := a.store.Get(r.Context(), tenantID)
	if err != nil {
		slog.Error("failed to get tenant after update", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to get updated tenant", cid, true)
		return
	}

	slog.Info("tenant updated", "tenant_id", tenantID)
	httpx.JSON(w, http.StatusOK, toResponse(tenant))
}

// extractTenantID extracts a tenant_id from the URL path.
// Expects paths like /v1/tenants/{tenant_id}.
func extractTenantID(r *http.Request) string {
	// Use URL path: /v1/tenants/{tenant_id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	// parts = ["v1", "tenants", "{tenant_id}"]
	if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "tenants" {
		return parts[2]
	}
	return ""
}

// randomHex returns n random hex characters.
func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; if it does, panic is appropriate.
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)[:n]
}
