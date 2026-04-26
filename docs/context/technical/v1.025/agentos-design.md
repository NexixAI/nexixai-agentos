# AgentOS v1.025 Design — Gaps to Production

This document defines the **architecture decisions** for v1.025 production hardening. It inherits the v1.02 design unchanged and specifies how production gaps are closed.

---

## 1) Storage architecture

### 1.1 Backend abstraction

The existing `RunStore` and `AgentStore` interfaces remain the abstraction boundary. A new `postgres` package implements these interfaces using `database/sql` + `lib/pq`.

```
internal/storage/
  ├── interfaces.go          (existing — RunStore, AgentStore interfaces)
  ├── file_run_store.go      (existing — file backend, retained for dev/testing)
  ├── file_run_store_test.go
  ├── agent_store.go         (existing — file backend)
  ├── agent_store_test.go
  ├── postgres/
  │   ├── run_store.go       (new — PostgreSQL RunStore implementation)
  │   ├── agent_store.go     (new — PostgreSQL AgentStore implementation)
  │   ├── audit_store.go     (new — PostgreSQL audit store)
  │   ├── usage_store.go     (new — PostgreSQL usage tracking)
  │   ├── migrations.go      (new — auto-migration on startup)
  │   └── postgres_test.go   (new — integration tests with testcontainers)
  └── factory.go             (new — backend selection based on config)
```

### 1.2 Backend selection

```go
// factory.go
func NewRunStore(cfg config.StorageConfig) (RunStore, error) {
    switch cfg.Backend {
    case "postgres":
        return postgres.NewRunStore(cfg.Postgres)
    case "file", "":
        return NewFileRunStore(cfg.DataDir)
    default:
        return nil, fmt.Errorf("unknown storage backend: %s", cfg.Backend)
    }
}
```

### 1.3 Schema

```sql
CREATE TABLE IF NOT EXISTS runs (
    tenant_id    TEXT NOT NULL,
    run_id       TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    status       TEXT NOT NULL,
    input        JSONB,
    output       JSONB,
    idempotency_key TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, run_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_idempotency
    ON runs (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS agents (
    tenant_id    TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    name         TEXT NOT NULL,
    description  TEXT,
    config       JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, agent_id)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    event_type   TEXT NOT NULL,
    actor        TEXT,
    resource     TEXT,
    detail       JSONB,
    timestamp    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_tenant_time
    ON audit_events (tenant_id, timestamp);

CREATE TABLE IF NOT EXISTS usage_records (
    tenant_id    TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_type  TEXT NOT NULL,  -- 'hourly' or 'daily'
    tokens_used  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, window_start, window_type)
);
```

---

## 2) Concurrency fixes

### 2.1 Quota limiter

The existing `limiter.go` is correct for `TryIncConcurrent`/`DecConcurrent` (mutex-protected). The `Allow()` rate limiter needs verification under `-race`.

**Decision**: Add `go test -race` to CI for all packages. Add a concurrent stress test in `limiter_test.go` with 100 goroutines.

### 2.2 Usage tracker UTC

Replace `time.Now()` with `time.Now().UTC()` in `modelpolicy/usage.go` for all window calculations. This is a one-line fix but affects billing accuracy across time zones.

---

## 3) Graceful shutdown

### 3.1 Shutdown sequence

Each service (`agentorchestrator`, `modelpolicy`, `federation`) follows the same pattern:

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()

// Start server
go srv.ListenAndServe()

<-ctx.Done()

// Drain
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
srv.Shutdown(shutdownCtx)

// Close DB, flush audit
store.Close()
auditSink.Flush()
```

### 3.2 Docker integration

Compose services use `stop_grace_period: 35s` (> drain timeout) to allow graceful shutdown before SIGKILL.

---

## 4) Structured logging

### 4.1 Logger

Use `log/slog` (Go 1.21+ stdlib) with JSON handler when `AGENTOS_LOG_FORMAT=json`.

```go
var logger *slog.Logger

func initLogger(format string) {
    switch format {
    case "json":
        logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
    default:
        logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
    }
}
```

### 4.2 Request context

Middleware injects `request_id` (UUID) and `tenant_id` into context. Logger extracts these for every log line.

---

## 5) Health check depth

### 5.1 Response schema

```json
{
  "status": "healthy|degraded|unhealthy",
  "version": "v1.025",
  "checks": {
    "database": { "status": "healthy", "latency_ms": 2 },
    "audit_sink": { "status": "healthy" }
  }
}
```

### 5.2 Logic

- All checks pass: `healthy`
- Optional check fails: `degraded` (200)
- Required check fails: `unhealthy` (503)
- Database is required when `AGENTOS_STORAGE_BACKEND=postgres`

---

## 6) Docker Compose production profile

### 6.1 File layout

```
deploy/
  ├── local/
  │   └── compose.yaml           (existing — dev profile)
  └── production/
      ├── compose.yaml           (new — production profile)
      ├── .env.example           (new — documented env vars)
      └── postgres-init.sql      (new — optional pre-migration)
```

### 6.2 PostgreSQL service

```yaml
services:
  postgres:
    image: postgres:16-alpine
    volumes:
      - pgdata:/var/lib/postgresql/data
    environment:
      POSTGRES_DB: ${AGENTOS_DB_NAME}
      POSTGRES_USER: ${AGENTOS_DB_USER}
      POSTGRES_PASSWORD_FILE: /run/secrets/db_password
    secrets:
      - db_password
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${AGENTOS_DB_USER}"]
      interval: 5s
      timeout: 5s
      retries: 5
    deploy:
      resources:
        limits:
          memory: 512M

secrets:
  db_password:
    file: ./secrets/db_password.txt
```

---

## 7) Input validation

### 7.1 Validation middleware

A shared validation package at `internal/validate/` provides reusable validators:

```go
func TenantID(id string) error    // tnt_ prefix, 3-64 chars, alnum+underscore
func AgentID(id string) error     // 1-128 chars, alnum+hyphen+underscore
func RunID(id string) error       // valid UUID or prefixed ID
func RequestBody(r *http.Request, maxBytes int64) error  // size limit
```

Applied as middleware before handlers, returning 400 with structured error response.

---

## 8) Testing strategy

### 8.1 Test matrix

| Backend | Tests | CI |
|---------|-------|----|
| `file` | Unit tests (`go test ./...`) | Always |
| `postgres` | Integration tests (testcontainers) | Always |
| Race detection | `go test -race ./...` | Always |

### 8.2 Testcontainers

Integration tests use `testcontainers-go` to spin up a PostgreSQL container per test suite. No external DB dependency for CI.
