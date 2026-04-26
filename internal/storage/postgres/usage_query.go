package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

// UsageReport is the aggregated usage data returned by QueryUsage.
type UsageReport struct {
	TenantID    string           `json:"tenant_id"`
	Period      UsagePeriod      `json:"period"`
	TotalTokens int64            `json:"total_tokens"`
	TotalRuns   int64            `json:"total_runs"`
	ByModel     []UsageByModel   `json:"by_model"`
	ByBucket    []UsageByBucket  `json:"by_day"`
}

// UsagePeriod describes the time range of the report.
type UsagePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// UsageByModel holds per-model aggregation.
type UsageByModel struct {
	Model  string `json:"model"`
	Tokens int64  `json:"tokens"`
	Runs   int64  `json:"runs"`
}

// UsageByBucket holds per-time-bucket aggregation.
type UsageByBucket struct {
	Date   string `json:"date"`
	Tokens int64  `json:"tokens"`
	Runs   int64  `json:"runs"`
}

// UsageQueryStore defines the interface for querying aggregated usage data.
type UsageQueryStore interface {
	QueryUsage(ctx context.Context, tenantID string, start, end time.Time, granularity string) (*UsageReport, error)
}

// UsageQueryPGStore is a PostgreSQL-backed implementation of UsageQueryStore.
type UsageQueryPGStore struct {
	db *sql.DB
}

// NewUsageQueryStore opens a connection pool and returns a ready-to-use UsageQueryPGStore.
func NewUsageQueryStore(cfg config.PostgresConfig) (*UsageQueryPGStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &UsageQueryPGStore{db: db}, nil
}

// NewUsageQueryStoreFromDB wraps an existing *sql.DB as a UsageQueryPGStore.
func NewUsageQueryStoreFromDB(db *sql.DB) *UsageQueryPGStore {
	return &UsageQueryPGStore{db: db}
}

// QueryUsage aggregates usage records for the given tenant and time range.
// granularity must be "day" or "hour".
func (s *UsageQueryPGStore) QueryUsage(ctx context.Context, tenantID string, start, end time.Time, granularity string) (*UsageReport, error) {
	report := &UsageReport{
		TenantID: tenantID,
		Period: UsagePeriod{
			Start: start.Format(time.RFC3339),
			End:   end.Format(time.RFC3339),
		},
		ByModel:  []UsageByModel{},
		ByBucket: []UsageByBucket{},
	}

	// Total aggregation.
	const totalQ = `
SELECT COALESCE(SUM(tokens), 0), COUNT(*)
FROM usage_records
WHERE tenant_id = $1
  AND recorded_at >= $2
  AND recorded_at < $3`

	if err := s.db.QueryRowContext(ctx, totalQ, tenantID, start, end).Scan(&report.TotalTokens, &report.TotalRuns); err != nil {
		return nil, fmt.Errorf("usage total query: %w", err)
	}

	// Per-model aggregation.
	const modelQ = `
SELECT COALESCE(model, ''), COALESCE(SUM(tokens), 0), COUNT(*)
FROM usage_records
WHERE tenant_id = $1
  AND recorded_at >= $2
  AND recorded_at < $3
GROUP BY model
ORDER BY SUM(tokens) DESC`

	modelRows, err := s.db.QueryContext(ctx, modelQ, tenantID, start, end)
	if err != nil {
		return nil, fmt.Errorf("usage model query: %w", err)
	}
	defer modelRows.Close()

	for modelRows.Next() {
		var m UsageByModel
		if err := modelRows.Scan(&m.Model, &m.Tokens, &m.Runs); err != nil {
			return nil, fmt.Errorf("scan model row: %w", err)
		}
		report.ByModel = append(report.ByModel, m)
	}
	if err := modelRows.Err(); err != nil {
		return nil, fmt.Errorf("model rows iteration: %w", err)
	}

	// Per-bucket aggregation.
	// SECURITY: dateFmt is interpolated into SQL via fmt.Sprintf. It MUST
	// only contain Postgres date format literals — never user-controlled values.
	// The switch validates granularity to known-safe values.
	var dateFmt string
	switch granularity {
	case "hour":
		dateFmt = "YYYY-MM-DD\"T\"HH24:00:00\"Z\""
	case "day", "":
		dateFmt = "YYYY-MM-DD"
	default:
		return nil, fmt.Errorf("invalid granularity %q: must be \"hour\" or \"day\"", granularity)
	}

	bucketQ := fmt.Sprintf(`
SELECT to_char(recorded_at, '%s'), COALESCE(SUM(tokens), 0), COUNT(*)
FROM usage_records
WHERE tenant_id = $1
  AND recorded_at >= $2
  AND recorded_at < $3
GROUP BY to_char(recorded_at, '%s')
ORDER BY to_char(recorded_at, '%s')`, dateFmt, dateFmt, dateFmt)

	bucketRows, err := s.db.QueryContext(ctx, bucketQ, tenantID, start, end)
	if err != nil {
		return nil, fmt.Errorf("usage bucket query: %w", err)
	}
	defer bucketRows.Close()

	for bucketRows.Next() {
		var b UsageByBucket
		if err := bucketRows.Scan(&b.Date, &b.Tokens, &b.Runs); err != nil {
			return nil, fmt.Errorf("scan bucket row: %w", err)
		}
		report.ByBucket = append(report.ByBucket, b)
	}
	if err := bucketRows.Err(); err != nil {
		return nil, fmt.Errorf("bucket rows iteration: %w", err)
	}

	return report, nil
}

// Close closes the underlying database connection.
func (s *UsageQueryPGStore) Close() error {
	return s.db.Close()
}
