package lifecycle

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/NexixAI/nexixai-agentos/internal/httpx"
)

// AdminPurgeHandler returns an http.HandlerFunc for POST /v1/admin/purge.
// It triggers an immediate purge pass and returns the counts.
func AdminPurgeHandler(db *sql.DB, cfg PurgeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cid := httpx.CorrelationID(r)

		if r.Method != http.MethodPost {
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", cid, false)
			return
		}

		result, err := RunPurgeNow(r.Context(), db, cfg)
		if err != nil {
			slog.Error("manual purge failed", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "purge_failed", "purge operation failed", cid, true)
			return
		}

		slog.Info("manual purge completed",
			"deleted_runs", result.DeletedRuns,
			"deleted_events", result.DeletedEvents,
			"deleted_audit", result.DeletedAudit,
			"deleted_usage", result.DeletedUsage,
		)

		httpx.JSON(w, http.StatusOK, map[string]any{
			"result":         result,
			"correlation_id": cid,
		})
	}
}
