package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/id"
)

// JobsTableDDL is the SQL DDL for the jobs table. It is registered as a
// migration in internal/storage/postgres/migrations.go.
const JobsTableDDL = `
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    job_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_jobs_dequeue ON jobs (status, next_run_at);
`

// PostgresQueue is a Queue backed by a Postgres database.
type PostgresQueue struct {
	db *sql.DB
}

// NewPostgresQueue creates a new PostgresQueue. The caller is responsible for
// ensuring the jobs table exists (e.g., via Migrate).
func NewPostgresQueue(db *sql.DB) *PostgresQueue {
	return &PostgresQueue{db: db}
}

// Enqueue inserts a new pending job.
func (q *PostgresQueue) Enqueue(ctx context.Context, jobType JobType, payload any, opts ...EnqueueOption) (string, error) {
	o := defaultEnqueueOptions()
	for _, fn := range opts {
		fn(&o)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal job payload: %w", err)
	}

	jobID := id.New("job")
	nextRun := time.Now().Add(o.Delay)

	_, err = q.db.ExecContext(ctx,
		`INSERT INTO jobs (id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, last_error)
		 VALUES ($1, $2, $3, 'pending', 0, $4, $5, NOW(), '')`,
		jobID, string(jobType), json.RawMessage(raw), o.MaxAttempts, nextRun,
	)
	if err != nil {
		return "", fmt.Errorf("insert job: %w", err)
	}
	return jobID, nil
}

// Dequeue atomically claims the next eligible pending job, or returns nil, nil
// when none is available. Uses FOR UPDATE SKIP LOCKED for concurrency safety.
func (q *PostgresQueue) Dequeue(ctx context.Context) (*Job, error) {
	row := q.db.QueryRowContext(ctx,
		`UPDATE jobs
		 SET status = 'running', attempts = attempts + 1
		 WHERE id = (
		     SELECT id FROM jobs
		     WHERE status = 'pending' AND next_run_at <= NOW()
		     ORDER BY next_run_at
		     LIMIT 1
		     FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, last_error`,
	)

	j := &Job{}
	err := row.Scan(&j.ID, &j.Type, &j.Payload, &j.Status, &j.Attempts,
		&j.MaxAttempts, &j.NextRunAt, &j.CreatedAt, &j.LastError)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeue job: %w", err)
	}
	return j, nil
}

// Complete marks a job as completed.
func (q *PostgresQueue) Complete(ctx context.Context, jobID string) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'completed' WHERE id = $1`, jobID)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete job rows affected: %w", err)
	}
	if n == 0 {
		return ErrJobNotFound
	}
	return nil
}

// Fail marks a job as failed. If the job has remaining retries, it is
// rescheduled with exponential backoff (base 5s, factor 3). Otherwise the
// status becomes "failed".
func (q *PostgresQueue) Fail(ctx context.Context, jobID string, errMsg string) error {
	// Read current state.
	var attempts, maxAttempts int
	err := q.db.QueryRowContext(ctx,
		`SELECT attempts, max_attempts FROM jobs WHERE id = $1`, jobID,
	).Scan(&attempts, &maxAttempts)
	if err == sql.ErrNoRows {
		return ErrJobNotFound
	}
	if err != nil {
		return fmt.Errorf("read job for fail: %w", err)
	}

	if attempts < maxAttempts {
		nextRun := time.Now().Add(backoff(attempts))
		_, err = q.db.ExecContext(ctx,
			`UPDATE jobs SET status = 'pending', last_error = $2, next_run_at = $3 WHERE id = $1`,
			jobID, errMsg, nextRun,
		)
	} else {
		_, err = q.db.ExecContext(ctx,
			`UPDATE jobs SET status = 'failed', last_error = $2 WHERE id = $1`,
			jobID, errMsg,
		)
	}
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return nil
}
