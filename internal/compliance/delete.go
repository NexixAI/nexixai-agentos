package compliance

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
)

// TenantDeleter deletes all data for a tenant across all tables.
type TenantDeleter interface {
	DeleteAllTenantData(tenantID string) error
}

// AuditLogger records system-level audit events.
type AuditLogger interface {
	LogDeletion(tenantID, actor string) error
}

// DeleteHandler handles POST /v1/tenants/data-delete.
type DeleteHandler struct {
	deleter TenantDeleter
	auditor AuditLogger
}

// NewDeleteHandler creates a new DeleteHandler.
func NewDeleteHandler(deleter TenantDeleter, auditor AuditLogger) *DeleteHandler {
	return &DeleteHandler{
		deleter: deleter,
		auditor: auditor,
	}
}

// ServeHTTP handles tenant data deletion requests.
func (h *DeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported", httpx.CorrelationID(r), false)
		return
	}

	ac, ok := auth.Get(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing auth context", httpx.CorrelationID(r), false)
		return
	}

	// Require owner role.
	role := auth.Role(ac.SubjectType)
	if role == "" {
		role = auth.RoleOwner
	}
	if !auth.HasPermission(role, auth.RoleOwner) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "owner role required", httpx.CorrelationID(r), false)
		return
	}

	tenantID, err := auth.RequireTenant(ac)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_tenant", err.Error(), httpx.CorrelationID(r), false)
		return
	}

	// Validate confirmation header.
	confirmHeader := strings.TrimSpace(r.Header.Get("X-Confirm-Delete"))
	expected := fmt.Sprintf("DELETE %s", tenantID)
	if confirmHeader != expected {
		httpx.Error(w, http.StatusBadRequest, "confirmation_required",
			fmt.Sprintf("X-Confirm-Delete header must be %q", expected),
			httpx.CorrelationID(r), false)
		return
	}

	// Launch async deletion.
	go h.runDeletion(tenantID, ac.PrincipalID)

	httpx.JSON(w, http.StatusAccepted, map[string]string{
		"status":    "accepted",
		"tenant_id": tenantID,
		"message":   "deletion initiated",
	})
}

func (h *DeleteHandler) runDeletion(tenantID, actor string) {
	slog.Info("deletion: starting tenant data deletion",
		"tenant_id", tenantID, "actor", actor)

	if h.deleter != nil {
		if err := h.deleter.DeleteAllTenantData(tenantID); err != nil {
			slog.Error("deletion: failed to delete tenant data",
				"error", err, "tenant_id", tenantID)
			return
		}
	}

	// Log audit event.
	if h.auditor != nil {
		if err := h.auditor.LogDeletion(tenantID, actor); err != nil {
			slog.Error("deletion: failed to log audit event",
				"error", err, "tenant_id", tenantID)
		}
	}

	slog.Info("deletion: completed successfully",
		"tenant_id", tenantID)
}
