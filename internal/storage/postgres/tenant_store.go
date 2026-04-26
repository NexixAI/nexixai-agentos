package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

// Tenant represents a multi-tenant record in Postgres.
type Tenant struct {
	TenantID  string     `json:"tenant_id"`
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	Plan      string     `json:"plan"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// TenantUpdate holds optional fields for updating a tenant.
type TenantUpdate struct {
	Name *string
	Plan *string
}

// TenantMember represents a membership row.
type TenantMember struct {
	TenantID    string    `json:"tenant_id"`
	PrincipalID string    `json:"principal_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var (
	// ErrTenantNotFound signals the requested tenant does not exist.
	ErrTenantNotFound = errors.New("tenant not found")
	// ErrSlugConflict signals a slug uniqueness violation.
	ErrSlugConflict = errors.New("tenant slug already exists")
)

// TenantStore defines the interface for tenant persistence.
type TenantStore interface {
	Create(ctx context.Context, tenant Tenant) error
	Get(ctx context.Context, tenantID string) (*Tenant, error)
	Update(ctx context.Context, tenantID string, updates TenantUpdate) error
	Delete(ctx context.Context, tenantID string) error
	List(ctx context.Context) ([]Tenant, error)
	AddMember(ctx context.Context, member TenantMember) error
}

// PgTenantStore is a PostgreSQL-backed tenant store.
type PgTenantStore struct {
	db *sql.DB
}

// NewTenantStore opens a connection pool and returns a ready-to-use PgTenantStore.
func NewTenantStore(cfg config.PostgresConfig) (*PgTenantStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &PgTenantStore{db: db}, nil
}

// NewTenantStoreFromDB wraps an existing *sql.DB as a PgTenantStore.
func NewTenantStoreFromDB(db *sql.DB) *PgTenantStore {
	return &PgTenantStore{db: db}
}

func (s *PgTenantStore) Create(ctx context.Context, tenant Tenant) error {
	if tenant.CreatedAt.IsZero() {
		tenant.CreatedAt = time.Now().UTC()
	}
	if tenant.UpdatedAt.IsZero() {
		tenant.UpdatedAt = tenant.CreatedAt
	}

	const q = `
INSERT INTO tenants (tenant_id, name, slug, plan, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := s.db.ExecContext(ctx, q,
		tenant.TenantID, tenant.Name, tenant.Slug, tenant.Plan,
		tenant.CreatedAt, tenant.UpdatedAt,
	)
	if err != nil {
		// Detect unique constraint violation on slug.
		if isUniqueViolation(err) {
			return ErrSlugConflict
		}
		return fmt.Errorf("insert tenant: %w", err)
	}
	return nil
}

func (s *PgTenantStore) Get(ctx context.Context, tenantID string) (*Tenant, error) {
	if tenantID == "" {
		return nil, ErrTenantNotFound
	}

	const q = `
SELECT tenant_id, name, slug, plan, created_at, updated_at, deleted_at
FROM tenants
WHERE tenant_id = $1 AND deleted_at IS NULL`

	var t Tenant
	err := s.db.QueryRowContext(ctx, q, tenantID).Scan(
		&t.TenantID, &t.Name, &t.Slug, &t.Plan,
		&t.CreatedAt, &t.UpdatedAt, &t.DeletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	return &t, nil
}

func (s *PgTenantStore) Update(ctx context.Context, tenantID string, updates TenantUpdate) error {
	if tenantID == "" {
		return ErrTenantNotFound
	}

	const q = `
UPDATE tenants
SET name = COALESCE($2, name),
    plan = COALESCE($3, plan),
    updated_at = $4
WHERE tenant_id = $1 AND deleted_at IS NULL`

	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, q, tenantID, nullableString(updates.Name), nullableString(updates.Plan), now)
	if err != nil {
		return fmt.Errorf("update tenant: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update tenant rows affected: %w", err)
	}
	if rows == 0 {
		return ErrTenantNotFound
	}
	return nil
}

func (s *PgTenantStore) Delete(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return ErrTenantNotFound
	}

	const q = `
UPDATE tenants
SET deleted_at = $2
WHERE tenant_id = $1 AND deleted_at IS NULL`

	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, q, tenantID, now)
	if err != nil {
		return fmt.Errorf("soft delete tenant: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("soft delete tenant rows affected: %w", err)
	}
	if rows == 0 {
		return ErrTenantNotFound
	}
	return nil
}

func (s *PgTenantStore) List(ctx context.Context) ([]Tenant, error) {
	const q = `
SELECT tenant_id, name, slug, plan, created_at, updated_at, deleted_at
FROM tenants
WHERE deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1000`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	tenants := make([]Tenant, 0, 64)
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.TenantID, &t.Name, &t.Slug, &t.Plan, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

func (s *PgTenantStore) AddMember(ctx context.Context, member TenantMember) error {
	if member.CreatedAt.IsZero() {
		member.CreatedAt = time.Now().UTC()
	}
	if member.UpdatedAt.IsZero() {
		member.UpdatedAt = member.CreatedAt
	}

	const q = `
INSERT INTO tenant_members (tenant_id, principal_id, role, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, principal_id) DO UPDATE SET role = EXCLUDED.role, updated_at = EXCLUDED.updated_at`

	_, err := s.db.ExecContext(ctx, q, member.TenantID, member.PrincipalID, member.Role, member.CreatedAt, member.UpdatedAt)
	if err != nil {
		return fmt.Errorf("add tenant member: %w", err)
	}
	return nil
}

// tenantDataTables is the hardcoded list of tables that store per-tenant data.
// SECURITY: these are SQL identifiers — never accept external input here.
// Validated at init time by verifying each name matches ^[a-z_]+$.
var tenantDataTables = []string{
	"runs",
	"agents",
	"audit_events",
	"usage_records",
	"conversation_memory",
	"kv_store",
	"event_log",
	"api_keys",
	"tenant_members",
}

func init() {
	for _, t := range tenantDataTables {
		for _, c := range t {
			if !((c >= 'a' && c <= 'z') || c == '_') {
				panic(fmt.Sprintf("invalid table name in tenantDataTables: %q", t))
			}
		}
	}
}

// DeleteAllData hard-deletes all data for a tenant across all tables.
// Used by the tenant deletion flow. Deletes in batches to avoid long locks.
func (s *PgTenantStore) DeleteAllData(ctx context.Context, tenantID string) error {
	for _, table := range tenantDataTables {
		for {
			q := fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1 AND ctid IN (SELECT ctid FROM %s WHERE tenant_id = $1 LIMIT 1000)", table, table)
			res, err := s.db.ExecContext(ctx, q, tenantID)
			if err != nil {
				return fmt.Errorf("delete from %s: %w", table, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("rows affected %s: %w", table, err)
			}
			if n == 0 {
				break
			}
		}
	}

	// Finally remove the tenant record itself (hard delete).
	const q = `DELETE FROM tenants WHERE tenant_id = $1`
	_, err := s.db.ExecContext(ctx, q, tenantID)
	if err != nil {
		return fmt.Errorf("delete tenant record: %w", err)
	}
	return nil
}

func (s *PgTenantStore) Close() error {
	return s.db.Close()
}

// DB returns the underlying database connection for use by related subsystems.
func (s *PgTenantStore) DB() *sql.DB {
	return s.db
}

// nullableString converts *string to a sql.NullString.
func nullableString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// isUniqueViolation checks if the error is a Postgres unique constraint violation (23505).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq errors implement Error() with "pq: ..." and have a Code field.
	// We check the string since we don't want to import pq error types directly.
	return contains(err.Error(), "duplicate key") || contains(err.Error(), "23505")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
