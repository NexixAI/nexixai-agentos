package tenants

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// DeleteAPI holds the tenant deletion handler and its dependencies.
type DeleteAPI struct {
	store       postgres.TenantStore
	db          *sql.DB
	auditLogger audit.Logger
}

// NewDeleteAPI creates a new tenant deletion API.
func NewDeleteAPI(store postgres.TenantStore, db *sql.DB, auditLogger audit.Logger) *DeleteAPI {
	return &DeleteAPI{
		store:       store,
		db:          db,
		auditLogger: auditLogger,
	}
}

// HandleDelete handles DELETE /v1/tenants/{tenant_id}.
// Requires X-Confirm-Delete header with value "DELETE {tenant_id}".
// Returns 202 Accepted and schedules async background deletion.
func (d *DeleteAPI) HandleDelete(w http.ResponseWriter, r *http.Request) {
	cid := httpx.CorrelationID(r)

	ac, ok := auth.Get(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing auth context", cid, false)
		return
	}

	tenantID := extractTenantID(r)
	if tenantID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "tenant_id is required", cid, false)
		return
	}

	// Validate confirmation header.
	confirmHeader := strings.TrimSpace(r.Header.Get("X-Confirm-Delete"))
	expectedConfirm := "DELETE " + tenantID
	if confirmHeader != expectedConfirm {
		httpx.Error(w, http.StatusBadRequest, "confirmation_required",
			fmt.Sprintf("X-Confirm-Delete header must be %q", expectedConfirm), cid, false)
		return
	}

	// Verify tenant exists before scheduling deletion.
	_, err := d.store.Get(r.Context(), tenantID)
	if err != nil {
		if err == postgres.ErrTenantNotFound {
			httpx.Error(w, http.StatusNotFound, "not_found", "tenant not found", cid, false)
			return
		}
		slog.Error("failed to get tenant for deletion", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to verify tenant", cid, true)
		return
	}

	// Soft-delete immediately.
	if err := d.store.Delete(r.Context(), tenantID); err != nil {
		if err == postgres.ErrTenantNotFound {
			httpx.Error(w, http.StatusNotFound, "not_found", "tenant not found", cid, false)
			return
		}
		slog.Error("failed to soft-delete tenant", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to delete tenant", cid, true)
		return
	}

	// Log system-level audit event (not tenant-scoped, since tenant is being deleted).
	principalID := ac.PrincipalID
	if principalID == "" {
		principalID = "system"
	}
	if d.auditLogger != nil {
		d.auditLogger.Log(audit.Entry{
			Time:          time.Now().UTC().Format(time.RFC3339),
			TenantID:      "system",
			PrincipalID:   principalID,
			Action:        "tenant.delete.scheduled",
			Resource:      tenantID,
			Outcome:       "accepted",
			CorrelationID: cid,
		})
	}

	// Schedule async background deletion (only if db is available).
	if d.db != nil {
		go d.runAsyncDeletion(tenantID, principalID, cid)
	}

	slog.Info("tenant deletion scheduled", "tenant_id", tenantID, "principal_id", principalID)
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"tenant_id": tenantID,
		"status":    "deletion_scheduled",
		"message":   "Tenant soft-deleted. Background data purge in progress.",
	})
}

// runAsyncDeletion performs the background data deletion for a tenant.
func (d *DeleteAPI) runAsyncDeletion(tenantID, principalID, correlationID string) {
	if d.db == nil {
		slog.Error("async tenant data deletion skipped: no database connection", "tenant_id", tenantID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	slog.Info("starting async tenant data deletion", "tenant_id", tenantID)

	pgStore := postgres.NewTenantStoreFromDB(d.db)
	if err := pgStore.DeleteAllData(ctx, tenantID); err != nil {
		slog.Error("async tenant data deletion failed", "error", err, "tenant_id", tenantID)
		if d.auditLogger != nil {
			d.auditLogger.Log(audit.Entry{
				Time:          time.Now().UTC().Format(time.RFC3339),
				TenantID:      "system",
				PrincipalID:   principalID,
				Action:        "tenant.delete.failed",
				Resource:      tenantID,
				Outcome:       "error",
				CorrelationID: correlationID,
				Meta:          map[string]any{"error": err.Error()},
			})
		}
		return
	}

	slog.Info("async tenant data deletion completed", "tenant_id", tenantID)
	if d.auditLogger != nil {
		d.auditLogger.Log(audit.Entry{
			Time:          time.Now().UTC().Format(time.RFC3339),
			TenantID:      "system",
			PrincipalID:   principalID,
			Action:        "tenant.delete.completed",
			Resource:      tenantID,
			Outcome:       "success",
			CorrelationID: correlationID,
		})
	}
}
