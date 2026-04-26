package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"

	_ "github.com/lib/pq"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/crypto"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// KVStore is a PostgreSQL-backed implementation of storage.KVStore.
type KVStore struct {
	db              *sql.DB
	maxValueSize    int
	maxKeysPerAgent int
	enc             *crypto.Encryptor
}

// NewKVStore opens a connection pool and returns a ready-to-use KVStore.
func NewKVStore(cfg config.PostgresConfig, maxValueSize, maxKeysPerAgent int) (*KVStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	if maxValueSize <= 0 {
		maxValueSize = 65536
	}
	if maxKeysPerAgent <= 0 {
		maxKeysPerAgent = 1000
	}
	return &KVStore{db: db, maxValueSize: maxValueSize, maxKeysPerAgent: maxKeysPerAgent}, nil
}

// NewKVStoreFromDB wraps an existing *sql.DB as a KVStore.
func NewKVStoreFromDB(db *sql.DB, maxValueSize, maxKeysPerAgent int) *KVStore {
	if maxValueSize <= 0 {
		maxValueSize = 65536
	}
	if maxKeysPerAgent <= 0 {
		maxKeysPerAgent = 1000
	}
	return &KVStore{db: db, maxValueSize: maxValueSize, maxKeysPerAgent: maxKeysPerAgent}
}

// SetEncryptor configures an optional encryptor for at-rest encryption of values.
func (s *KVStore) SetEncryptor(enc *crypto.Encryptor) {
	s.enc = enc
}

func (s *KVStore) Get(ctx context.Context, tenantID, agentID, key string) (string, bool, error) {
	if err := types.ValidateKey(key); err != nil {
		return "", false, err
	}

	const q = `SELECT value FROM kv_store WHERE tenant_id = $1 AND agent_id = $2 AND key = $3`

	var value string
	err := s.db.QueryRowContext(ctx, q, tenantID, agentID, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		metrics.IncKVOp("get", "error")
		return "", false, err
	}

	if s.enc != nil {
		decoded, decErr := base64.StdEncoding.DecodeString(value)
		if decErr != nil {
			// Not base64 — likely plaintext from before encryption was enabled.
			slog.Warn("kv_store: value is not base64-encoded, returning as plaintext", "key", key)
			return value, true, nil
		}
		plaintext, decErr := s.enc.Decrypt(decoded, []byte(tenantID))
		if decErr != nil {
			// Decryption failed — likely plaintext that happened to be valid base64.
			slog.Warn("kv_store: decryption failed, returning value as-is", "key", key, "error", decErr)
			return value, true, nil
		}
		return string(plaintext), true, nil
	}

	metrics.IncKVOp("get", "ok")
	return value, true, nil
}

func (s *KVStore) Set(ctx context.Context, tenantID, agentID, key, value string) error {
	if tenantID == "" || agentID == "" {
		return errors.New("tenantID and agentID are required")
	}
	if err := types.ValidateKey(key); err != nil {
		return err
	}
	if err := types.ValidateValueSize(value, s.maxValueSize); err != nil {
		return err
	}

	// Check key count limit within a transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Check if this key already exists
	var exists bool
	err = tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM kv_store WHERE tenant_id = $1 AND agent_id = $2 AND key = $3)`,
		tenantID, agentID, key,
	).Scan(&exists)
	if err != nil {
		return err
	}

	if !exists {
		// Check key count
		var count int
		err = tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM kv_store WHERE tenant_id = $1 AND agent_id = $2`,
			tenantID, agentID,
		).Scan(&count)
		if err != nil {
			return err
		}
		if count >= s.maxKeysPerAgent {
			return types.ErrMaxKeysExceeded
		}
	}

	storeValue := value
	if s.enc != nil {
		encrypted, encErr := s.enc.Encrypt([]byte(value), []byte(tenantID))
		if encErr != nil {
			return encErr
		}
		storeValue = base64.StdEncoding.EncodeToString(encrypted)
	}

	const q = `
INSERT INTO kv_store (tenant_id, agent_id, key, value, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (tenant_id, agent_id, key) DO UPDATE SET
    value      = EXCLUDED.value,
    updated_at = EXCLUDED.updated_at`

	_, err = tx.ExecContext(ctx, q, tenantID, agentID, key, storeValue)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		metrics.IncKVOp("set", "error")
		return err
	}
	metrics.IncKVOp("set", "ok")
	return nil
}

func (s *KVStore) Delete(ctx context.Context, tenantID, agentID, key string) error {
	if err := types.ValidateKey(key); err != nil {
		return err
	}

	const q = `DELETE FROM kv_store WHERE tenant_id = $1 AND agent_id = $2 AND key = $3`
	_, err := s.db.ExecContext(ctx, q, tenantID, agentID, key)
	if err != nil {
		metrics.IncKVOp("delete", "error")
		return err
	}
	metrics.IncKVOp("delete", "ok")
	return nil
}

func (s *KVStore) ListKeys(ctx context.Context, tenantID, agentID string) ([]string, error) {
	if tenantID == "" || agentID == "" {
		return nil, nil
	}

	const q = `SELECT key FROM kv_store WHERE tenant_id = $1 AND agent_id = $2 ORDER BY key`

	rows, err := s.db.QueryContext(ctx, q, tenantID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		metrics.IncKVOp("list", "error")
		return nil, err
	}
	metrics.IncKVOp("list", "ok")
	return keys, nil
}

func (s *KVStore) Close() error {
	return s.db.Close()
}
