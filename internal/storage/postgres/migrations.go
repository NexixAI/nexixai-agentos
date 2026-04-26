package postgres

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
)

// migration represents a single numbered schema migration.
type migration struct {
	Version     int
	Description string
	SQL         string
}

// registeredMigrations is the ordered list of all schema migrations.
// New migrations MUST be appended with the next sequential version number.
var registeredMigrations = []migration{
	{
		Version:     1,
		Description: "Base tables: runs, agents, audit_events, usage_records, conversation_memory, kv_store, event_log",
		SQL: `
CREATE TABLE IF NOT EXISTS runs (
    tenant_id       TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    run_id          TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT '',
    started_at      TEXT NOT NULL DEFAULT '',
    completed_at    TEXT NOT NULL DEFAULT '',
    events_url      TEXT NOT NULL DEFAULT '',
    run_options     JSONB NOT NULL DEFAULT '{}',
    output          JSONB,
    error           JSONB,
    idempotency_key TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_tenant_run
    ON runs (tenant_id, run_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_tenant_idempotency
    ON runs (tenant_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_runs_tenant_created
    ON runs (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agents (
    agent_id    TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    version     TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT '',
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL DEFAULT ''
);

-- Backward-compatible migration for existing tables without config column.
DO $$ BEGIN
    ALTER TABLE agents ADD COLUMN IF NOT EXISTS config JSONB NOT NULL DEFAULT '{}';
EXCEPTION WHEN others THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_tenant_agent
    ON agents (tenant_id, agent_id);

CREATE TABLE IF NOT EXISTS audit_events (
    id        BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    action    TEXT NOT NULL,
    actor     TEXT NOT NULL DEFAULT '',
    detail    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_tenant_created
    ON audit_events (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS usage_records (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    tokens      INTEGER NOT NULL DEFAULT 0,
    recorded_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_tenant_recorded
    ON usage_records (tenant_id, recorded_at);

CREATE TABLE IF NOT EXISTS conversation_memory (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    tool_call_id TEXT,
    tool_calls   JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_tenant_agent
    ON conversation_memory (tenant_id, agent_id, created_at DESC);

CREATE TABLE IF NOT EXISTS kv_store (
    tenant_id    TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    key          TEXT NOT NULL,
    value        TEXT NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, agent_id, key)
);

CREATE TABLE IF NOT EXISTS event_log (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    agent_id     TEXT NOT NULL DEFAULT '',
    run_id       TEXT NOT NULL,
    event_id     TEXT NOT NULL,
    sequence     INTEGER NOT NULL,
    event_type   TEXT NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_event_log_event_id
    ON event_log (event_id);

CREATE INDEX IF NOT EXISTS idx_event_log_run_seq
    ON event_log (tenant_id, run_id, sequence);
`,
	},
	{
		Version:     2,
		Description: "v1.06 additions: tenants, tenant_members, api_keys, usage_records columns, parent_run_id",
		SQL: `
-- v1.06: tenants table
CREATE TABLE IF NOT EXISTS tenants (
    tenant_id   TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    plan        TEXT NOT NULL DEFAULT 'starter',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

-- v1.06: tenant membership / RBAC
CREATE TABLE IF NOT EXISTS tenant_members (
    tenant_id    TEXT NOT NULL,
    principal_id TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('owner','admin','developer','viewer')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, principal_id)
);

CREATE INDEX IF NOT EXISTS idx_tenant_members_principal
    ON tenant_members (principal_id);

-- v1.06: API keys
CREATE TABLE IF NOT EXISTS api_keys (
    key_id      TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    key_hash    TEXT NOT NULL,
    key_prefix  TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    role        TEXT NOT NULL CHECK (role IN ('owner','admin','developer','viewer')),
    created_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_tenant
    ON api_keys (tenant_id);

CREATE INDEX IF NOT EXISTS idx_api_keys_prefix
    ON api_keys (key_prefix);

-- v1.06: extend usage_records for per-model aggregation
DO $$ BEGIN
    ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;
DO $$ BEGIN
    ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS input_tokens INTEGER NOT NULL DEFAULT 0;
EXCEPTION WHEN others THEN NULL;
END $$;
DO $$ BEGIN
    ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS output_tokens INTEGER NOT NULL DEFAULT 0;
EXCEPTION WHEN others THEN NULL;
END $$;
DO $$ BEGIN
    ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS run_id TEXT NOT NULL DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;

-- v1.06: agent delegation — link child runs to parent runs
DO $$ BEGIN
    ALTER TABLE runs ADD COLUMN IF NOT EXISTS parent_run_id TEXT NOT NULL DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;
`,
	},
	{
		Version:     3,
		Description: "v1.07: agent_versions history table, runs.retry_of column",
		SQL: `
-- v1.07: agent version history for rollback support
CREATE TABLE IF NOT EXISTS agent_versions (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    agent_id    TEXT NOT NULL,
    version     TEXT NOT NULL,
    config      JSONB NOT NULL DEFAULT '{}',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_versions_lookup
    ON agent_versions (tenant_id, agent_id, created_at DESC);

-- v1.07: run retry linkage
DO $$ BEGIN
    ALTER TABLE runs ADD COLUMN IF NOT EXISTS retry_of TEXT NOT NULL DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;
`,
	},
	{
		Version:     4,
		Description: "v1.075: internal job queue table",
		SQL: `
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
`,
	},
	{
		Version:     5,
		Description: "v2.0: agent_clearance and agent_decisions tables for MCP governance",
		SQL: `
-- v2.0: agent clearance tiers for MCP tool authorization
CREATE TABLE IF NOT EXISTS agent_clearance (
    agent_id    TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    tier        INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_clearance_tenant
    ON agent_clearance (tenant_id);

-- v2.0: immutable agent decision audit log
CREATE TABLE IF NOT EXISTS agent_decisions (
    id          TEXT PRIMARY KEY,
    agent_id    TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    decision    TEXT NOT NULL,
    reasoning   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_decisions_agent
    ON agent_decisions (agent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_decisions_tenant
    ON agent_decisions (tenant_id, created_at DESC);
`,
	},
	{
		Version:     6,
		Description: "v2.0 #7: agent_memory table for memory_store/memory_recall MCP tools",
		SQL: `
-- v2.0 #7: per-agent per-tenant key-value memory with TTL support
CREATE TABLE IF NOT EXISTS agent_memory (
    agent_id    TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       TEXT NOT NULL,
    metadata    JSONB DEFAULT '{}',
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,
    PRIMARY KEY (agent_id, tenant_id, key)
);

CREATE INDEX IF NOT EXISTS idx_agent_memory_tenant
    ON agent_memory (tenant_id);

CREATE INDEX IF NOT EXISTS idx_agent_memory_expires
    ON agent_memory (expires_at) WHERE expires_at IS NOT NULL;
`,
	},
	{
		Version:     7,
		Description: "v2.1 #9: knowledge_documents table for full-text search MCP tools",
		SQL: `
-- v2.1 #9: knowledge documents with Postgres full-text search
CREATE TABLE IF NOT EXISTS knowledge_documents (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    title       TEXT NOT NULL,
    content     TEXT NOT NULL,
    source      TEXT DEFAULT '',
    metadata    JSONB DEFAULT '{}',
    tsv         TSVECTOR,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_knowledge_tenant
    ON knowledge_documents (tenant_id);

CREATE INDEX IF NOT EXISTS idx_knowledge_tsv
    ON knowledge_documents USING GIN (tsv);
`,
	},
	{
		Version:     8,
		Description: "v2.3 #16: elevation_requests table for clearance elevation workflow",
		SQL: `
-- v2.3 #16: elevation requests for human-reviewed clearance changes
CREATE TABLE IF NOT EXISTS elevation_requests (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    requested_tier  INTEGER NOT NULL,
    reason          TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied')),
    approved_at     TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_elevation_requests_agent
    ON elevation_requests (agent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_elevation_requests_tenant
    ON elevation_requests (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_elevation_requests_status
    ON elevation_requests (status) WHERE status = 'pending';
`,
	},
	{
		Version:     9,
		Description: "v2.3 #15: managed_agents and agent_messages tables for spawn/message MCP tools",
		SQL: `
-- v2.3 #15: managed (spawned) agents
CREATE TABLE IF NOT EXISTS managed_agents (
    id            TEXT PRIMARY KEY,
    parent_id     TEXT NOT NULL,
    tenant_id     TEXT NOT NULL,
    directive     TEXT NOT NULL,
    allowed_tools JSONB NOT NULL DEFAULT '[]',
    status        TEXT NOT NULL DEFAULT 'created',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_managed_agents_parent
    ON managed_agents (parent_id);

CREATE INDEX IF NOT EXISTS idx_managed_agents_tenant
    ON managed_agents (tenant_id);

CREATE INDEX IF NOT EXISTS idx_managed_agents_tenant_status
    ON managed_agents (tenant_id, status);

-- v2.3 #15: inter-agent messages
CREATE TABLE IF NOT EXISTS agent_messages (
    id         TEXT PRIMARY KEY,
    from_id    TEXT NOT NULL,
    to_id      TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_messages_to
    ON agent_messages (to_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_messages_from
    ON agent_messages (from_id, created_at DESC);
`,
	},
	{
		Version:     10,
		Description: "v2.4 #18: pg_trgm extension and trigram indexes for semantic memory search",
		SQL: `
-- v2.4 #18: enable trigram extension for fuzzy/semantic memory search
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- v2.4 #18: trigram indexes on agent_memory key and value for similarity() queries
CREATE INDEX IF NOT EXISTS idx_agent_memory_key_trgm ON agent_memory USING GIN(key gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_agent_memory_value_trgm ON agent_memory USING GIN(value gin_trgm_ops);
`,
	},
}

// bootstrapDDL creates the schema_migrations table itself.
const bootstrapDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    checksum    TEXT NOT NULL DEFAULT ''
);
`

// migrationChecksum computes the SHA-256 hex digest of a migration's SQL.
func migrationChecksum(sqlText string) string {
	h := sha256.Sum256([]byte(sqlText))
	return fmt.Sprintf("%x", h[:])
}

// Migrate creates tables and indexes if they do not already exist.
// It is safe to call on every startup.
func Migrate(db *sql.DB) error {
	// Dry-run mode: log pending migrations and return.
	if os.Getenv("AGENTOS_MIGRATION_DRY_RUN") == "true" {
		slog.Info("migration dry-run mode enabled, listing all registered migrations")
		for _, m := range registeredMigrations {
			slog.Info("registered migration",
				"version", m.Version,
				"description", m.Description,
				"checksum", migrationChecksum(m.SQL),
			)
		}
		return nil
	}

	// Bootstrap: ensure schema_migrations table exists.
	if _, err := db.Exec(bootstrapDDL); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	// Determine the highest applied version.
	var currentVersion int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations")
	if err := row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("query current migration version: %w", err)
	}

	// Checksum validation for already-applied migrations.
	appliedChecksums := make(map[int]string)
	rows, err := db.Query("SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("query applied checksums: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		var cs string
		if err := rows.Scan(&v, &cs); err != nil {
			return fmt.Errorf("scan applied checksum: %w", err)
		}
		appliedChecksums[v] = cs
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate applied checksums: %w", err)
	}

	for _, m := range registeredMigrations {
		cs := migrationChecksum(m.SQL)

		// Validate checksum for already-applied migrations.
		if m.Version <= currentVersion {
			if stored, ok := appliedChecksums[m.Version]; ok && stored != "" && stored != cs {
				slog.Warn("migration checksum mismatch",
					"version", m.Version,
					"expected", stored,
					"actual", cs,
				)
			}
			continue
		}

		// Apply migration in its own transaction.
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", m.Version, err)
		}

		if _, err := tx.Exec(m.SQL); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("rollback failed after migration error",
					"version", m.Version,
					"rollback_error", rbErr,
				)
			}
			return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Description, err)
		}

		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, description, checksum) VALUES ($1, $2, $3)",
			m.Version, m.Description, cs,
		); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("rollback failed after insert error",
					"version", m.Version,
					"rollback_error", rbErr,
				)
			}
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.Version, err)
		}

		slog.Info("applied migration",
			"version", m.Version,
			"description", m.Description,
			"checksum", cs,
		)
	}

	return nil
}
