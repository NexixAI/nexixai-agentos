package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// PurgeConfig holds retention durations for the purge loop.
type PurgeConfig struct {
	RetentionRunsDays   int
	RetentionEventsDays int
	RetentionAuditDays  int
	RetentionUsageDays  int
}

// PurgeResult contains the counts of deleted rows per table.
type PurgeResult struct {
	DeletedRuns   int `json:"deleted_runs"`
	DeletedEvents int `json:"deleted_events"`
	DeletedAudit  int `json:"deleted_audit"`
	DeletedUsage  int `json:"deleted_usage"`
}

const batchSize = 1000

// LoadPurgeConfig reads retention configuration from environment variables.
func LoadPurgeConfig() PurgeConfig {
	return PurgeConfig{
		RetentionRunsDays:   envIntWithMinDefault("AGENTOS_RETENTION_RUNS_DAYS", 90, 7),
		RetentionEventsDays: envIntWithMinDefault("AGENTOS_RETENTION_EVENTS_DAYS", 30, 7),
		RetentionAuditDays:  envIntWithMinDefault("AGENTOS_RETENTION_AUDIT_DAYS", 365, 90),
		RetentionUsageDays:  envIntWithMinDefault("AGENTOS_RETENTION_USAGE_DAYS", 730, 90),
	}
}

// StartPurgeLoop runs the purge job daily. It blocks until the context is cancelled.
func StartPurgeLoop(ctx context.Context, db *sql.DB, cfg PurgeConfig) {
	slog.Info("purge loop started",
		"runs_days", cfg.RetentionRunsDays,
		"events_days", cfg.RetentionEventsDays,
		"audit_days", cfg.RetentionAuditDays,
		"usage_days", cfg.RetentionUsageDays,
	)

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run once on startup.
	result, err := RunPurgeNow(ctx, db, cfg)
	if err != nil {
		slog.Error("initial purge failed", "error", err)
	} else {
		logPurgeResult(result)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("purge loop stopped")
			return
		case <-ticker.C:
			result, err := RunPurgeNow(ctx, db, cfg)
			if err != nil {
				slog.Error("purge cycle failed", "error", err)
			} else {
				logPurgeResult(result)
			}
		}
	}
}

// RunPurgeNow executes a single purge pass and returns counts of deleted rows.
func RunPurgeNow(ctx context.Context, db *sql.DB, cfg PurgeConfig) (PurgeResult, error) {
	var result PurgeResult
	now := time.Now().UTC()

	// Purge completed/failed/canceled runs older than retention.
	runsCutoff := now.Add(-time.Duration(cfg.RetentionRunsDays) * 24 * time.Hour)
	deleted, err := batchDeleteRuns(ctx, db, runsCutoff)
	if err != nil {
		return result, fmt.Errorf("purge runs: %w", err)
	}
	result.DeletedRuns = deleted

	// Purge event_log entries older than retention.
	eventsCutoff := now.Add(-time.Duration(cfg.RetentionEventsDays) * 24 * time.Hour)
	deleted, err = batchDeleteByTimestamp(ctx, db, "event_log", "created_at", eventsCutoff)
	if err != nil {
		return result, fmt.Errorf("purge events: %w", err)
	}
	result.DeletedEvents = deleted

	// Purge audit_events older than retention.
	auditCutoff := now.Add(-time.Duration(cfg.RetentionAuditDays) * 24 * time.Hour)
	deleted, err = batchDeleteByTimestamp(ctx, db, "audit_events", "created_at", auditCutoff)
	if err != nil {
		return result, fmt.Errorf("purge audit: %w", err)
	}
	result.DeletedAudit = deleted

	// Purge usage_records older than retention.
	usageCutoff := now.Add(-time.Duration(cfg.RetentionUsageDays) * 24 * time.Hour)
	deleted, err = batchDeleteByTimestamp(ctx, db, "usage_records", "recorded_at", usageCutoff)
	if err != nil {
		return result, fmt.Errorf("purge usage: %w", err)
	}
	result.DeletedUsage = deleted

	return result, nil
}

// batchDeleteRuns deletes completed/failed/canceled runs older than the cutoff,
// in batches. Running runs (status not in completed/failed/canceled) are exempt.
func batchDeleteRuns(ctx context.Context, db *sql.DB, cutoff time.Time) (int, error) {
	total := 0
	for {
		const q = `
DELETE FROM runs
WHERE ctid IN (
    SELECT ctid FROM runs
    WHERE created_at < $1
      AND status IN ('completed', 'failed', 'canceled')
    LIMIT $2
)`
		// runs.created_at is TEXT (RFC3339), so we format for comparison.
		cutoffStr := cutoff.Format(time.RFC3339)
		res, err := db.ExecContext(ctx, q, cutoffStr, batchSize)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += int(n)
		if n < int64(batchSize) {
			break
		}
	}
	return total, nil
}

// batchDeleteByTimestamp deletes rows from a table where tsColumn < cutoff, in batches.
func batchDeleteByTimestamp(ctx context.Context, db *sql.DB, table, tsColumn string, cutoff time.Time) (int, error) {
	total := 0
	for {
		q := fmt.Sprintf(
			"DELETE FROM %s WHERE ctid IN (SELECT ctid FROM %s WHERE %s < $1 LIMIT %d)",
			table, table, tsColumn, batchSize,
		)
		res, err := db.ExecContext(ctx, q, cutoff)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += int(n)
		if n < int64(batchSize) {
			break
		}
	}
	return total, nil
}

func logPurgeResult(r PurgeResult) {
	slog.Info("purge cycle completed",
		"deleted_runs", r.DeletedRuns,
		"deleted_events", r.DeletedEvents,
		"deleted_audit", r.DeletedAudit,
		"deleted_usage", r.DeletedUsage,
	)
}

// envIntWithMinDefault reads an env var as int, returns defaultVal if unset,
// and enforces a minimum value.
func envIntWithMinDefault(key string, defaultVal, minVal int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid env var, using default", "key", key, "value", raw, "default", defaultVal)
		return defaultVal
	}
	if val < minVal {
		slog.Warn("env var below minimum, clamping", "key", key, "value", val, "min", minVal)
		return minVal
	}
	return val
}
