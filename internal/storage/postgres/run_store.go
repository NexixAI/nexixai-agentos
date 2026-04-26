package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/lib/pq"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// RunStore is a PostgreSQL-backed implementation of storage.RunStore.
type RunStore struct {
	db *sql.DB
}

// NewRunStore opens a connection pool and returns a ready-to-use RunStore.
func NewRunStore(cfg config.PostgresConfig) (*RunStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &RunStore{db: db}, nil
}

// NewRunStoreFromDB wraps an existing *sql.DB as a RunStore.
func NewRunStoreFromDB(db *sql.DB) *RunStore {
	return &RunStore{db: db}
}

func (s *RunStore) Create(ctx context.Context, run types.Run) error {
	optionsJSON, err := json.Marshal(run.RunOptions)
	if err != nil {
		return fmt.Errorf("marshal run_options: %w", err)
	}
	outputJSON, err := marshalNullable(run.Output)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}
	errorJSON, err := marshalNullable(run.Error)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	const q = `
INSERT INTO runs (tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	_, err = s.db.ExecContext(ctx, q,
		run.TenantID, run.AgentID, run.RunID, run.Status,
		run.CreatedAt, run.StartedAt, run.CompletedAt, run.EventsURL,
		optionsJSON, outputJSON, errorJSON, run.IdempotencyKey,
	)
	return err
}

func (s *RunStore) Get(ctx context.Context, tenantID, runID string) (types.Run, bool, error) {
	if tenantID == "" || runID == "" {
		return types.Run{}, false, nil
	}

	const q = `
SELECT tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key
FROM runs
WHERE tenant_id = $1 AND run_id = $2`

	run, err := scanRun(s.db.QueryRowContext(ctx, q, tenantID, runID))
	if err == sql.ErrNoRows {
		return types.Run{}, false, nil
	}
	if err != nil {
		return types.Run{}, false, err
	}
	return run, true, nil
}

func (s *RunStore) GetByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (types.Run, bool, error) {
	if tenantID == "" || idempotencyKey == "" {
		return types.Run{}, false, nil
	}

	const q = `
SELECT tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key
FROM runs
WHERE tenant_id = $1 AND idempotency_key = $2`

	run, err := scanRun(s.db.QueryRowContext(ctx, q, tenantID, idempotencyKey))
	if err == sql.ErrNoRows {
		return types.Run{}, false, nil
	}
	if err != nil {
		return types.Run{}, false, err
	}
	return run, true, nil
}

func (s *RunStore) Save(ctx context.Context, run types.Run) error {
	optionsJSON, err := json.Marshal(run.RunOptions)
	if err != nil {
		return fmt.Errorf("marshal run_options: %w", err)
	}
	outputJSON, err := marshalNullable(run.Output)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}
	errorJSON, err := marshalNullable(run.Error)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	const q = `
INSERT INTO runs (tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (tenant_id, run_id) DO UPDATE SET
    agent_id        = EXCLUDED.agent_id,
    status          = EXCLUDED.status,
    created_at      = EXCLUDED.created_at,
    started_at      = EXCLUDED.started_at,
    completed_at    = EXCLUDED.completed_at,
    events_url      = EXCLUDED.events_url,
    run_options     = EXCLUDED.run_options,
    output          = EXCLUDED.output,
    error           = EXCLUDED.error,
    idempotency_key = EXCLUDED.idempotency_key`

	_, err = s.db.ExecContext(ctx, q,
		run.TenantID, run.AgentID, run.RunID, run.Status,
		run.CreatedAt, run.StartedAt, run.CompletedAt, run.EventsURL,
		optionsJSON, outputJSON, errorJSON, run.IdempotencyKey,
	)
	return err
}

// defaultListLimit is the maximum number of rows returned by List when no
// explicit limit is provided, preventing unbounded result sets (v9.0 L-2).
const defaultListLimit = 1000

func (s *RunStore) List(ctx context.Context, tenantID string) ([]types.Run, error) {
	if tenantID == "" {
		return nil, nil
	}

	const q = `
SELECT tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key
FROM runs
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2`

	rows, err := s.db.QueryContext(ctx, q, tenantID, defaultListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []types.Run
	for rows.Next() {
		run, err := scanRunFromRows(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *RunStore) ListByAgent(ctx context.Context, tenantID, agentID string, limit int, afterRunID string) ([]types.Run, error) {
	if tenantID == "" || agentID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	if afterRunID != "" {
		const q = `
SELECT tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key
FROM runs
WHERE tenant_id = $1 AND agent_id = $2 AND created_at < (SELECT created_at FROM runs WHERE tenant_id = $1 AND run_id = $3)
ORDER BY created_at DESC
LIMIT $4`
		rows, err := s.db.QueryContext(ctx, q, tenantID, agentID, afterRunID, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var runs []types.Run
		for rows.Next() {
			run, err := scanRunFromRows(rows)
			if err != nil {
				return nil, err
			}
			runs = append(runs, run)
		}
		return runs, rows.Err()
	}

	const q = `
SELECT tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key
FROM runs
WHERE tenant_id = $1 AND agent_id = $2
ORDER BY created_at DESC
LIMIT $3`
	rows, err := s.db.QueryContext(ctx, q, tenantID, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []types.Run
	for rows.Next() {
		run, err := scanRunFromRows(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *RunStore) ListChildRuns(ctx context.Context, tenantID, parentRunID string) ([]types.Run, error) {
	if tenantID == "" || parentRunID == "" {
		return nil, nil
	}

	const q = `
SELECT tenant_id, agent_id, run_id, status, created_at, started_at, completed_at, events_url, run_options, output, error, idempotency_key
FROM runs
WHERE tenant_id = $1 AND (retry_of = $2 OR parent_run_id = $2)
ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, q, tenantID, parentRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []types.Run
	for rows.Next() {
		run, err := scanRunFromRows(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *RunStore) Close() error {
	return s.db.Close()
}

// scanRun scans a single row into a types.Run.
func scanRun(row *sql.Row) (types.Run, error) {
	var r types.Run
	var optionsJSON, outputJSON, errorJSON []byte
	err := row.Scan(
		&r.TenantID, &r.AgentID, &r.RunID, &r.Status,
		&r.CreatedAt, &r.StartedAt, &r.CompletedAt, &r.EventsURL,
		&optionsJSON, &outputJSON, &errorJSON, &r.IdempotencyKey,
	)
	if err != nil {
		return types.Run{}, err
	}
	if err := unmarshalRunJSON(&r, optionsJSON, outputJSON, errorJSON); err != nil {
		return types.Run{}, err
	}
	return r, nil
}

// scanRunFromRows scans the current row from sql.Rows into a types.Run.
func scanRunFromRows(rows *sql.Rows) (types.Run, error) {
	var r types.Run
	var optionsJSON, outputJSON, errorJSON []byte
	err := rows.Scan(
		&r.TenantID, &r.AgentID, &r.RunID, &r.Status,
		&r.CreatedAt, &r.StartedAt, &r.CompletedAt, &r.EventsURL,
		&optionsJSON, &outputJSON, &errorJSON, &r.IdempotencyKey,
	)
	if err != nil {
		return types.Run{}, err
	}
	if err := unmarshalRunJSON(&r, optionsJSON, outputJSON, errorJSON); err != nil {
		return types.Run{}, err
	}
	return r, nil
}

func unmarshalRunJSON(r *types.Run, optionsJSON, outputJSON, errorJSON []byte) error {
	if len(optionsJSON) > 0 {
		if err := json.Unmarshal(optionsJSON, &r.RunOptions); err != nil {
			return fmt.Errorf("unmarshal run_options: %w", err)
		}
	}
	if len(outputJSON) > 0 {
		r.Output = &types.RunOutput{}
		if err := json.Unmarshal(outputJSON, r.Output); err != nil {
			return fmt.Errorf("unmarshal output: %w", err)
		}
	}
	if len(errorJSON) > 0 {
		r.Error = &types.RunError{}
		if err := json.Unmarshal(errorJSON, r.Error); err != nil {
			return fmt.Errorf("unmarshal error: %w", err)
		}
	}
	return nil
}

// marshalNullable returns nil (SQL NULL) when v is nil, otherwise JSON bytes.
func marshalNullable(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// OpenDB creates a *sql.DB connection pool from config.
func OpenDB(cfg config.PostgresConfig) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.Name, cfg.User, cfg.Password, cfg.SSLMode,
	)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(cfg.PoolSize)
	db.SetMaxIdleConns(cfg.PoolSize)
	return db, nil
}
