package quota

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// PostgresBackend is a QuotaBackend backed by a `quota_state` table.
//
// Table schema (must be created via migrations):
//
//	CREATE TABLE IF NOT EXISTS quota_state (
//	    tenant_id  TEXT PRIMARY KEY,
//	    tokens     FLOAT8       NOT NULL DEFAULT 0,
//	    last_refill TIMESTAMPTZ NOT NULL DEFAULT NOW(),
//	    concurrent INT          NOT NULL DEFAULT 0
//	);
type PostgresBackend struct {
	db *sql.DB
}

// NewPostgresBackend creates a PostgresBackend that uses the given database connection.
func NewPostgresBackend(db *sql.DB) *PostgresBackend {
	return &PostgresBackend{db: db}
}

// AllowQPS atomically refills and consumes one token for the tenant.
// It uses an UPSERT to create the row on first access.
func (p *PostgresBackend) AllowQPS(tenant string, qps int) bool {
	if p.db == nil {
		slog.Error("quota: AllowQPS called with nil db, failing closed")
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Upsert: insert if missing, then atomically refill + consume.
	// The CTE ensures the row exists before the update.
	query := `
WITH ensure_row AS (
    INSERT INTO quota_state (tenant_id, tokens, last_refill, concurrent)
    VALUES ($1, $2, NOW(), 0)
    ON CONFLICT (tenant_id) DO NOTHING
)
UPDATE quota_state
SET
    tokens = LEAST(tokens + EXTRACT(EPOCH FROM (NOW() - last_refill)) * $2, $2) - 1,
    last_refill = NOW()
WHERE tenant_id = $1
  AND LEAST(tokens + EXTRACT(EPOCH FROM (NOW() - last_refill)) * $2, $2) >= 1
RETURNING tokens`

	var remaining float64
	err := p.db.QueryRowContext(ctx, query, tenant, qps).Scan(&remaining)
	if err != nil {
		if err == sql.ErrNoRows {
			// Not enough tokens — rate limited.
			return false
		}
		slog.Error("quota: AllowQPS postgres error", "tenant", tenant, "err", err)
		// Fail closed: deny on error.
		return false
	}
	return true
}

// TryIncConcurrent atomically increments concurrent if below max.
func (p *PostgresBackend) TryIncConcurrent(tenant string, max int) bool {
	if p.db == nil {
		slog.Error("quota: TryIncConcurrent called with nil db, failing closed")
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := `
WITH ensure_row AS (
    INSERT INTO quota_state (tenant_id, tokens, last_refill, concurrent)
    VALUES ($1, 0, NOW(), 0)
    ON CONFLICT (tenant_id) DO NOTHING
)
UPDATE quota_state
SET concurrent = concurrent + 1
WHERE tenant_id = $1
  AND concurrent < $2
RETURNING concurrent`

	var cur int
	err := p.db.QueryRowContext(ctx, query, tenant, max).Scan(&cur)
	if err != nil {
		if err == sql.ErrNoRows {
			return false
		}
		slog.Error("quota: TryIncConcurrent postgres error", "tenant", tenant, "err", err)
		// Fail closed.
		return false
	}
	return true
}

// DecConcurrent atomically decrements concurrent (floor at 0).
func (p *PostgresBackend) DecConcurrent(tenant string) {
	if p.db == nil {
		slog.Error("quota: DecConcurrent called with nil db, failing closed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := `UPDATE quota_state SET concurrent = GREATEST(concurrent - 1, 0) WHERE tenant_id = $1`
	_, err := p.db.ExecContext(ctx, query, tenant)
	if err != nil {
		slog.Error("quota: DecConcurrent postgres error", "tenant", tenant, "err", err)
	}
}

// WithInitialConcurrent upserts initial concurrent counts (e.g. on restart).
func (p *PostgresBackend) WithInitialConcurrent(counts map[string]int) {
	if p.db == nil {
		slog.Error("quota: WithInitialConcurrent called with nil db")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query := `
INSERT INTO quota_state (tenant_id, tokens, last_refill, concurrent)
VALUES ($1, 0, NOW(), $2)
ON CONFLICT (tenant_id) DO UPDATE SET concurrent = $2`

	for tenant, count := range counts {
		_, err := p.db.ExecContext(ctx, query, tenant, count)
		if err != nil {
			slog.Error("quota: WithInitialConcurrent postgres error", "tenant", tenant, "err", err)
		}
	}
}
