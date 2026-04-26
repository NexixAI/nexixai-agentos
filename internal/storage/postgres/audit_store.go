package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

// AuditEvent represents a single auditable action.
type AuditEvent struct {
	TenantID  string         `json:"tenant_id"`
	Action    string         `json:"action"`
	Actor     string         `json:"actor"`
	Detail    map[string]any `json:"detail,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// AuditStore is a PostgreSQL-backed store for audit events.
type AuditStore struct {
	db *sql.DB
}

// NewAuditStore opens a connection pool and returns a ready-to-use AuditStore.
func NewAuditStore(cfg config.PostgresConfig) (*AuditStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &AuditStore{db: db}, nil
}

// NewAuditStoreFromDB wraps an existing *sql.DB as an AuditStore.
func NewAuditStoreFromDB(db *sql.DB) *AuditStore {
	return &AuditStore{db: db}
}

func (s *AuditStore) Store(ctx context.Context, event AuditEvent) error {
	detailJSON, err := json.Marshal(event.Detail)
	if err != nil {
		return fmt.Errorf("marshal audit detail: %w", err)
	}

	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	const q = `
INSERT INTO audit_events (tenant_id, action, actor, detail, created_at)
VALUES ($1, $2, $3, $4, $5)`

	_, err = s.db.ExecContext(ctx, q, event.TenantID, event.Action, event.Actor, detailJSON, ts)
	return err
}

func (s *AuditStore) List(ctx context.Context, tenantID string, limit int) ([]AuditEvent, error) {
	if tenantID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}

	const q = `
SELECT tenant_id, action, actor, detail, created_at
FROM audit_events
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2`

	rows, err := s.db.QueryContext(ctx, q, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var detailJSON []byte
		if err := rows.Scan(&e.TenantID, &e.Action, &e.Actor, &detailJSON, &e.Timestamp); err != nil {
			return nil, err
		}
		if len(detailJSON) > 0 {
			if err := json.Unmarshal(detailJSON, &e.Detail); err != nil {
				return nil, fmt.Errorf("unmarshal audit detail: %w", err)
			}
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *AuditStore) Close() error {
	return s.db.Close()
}
