package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// DefaultMemoryQuotaBytes is the default per-agent per-tenant memory quota (100 MB).
const DefaultMemoryQuotaBytes int64 = 100 * 1024 * 1024

// MemoryEntry represents a single stored memory record.
type MemoryEntry struct {
	Key       string            `json:"key"`
	Value     string            `json:"value"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	SizeBytes int64             `json:"size_bytes"`
}

// AgentMemoryStore abstracts agent memory persistence.
// Implementations MUST enforce per-agent per-tenant namespace isolation:
// every query filters by agent_id AND tenant_id.
type AgentMemoryStore interface {
	// Store upserts a memory entry. If ttl is zero, the entry has no expiry.
	Store(ctx context.Context, agentID, tenantID, key, value string, ttl time.Duration, metadata map[string]string) error
	// Recall returns the entry for an exact key match, or nil if not found or expired.
	Recall(ctx context.Context, agentID, tenantID, key string) (*MemoryEntry, error)
	// Search performs a keyword search on key and value columns using ILIKE.
	Search(ctx context.Context, agentID, tenantID, query string, limit int) ([]MemoryEntry, error)
	// Delete removes a memory entry.
	Delete(ctx context.Context, agentID, tenantID, key string) error
	// GetUsage returns the total bytes stored for an agent within a tenant.
	GetUsage(ctx context.Context, agentID, tenantID string) (int64, error)
	// GetThread returns memory entries for a thread (keys prefixed with "thread:{threadID}:").
	GetThread(ctx context.Context, agentID, tenantID, threadID string, limit int) ([]MemoryEntry, error)
	// SemanticSearch performs fuzzy search using pg_trgm similarity on key and value columns.
	SemanticSearch(ctx context.Context, agentID, tenantID, query string, limit int) ([]MemoryEntry, error)
}

// PostgresAgentMemoryStore implements AgentMemoryStore backed by Postgres.
type PostgresAgentMemoryStore struct {
	db    *sql.DB
	quota int64
}

// NewPostgresAgentMemoryStore creates a PostgresAgentMemoryStore with the given database
// and default quota. Pass 0 to use DefaultMemoryQuotaBytes.
func NewPostgresAgentMemoryStore(db *sql.DB, quota int64) *PostgresAgentMemoryStore {
	if quota <= 0 {
		quota = DefaultMemoryQuotaBytes
	}
	return &PostgresAgentMemoryStore{db: db, quota: quota}
}

// Store upserts a memory entry. It checks the quota before inserting and
// rejects writes that would exceed the per-agent per-tenant limit.
func (s *PostgresAgentMemoryStore) Store(ctx context.Context, agentID, tenantID, key, value string, ttl time.Duration, metadata map[string]string) error {
	if agentID == "" {
		return fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return fmt.Errorf("agentmemory: tenant_id must not be empty")
	}
	if key == "" {
		return fmt.Errorf("agentmemory: key must not be empty")
	}

	sizeBytes := int64(len(value))

	// Check quota: current usage minus existing entry size (for upsert) plus new size.
	currentUsage, err := s.GetUsage(ctx, agentID, tenantID)
	if err != nil {
		return fmt.Errorf("agentmemory: check quota: %w", err)
	}

	// If the key already exists, subtract its current size from usage (upsert case).
	var existingSize int64
	err = s.db.QueryRowContext(ctx,
		"SELECT size_bytes FROM agent_memory WHERE agent_id = $1 AND tenant_id = $2 AND key = $3",
		agentID, tenantID, key,
	).Scan(&existingSize)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("agentmemory: check existing entry: %w", err)
	}

	netUsage := currentUsage - existingSize + sizeBytes
	if netUsage > s.quota {
		return fmt.Errorf("agentmemory: quota exceeded (usage %d + new %d > quota %d)", currentUsage-existingSize, sizeBytes, s.quota)
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("agentmemory: marshal metadata: %w", err)
	}

	now := time.Now().UTC()
	var expiresAt *time.Time
	if ttl > 0 {
		t := now.Add(ttl)
		expiresAt = &t
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agent_memory (agent_id, tenant_id, key, value, metadata, size_bytes, created_at, updated_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (agent_id, tenant_id, key) DO UPDATE
		 SET value = EXCLUDED.value,
		     metadata = EXCLUDED.metadata,
		     size_bytes = EXCLUDED.size_bytes,
		     updated_at = EXCLUDED.updated_at,
		     expires_at = EXCLUDED.expires_at`,
		agentID, tenantID, key, value, metadataJSON, sizeBytes, now, now, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("agentmemory: upsert failed: %w", err)
	}

	slog.Debug("agentmemory: stored",
		"agent_id", agentID,
		"tenant_id", tenantID,
		"key", key,
		"size_bytes", sizeBytes,
	)

	return nil
}

// Recall returns the entry for an exact key match, or nil if not found or expired.
func (s *PostgresAgentMemoryStore) Recall(ctx context.Context, agentID, tenantID, key string) (*MemoryEntry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("agentmemory: tenant_id must not be empty")
	}
	if key == "" {
		return nil, fmt.Errorf("agentmemory: key must not be empty")
	}

	var entry MemoryEntry
	var metadataJSON []byte
	var expiresAt sql.NullTime

	err := s.db.QueryRowContext(ctx,
		`SELECT key, value, metadata, created_at, expires_at, size_bytes
		 FROM agent_memory
		 WHERE agent_id = $1 AND tenant_id = $2 AND key = $3`,
		agentID, tenantID, key,
	).Scan(&entry.Key, &entry.Value, &metadataJSON, &entry.CreatedAt, &expiresAt, &entry.SizeBytes)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentmemory: recall failed: %w", err)
	}

	// Check TTL expiry on read.
	if expiresAt.Valid {
		entry.ExpiresAt = &expiresAt.Time
		if time.Now().UTC().After(expiresAt.Time) {
			// Lazy cleanup: entry is expired.
			slog.Debug("agentmemory: expired entry (lazy)",
				"agent_id", agentID,
				"key", key,
			)
			return nil, nil
		}
	}

	if len(metadataJSON) > 0 {
		entry.Metadata = make(map[string]string)
		if err := json.Unmarshal(metadataJSON, &entry.Metadata); err != nil {
			return nil, fmt.Errorf("agentmemory: unmarshal metadata: %w", err)
		}
	}

	return &entry, nil
}

// Search performs a keyword search on key and value columns using ILIKE.
func (s *PostgresAgentMemoryStore) Search(ctx context.Context, agentID, tenantID, query string, limit int) ([]MemoryEntry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("agentmemory: tenant_id must not be empty")
	}
	if query == "" {
		return nil, fmt.Errorf("agentmemory: query must not be empty")
	}
	if limit <= 0 {
		limit = 10
	}
	// Cap search results to prevent unbounded memory.
	const maxLimit = 100
	if limit > maxLimit {
		limit = maxLimit
	}

	pattern := "%" + query + "%"

	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value, metadata, created_at, expires_at, size_bytes
		 FROM agent_memory
		 WHERE agent_id = $1 AND tenant_id = $2
		   AND (key ILIKE $3 OR value ILIKE $3)
		   AND (expires_at IS NULL OR expires_at > NOW())
		 ORDER BY updated_at DESC
		 LIMIT $4`,
		agentID, tenantID, pattern, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("agentmemory: search failed: %w", err)
	}
	defer rows.Close()

	var results []MemoryEntry
	for rows.Next() {
		var entry MemoryEntry
		var metadataJSON []byte
		var expiresAt sql.NullTime

		if err := rows.Scan(&entry.Key, &entry.Value, &metadataJSON, &entry.CreatedAt, &expiresAt, &entry.SizeBytes); err != nil {
			return nil, fmt.Errorf("agentmemory: scan search result: %w", err)
		}

		if expiresAt.Valid {
			entry.ExpiresAt = &expiresAt.Time
		}

		if len(metadataJSON) > 0 {
			entry.Metadata = make(map[string]string)
			if err := json.Unmarshal(metadataJSON, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("agentmemory: unmarshal search metadata: %w", err)
			}
		}

		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agentmemory: iterate search results: %w", err)
	}

	return results, nil
}

// Delete removes a memory entry by key.
func (s *PostgresAgentMemoryStore) Delete(ctx context.Context, agentID, tenantID, key string) error {
	if agentID == "" {
		return fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return fmt.Errorf("agentmemory: tenant_id must not be empty")
	}
	if key == "" {
		return fmt.Errorf("agentmemory: key must not be empty")
	}

	_, err := s.db.ExecContext(ctx,
		"DELETE FROM agent_memory WHERE agent_id = $1 AND tenant_id = $2 AND key = $3",
		agentID, tenantID, key,
	)
	if err != nil {
		return fmt.Errorf("agentmemory: delete failed: %w", err)
	}

	return nil
}

// GetUsage returns the total bytes stored for an agent within a tenant.
func (s *PostgresAgentMemoryStore) GetUsage(ctx context.Context, agentID, tenantID string) (int64, error) {
	if agentID == "" {
		return 0, fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return 0, fmt.Errorf("agentmemory: tenant_id must not be empty")
	}

	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(size_bytes), 0) FROM agent_memory WHERE agent_id = $1 AND tenant_id = $2",
		agentID, tenantID,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("agentmemory: get usage failed: %w", err)
	}

	if total.Valid {
		return total.Int64, nil
	}
	return 0, nil
}

// GetThread returns memory entries whose keys are prefixed with "thread:{threadID}:".
func (s *PostgresAgentMemoryStore) GetThread(ctx context.Context, agentID, tenantID, threadID string, limit int) ([]MemoryEntry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("agentmemory: tenant_id must not be empty")
	}
	if threadID == "" {
		return nil, fmt.Errorf("agentmemory: thread_id must not be empty")
	}
	if limit <= 0 {
		limit = 50
	}
	const maxLimit = 100
	if limit > maxLimit {
		limit = maxLimit
	}

	prefix := "thread:" + threadID + ":%"

	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value, metadata, created_at, expires_at, size_bytes
		 FROM agent_memory
		 WHERE agent_id = $1 AND tenant_id = $2
		   AND key LIKE $3
		   AND (expires_at IS NULL OR expires_at > NOW())
		 ORDER BY created_at ASC
		 LIMIT $4`,
		agentID, tenantID, prefix, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("agentmemory: get thread failed: %w", err)
	}
	defer rows.Close()

	var results []MemoryEntry
	for rows.Next() {
		var entry MemoryEntry
		var metadataJSON []byte
		var expiresAt sql.NullTime

		if err := rows.Scan(&entry.Key, &entry.Value, &metadataJSON, &entry.CreatedAt, &expiresAt, &entry.SizeBytes); err != nil {
			return nil, fmt.Errorf("agentmemory: scan thread result: %w", err)
		}

		if expiresAt.Valid {
			entry.ExpiresAt = &expiresAt.Time
		}

		if len(metadataJSON) > 0 {
			entry.Metadata = make(map[string]string)
			if err := json.Unmarshal(metadataJSON, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("agentmemory: unmarshal thread metadata: %w", err)
			}
		}

		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agentmemory: iterate thread results: %w", err)
	}

	return results, nil
}

// SemanticSearch performs fuzzy search using pg_trgm similarity() on key and value columns.
func (s *PostgresAgentMemoryStore) SemanticSearch(ctx context.Context, agentID, tenantID, query string, limit int) ([]MemoryEntry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("agentmemory: tenant_id must not be empty")
	}
	if query == "" {
		return nil, fmt.Errorf("agentmemory: query must not be empty")
	}
	if limit <= 0 {
		limit = 10
	}
	const maxLimit = 100
	if limit > maxLimit {
		limit = maxLimit
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value, metadata, created_at, expires_at, size_bytes
		 FROM agent_memory
		 WHERE agent_id = $1 AND tenant_id = $2
		   AND (expires_at IS NULL OR expires_at > NOW())
		   AND (similarity(key, $3) > 0.1 OR similarity(value, $3) > 0.1)
		 ORDER BY GREATEST(similarity(key, $3), similarity(value, $3)) DESC
		 LIMIT $4`,
		agentID, tenantID, query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("agentmemory: semantic search failed: %w", err)
	}
	defer rows.Close()

	var results []MemoryEntry
	for rows.Next() {
		var entry MemoryEntry
		var metadataJSON []byte
		var expiresAt sql.NullTime

		if err := rows.Scan(&entry.Key, &entry.Value, &metadataJSON, &entry.CreatedAt, &expiresAt, &entry.SizeBytes); err != nil {
			return nil, fmt.Errorf("agentmemory: scan semantic result: %w", err)
		}

		if expiresAt.Valid {
			entry.ExpiresAt = &expiresAt.Time
		}

		if len(metadataJSON) > 0 {
			entry.Metadata = make(map[string]string)
			if err := json.Unmarshal(metadataJSON, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("agentmemory: unmarshal semantic metadata: %w", err)
			}
		}

		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agentmemory: iterate semantic results: %w", err)
	}

	return results, nil
}
