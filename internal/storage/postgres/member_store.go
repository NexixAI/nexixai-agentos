package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrMemberNotFound is returned when no membership row exists for a
// given (tenant_id, principal_id) pair.
var ErrMemberNotFound = errors.New("member not found")

// Member represents a row in the tenant_members table.
type Member struct {
	TenantID    string    `json:"tenant_id"`
	PrincipalID string    `json:"principal_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MemberStore defines the interface for tenant membership operations.
type MemberStore interface {
	GetRole(ctx context.Context, tenantID, principalID string) (string, error)
	SetRole(ctx context.Context, tenantID, principalID, role string) error
	CountMembers(ctx context.Context, tenantID string) (int, error)
	DeleteMember(ctx context.Context, tenantID, principalID string) error
	ListMembers(ctx context.Context, tenantID string) ([]Member, error)
}

// PgMemberStore is the Postgres-backed implementation of MemberStore.
type PgMemberStore struct {
	db *sql.DB
}

// NewMemberStore returns a PgMemberStore backed by the given *sql.DB.
func NewMemberStore(db *sql.DB) *PgMemberStore {
	return &PgMemberStore{db: db}
}

// GetRole returns the role for the given tenant/principal, or ErrMemberNotFound.
func (s *PgMemberStore) GetRole(ctx context.Context, tenantID, principalID string) (string, error) {
	const q = `SELECT role FROM tenant_members WHERE tenant_id = $1 AND principal_id = $2`
	var role string
	err := s.db.QueryRowContext(ctx, q, tenantID, principalID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrMemberNotFound
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// SetRole upserts a membership row.
func (s *PgMemberStore) SetRole(ctx context.Context, tenantID, principalID, role string) error {
	const q = `
INSERT INTO tenant_members (tenant_id, principal_id, role, created_at, updated_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (tenant_id, principal_id) DO UPDATE SET role = EXCLUDED.role, updated_at = NOW()`
	_, err := s.db.ExecContext(ctx, q, tenantID, principalID, role)
	return err
}

// CountMembers returns the number of members for a tenant.
func (s *PgMemberStore) CountMembers(ctx context.Context, tenantID string) (int, error) {
	const q = `SELECT COUNT(*) FROM tenant_members WHERE tenant_id = $1`
	var count int
	err := s.db.QueryRowContext(ctx, q, tenantID).Scan(&count)
	return count, err
}

// DeleteMember removes a membership row.
func (s *PgMemberStore) DeleteMember(ctx context.Context, tenantID, principalID string) error {
	const q = `DELETE FROM tenant_members WHERE tenant_id = $1 AND principal_id = $2`
	res, err := s.db.ExecContext(ctx, q, tenantID, principalID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// ListMembers returns all members for a tenant ordered by created_at.
func (s *PgMemberStore) ListMembers(ctx context.Context, tenantID string) ([]Member, error) {
	const q = `SELECT tenant_id, principal_id, role, created_at, updated_at
	           FROM tenant_members WHERE tenant_id = $1 ORDER BY created_at`
	rows, err := s.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.TenantID, &m.PrincipalID, &m.Role, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return members, nil
}
