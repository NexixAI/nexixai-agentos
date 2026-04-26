package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrAPIKeyNotFound is returned when no API key matches the lookup criteria.
var ErrAPIKeyNotFound = errors.New("api key not found")

// APIKeyRecord represents a row in the api_keys table.
type APIKeyRecord struct {
	KeyID     string     `json:"key_id"`
	TenantID  string     `json:"tenant_id"`
	KeyHash   string     `json:"-"` // never expose in list responses
	KeyPrefix string     `json:"key_prefix"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// APIKeyStore defines the interface for API key persistence.
type APIKeyStore interface {
	Create(ctx context.Context, key APIKeyRecord) error
	GetByPrefix(ctx context.Context, tenantID, prefix string) (*APIKeyRecord, error)
	List(ctx context.Context, tenantID string) ([]APIKeyRecord, error)
	Revoke(ctx context.Context, tenantID, keyID string) error
}

// PgAPIKeyStore is the Postgres-backed implementation of APIKeyStore.
type PgAPIKeyStore struct {
	db *sql.DB
}

// NewAPIKeyStore returns a PgAPIKeyStore backed by the given *sql.DB.
func NewAPIKeyStore(db *sql.DB) *PgAPIKeyStore {
	return &PgAPIKeyStore{db: db}
}

// Create inserts a new API key record.
func (s *PgAPIKeyStore) Create(ctx context.Context, key APIKeyRecord) error {
	const q = `
INSERT INTO api_keys (key_id, tenant_id, key_hash, key_prefix, name, role, created_by, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := s.db.ExecContext(ctx, q,
		key.KeyID, key.TenantID, key.KeyHash, key.KeyPrefix,
		key.Name, key.Role, key.CreatedBy, key.CreatedAt, key.ExpiresAt,
	)
	return err
}

// GetByPrefix retrieves an API key record by tenant and prefix.
// Returns the full record including the hash (for validation).
func (s *PgAPIKeyStore) GetByPrefix(ctx context.Context, tenantID, prefix string) (*APIKeyRecord, error) {
	const q = `
SELECT key_id, tenant_id, key_hash, key_prefix, name, role, created_by, created_at, expires_at, revoked_at
FROM api_keys WHERE tenant_id = $1 AND key_prefix = $2`
	var rec APIKeyRecord
	err := s.db.QueryRowContext(ctx, q, tenantID, prefix).Scan(
		&rec.KeyID, &rec.TenantID, &rec.KeyHash, &rec.KeyPrefix,
		&rec.Name, &rec.Role, &rec.CreatedBy, &rec.CreatedAt,
		&rec.ExpiresAt, &rec.RevokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAPIKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// LookupByPrefix retrieves an API key record by prefix alone (without tenant scoping).
// Used by the auth middleware when no tenant context is available yet — the key itself
// is the source of the tenant identity. The prefix index makes this efficient.
func (s *PgAPIKeyStore) LookupByPrefix(ctx context.Context, prefix string) (*APIKeyRecord, error) {
	const q = `
SELECT key_id, tenant_id, key_hash, key_prefix, name, role, created_by, created_at, expires_at, revoked_at
FROM api_keys WHERE key_prefix = $1`
	var rec APIKeyRecord
	err := s.db.QueryRowContext(ctx, q, prefix).Scan(
		&rec.KeyID, &rec.TenantID, &rec.KeyHash, &rec.KeyPrefix,
		&rec.Name, &rec.Role, &rec.CreatedBy, &rec.CreatedAt,
		&rec.ExpiresAt, &rec.RevokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAPIKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// List returns all API key records for a tenant, with hashes omitted.
func (s *PgAPIKeyStore) List(ctx context.Context, tenantID string) ([]APIKeyRecord, error) {
	const q = `
SELECT key_id, tenant_id, key_prefix, name, role, created_by, created_at, expires_at, revoked_at
FROM api_keys WHERE tenant_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKeyRecord
	for rows.Next() {
		var rec APIKeyRecord
		if err := rows.Scan(
			&rec.KeyID, &rec.TenantID, &rec.KeyPrefix,
			&rec.Name, &rec.Role, &rec.CreatedBy, &rec.CreatedAt,
			&rec.ExpiresAt, &rec.RevokedAt,
		); err != nil {
			return nil, err
		}
		keys = append(keys, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// Revoke sets the revoked_at timestamp on an API key.
func (s *PgAPIKeyStore) Revoke(ctx context.Context, tenantID, keyID string) error {
	const q = `UPDATE api_keys SET revoked_at = NOW() WHERE tenant_id = $1 AND key_id = $2 AND revoked_at IS NULL`
	res, err := s.db.ExecContext(ctx, q, tenantID, keyID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}
