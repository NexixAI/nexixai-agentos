package admin

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/httpx"
)

// BackupCheckResponse is the JSON shape returned by the backup-check endpoint.
type BackupCheckResponse struct {
	LastBackupTS      string         `json:"last_backup_ts"`
	Warning           string         `json:"warning"`
	DatabaseSizeBytes int64          `json:"database_size_bytes"`
	TableRowCounts    map[string]int `json:"table_row_counts"`
}

// monitoredTables is the fixed set of tables whose row counts are reported.
var monitoredTables = []string{
	"runs",
	"agents",
	"audit_events",
	"usage_records",
	"event_log",
	"tenants",
	"tenant_members",
	"api_keys",
}

// BackupCheckHandler returns an http.HandlerFunc that reports backup status
// and basic database statistics. If db is nil (file-mode), row counts are -1.
func BackupCheckHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported", httpx.CorrelationID(r), false)
			return
		}

		resp := BackupCheckResponse{
			TableRowCounts: make(map[string]int, len(monitoredTables)),
		}

		// --- last_backup_ts & warning ---
		resp.LastBackupTS = os.Getenv("AGENTOS_LAST_BACKUP_TS")
		if resp.LastBackupTS == "" {
			resp.Warning = "No backup detected in >24h"
		} else {
			ts, err := time.Parse(time.RFC3339, resp.LastBackupTS)
			if err != nil {
				slog.Warn("failed to parse AGENTOS_LAST_BACKUP_TS", "value", resp.LastBackupTS, "error", err)
				resp.Warning = "No backup detected in >24h"
			} else if time.Since(ts) > 24*time.Hour {
				resp.Warning = "No backup detected in >24h"
			}
		}

		// --- database stats ---
		if db == nil {
			resp.DatabaseSizeBytes = -1
			for _, t := range monitoredTables {
				resp.TableRowCounts[t] = -1
			}
		} else {
			if err := db.QueryRow("SELECT pg_database_size(current_database())").Scan(&resp.DatabaseSizeBytes); err != nil {
				slog.Error("failed to query database size", "error", err)
				resp.DatabaseSizeBytes = -1
			}

			for _, table := range monitoredTables {
				var count int
				// Table names are from a fixed list, not user input — safe to interpolate.
				if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil { //nolint:gosec // fixed table list
					slog.Error("failed to query row count", "table", table, "error", err)
					count = -1
				}
				resp.TableRowCounts[table] = count
			}
		}

		httpx.JSON(w, http.StatusOK, resp)
	}
}
