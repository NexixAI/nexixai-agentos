package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

// UsageStore is a PostgreSQL-backed store for token usage records.
type UsageStore struct {
	db *sql.DB
}

// NewUsageStore opens a connection pool and returns a ready-to-use UsageStore.
func NewUsageStore(cfg config.PostgresConfig) (*UsageStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &UsageStore{db: db}, nil
}

// NewUsageStoreFromDB wraps an existing *sql.DB as a UsageStore.
func NewUsageStoreFromDB(db *sql.DB) *UsageStore {
	return &UsageStore{db: db}
}

func (s *UsageStore) Record(ctx context.Context, tenantID string, tokens int, timestamp time.Time) error {
	const q = `
INSERT INTO usage_records (tenant_id, tokens, recorded_at)
VALUES ($1, $2, $3)`

	_, err := s.db.ExecContext(ctx, q, tenantID, tokens, timestamp)
	return err
}

func (s *UsageStore) HourlyUsage(ctx context.Context, tenantID string, hour time.Time) (int, error) {
	h := hour.Truncate(time.Hour)
	const q = `
SELECT COALESCE(SUM(tokens), 0)
FROM usage_records
WHERE tenant_id = $1
  AND recorded_at >= $2
  AND recorded_at < $3`

	var total int
	err := s.db.QueryRowContext(ctx, q, tenantID, h, h.Add(time.Hour)).Scan(&total)
	return total, err
}

func (s *UsageStore) DailyUsage(ctx context.Context, tenantID string, day time.Time) (int, error) {
	d := day.Truncate(24 * time.Hour)
	const q = `
SELECT COALESCE(SUM(tokens), 0)
FROM usage_records
WHERE tenant_id = $1
  AND recorded_at >= $2
  AND recorded_at < $3`

	var total int
	err := s.db.QueryRowContext(ctx, q, tenantID, d, d.Add(24*time.Hour)).Scan(&total)
	return total, err
}

func (s *UsageStore) Close() error {
	return s.db.Close()
}
