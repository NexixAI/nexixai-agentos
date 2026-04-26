# AgentOS Platform — Current-State Architecture

**Version**: v1.077
**Last updated**: 2026-04-04
**Audience**: Human developers and AI agents

This document describes the AgentOS platform as it exists today. It replaces the need to read the versioned design docs (v1.02 through v1.05) chronologically.

---

## 1. System Overview

AgentOS is a **spec-first, multi-tenant, federated agent platform** built by NexixAI. It orchestrates AI agents end-to-end: agent lifecycle management, run execution, model routing, tool invocation, conversation memory, event streaming, multi-tenancy, cross-node federation, and observability.

Key characteristics:
- **Multi-tenant** — every request executes within exactly one `tenant_id`, with full data isolation
- **Federated** — multiple AgentOS nodes can discover each other, forward runs, and replicate events
- **OpenAI-compatible** — exposes `/v1/chat/completions`, `/v1/models`, and `/v1/embeddings` endpoints
- **Dual-backend storage** — file-based for development, PostgreSQL for production, with a factory pattern and migration tooling
- **Single binary** — one Go binary (`agentos`) serves all three services and provides CLI operations

---

## 2. Service Topology

AgentOS runs as three independent HTTP services from a single binary. Each service is selected via `agentos serve <service-name>`.

| Service | Default Port | Health Endpoint | Responsibility |
|---------|-------------|-----------------|----------------|
| **agent-orchestrator** | `:8081` (container: `50081`) | `/v1/health`, `/v1/ready` | Agent CRUD, run lifecycle, execution, events (SSE), memory, KV, admin, RBAC, compliance, usage |
| **model-policy** | `:8082` (container: `50082`) | `/v1/health` | Model registry, model invocation routing, policy checks, token budgets, PII detection |
| **federation** | `:8083` (container: `50083`) | `/v1/federation/health` | Peer discovery, run forwarding, SSE event proxy, event ingestion, JWT-based inter-node auth |

### Middleware Stack (all services)

Each service applies middleware in this order (outermost first):

1. `metrics.Instrument` — Prometheus request metrics
2. `logging.RequestContextMiddleware` — structured slog context
3. `middleware.EnsureRequestID` — generates `X-Request-Id` if absent
4. `middleware.APIKeyMiddleware` — API key resolution (agent-orchestrator only)
5. `middleware.WithAuth` — OIDC token validation, tenant/principal extraction
6. `JWTMiddleware` — federation JWT verification (federation only)

### Agent Orchestrator Routes

| Route | Methods | RBAC | Purpose |
|-------|---------|------|---------|
| `/v1/agents/` | GET, POST, PUT, DELETE | viewer (read), developer (write) | Agent CRUD, versioning, rollback |
| `/v1/agents/{id}/runs` | POST | developer | Create runs for an agent |
| `/v1/agents/{id}/memory` | GET, POST, DELETE | developer | Per-agent conversation memory |
| `/v1/agents/{id}/kv` | GET, PUT, DELETE | developer | Per-agent key-value store |
| `/v1/runs/` | GET, POST (cancel/retry) | viewer (read), developer (write) | Run lifecycle, batch, cancel, retry |
| `/v1/runs/{id}/events` | GET (SSE) | viewer | Server-Sent Events streaming |
| `/v1/chat/completions` | POST | developer | OpenAI-compatible chat endpoint |
| `/v1/models`, `/v1/models/{id}` | GET | — | Model listing |
| `/v1/embeddings` | POST | — | Embedding proxy |
| `/v1/admin/tenants` | GET, POST | admin | Tenant management |
| `/v1/tenants/members` | GET, POST, PUT, DELETE | admin | RBAC member management |
| `/v1/tenants/api-keys` | GET, POST, DELETE | admin | API key management |
| `/v1/usage`, `/v1/usage/export` | GET | viewer | Usage reporting |
| `/v1/admin/audit-log` | GET | admin | Audit log query |
| `/v1/admin/backup-check` | GET | admin | Backup status (postgres only) |
| `/v1/admin/purge` | POST | admin | Data retention purge |
| `/v1/admin/pii-scan` | POST | admin | PII scan on event log |
| `/v1/tenants/data-export` | POST | admin | GDPR data export |
| `/v1/tenants/data-delete` | POST | admin | GDPR data deletion |
| `/v1/auth/refresh` | POST | — | OIDC token refresh proxy |
| `/metrics` | GET | configurable | Prometheus metrics |

### Model Policy Routes

| Route | Methods | Purpose |
|-------|---------|---------|
| `/v1/models` | GET | List registered model providers |
| `/v1/models:invoke` | POST | Route and invoke a model call (QPS-limited per tenant) |
| `/v1/policy:check` | POST | Check model policy before invocation |

### Federation Routes

| Route | Methods | Purpose |
|-------|---------|---------|
| `/v1/federation/peer` | GET | Local peer info |
| `/v1/federation/peer/capabilities` | GET | Peer capabilities and protocol version |
| `/v1/federation/peers` | GET, POST, DELETE | Peer registry management |
| `/v1/federation/runs:forward` | POST | Forward a run to a remote peer |
| `/v1/federation/runs/{id}/events` | GET (SSE) | Proxy SSE events from a forwarded run |
| `/v1/federation/events:ingest` | POST | Receive replicated events from peers |

---

## 3. Storage Layer

### Dual-Backend Architecture

All persistent stores are behind Go interfaces. A factory in `internal/storage/factory.go` selects the backend based on `AGENTOS_STORAGE_BACKEND`:

| Store Interface | File Backend | Postgres Backend |
|----------------|-------------|-----------------|
| `RunStore` | `FileRunStore` (single JSON file) | `postgres.RunStore` |
| `AgentStore` | `FileAgentStore` (directory of JSON files) | `postgres.AgentStore` |
| `MemoryStore` | `FileMemoryStore` (directory) | `postgres.MemoryStore` |
| `KVStore` | `FileKVStore` (directory) | `postgres.KVStore` |
| `EventLogStore` | `FileEventLogStore` (directory) | `postgres.EventLogStore` |
| `UsageStore` | nil (in-memory only) | `postgres.UsageStore` |

The file backend logs a one-time warning on startup recommending postgres for production.

### Migration System

Schema migrations live in `internal/storage/postgres/migrations.go` as an ordered list of versioned SQL blocks. The system:

- Bootstraps a `schema_migrations` tracking table
- Applies migrations sequentially in transactions
- Validates checksums (SHA-256) of previously applied migrations
- Supports dry-run mode (`AGENTOS_MIGRATION_DRY_RUN=true`)
- Is safe to call on every startup (`Migrate(db)` is idempotent)

Current migrations:

| Version | Origin | Tables/Changes |
|---------|--------|---------------|
| 1 | v1.025 | `runs`, `agents`, `audit_events`, `usage_records`, `conversation_memory`, `kv_store`, `event_log` |
| 2 | v1.06 | `tenants`, `tenant_members`, `api_keys`; adds `model`, `input_tokens`, `output_tokens`, `run_id` to `usage_records`; adds `parent_run_id` to `runs` |
| 3 | v1.07 | `agent_versions` (rollback support); adds `retry_of` to `runs` |
| 4 | v1.075 | `jobs` (internal job queue) |

### Data Migration CLI

`agentos migrate-storage --from file --to postgres` migrates all stores (agents, runs, memory, KV, events) from file to postgres. Supports `--dry-run`.

---

## 4. Auth Model

### Authentication Methods

1. **OIDC/OAuth2** — bearer tokens validated against a configured issuer (`AGENTOS_OIDC_ISSUER_URL`). Tenant and principal extracted from JWT claims. Token refresh proxied via `/v1/auth/refresh`.
2. **API Keys** — hashed keys stored in `api_keys` table. Resolved by prefix lookup, scoped to a tenant and RBAC role. Created/revoked via `/v1/tenants/api-keys`.
3. **Dev Headers** — `X-Tenant-Id` and `X-Principal-Id` headers accepted in dev/demo mode. Controlled by `AGENTOS_ALLOW_DEV_HEADERS`.

### Tenant Resolution Order

1. API key metadata (if `Authorization: Bearer ak_...`)
2. OIDC JWT claims (`tenant_id` or `tid`)
3. `X-Tenant-Id` header (dev mode only)

### RBAC

Four roles with hierarchical permissions: `owner` > `admin` > `developer` > `viewer`.

| Role | Capabilities |
|------|-------------|
| **owner** | All admin operations, member management, tenant deletion |
| **admin** | Tenant config, API keys, audit log, purge, compliance |
| **developer** | Agent CRUD, run creation/cancellation, memory/KV, chat |
| **viewer** | Read agents, runs, events, usage |

Membership stored in `tenant_members` table. RBAC middleware checks are per-route (see route table above).

### Fail-Closed Principle

When OIDC is configured and a bearer token is present but invalid, the middleware returns 401 immediately. It does not fall through to permissive mode. This is a hard-fail invariant (see Section 9).

---

## 5. Execution Model

### Executor

The `Executor` struct in `agentorchestrator/executor.go` manages a fixed-size worker pool:

- **Worker count**: `AGENTOS_EXEC_WORKERS` (default 5)
- **Queue**: buffered channel of `runJob` structs
- **Max steps**: `AGENTOS_EXEC_DEFAULT_MAX_STEPS` (default 10) — caps agent loop iterations
- **Timeout**: `AGENTOS_EXEC_DEFAULT_TIMEOUT` (default 300s) — wall-clock limit per run

### Agent Loop

Each run executes this loop until completion, failure, cancellation, or step/timeout limit:

```
1. Load conversation memory for (tenant, agent)
2. Build messages array (system prompt + memory + user input)
3. PII detection on outbound messages (warn or redact per AGENTOS_PII_DEFAULT_MODE)
4. Call model provider via model-policy service
5. Parse response for tool calls
6. If tool calls present:
   a. Execute each tool via tools.Registry
   b. Append tool results to messages
   c. Emit step events
   d. Loop back to step 3
7. If no tool calls: finalize response
8. Record token usage, emit completion event
9. Persist conversation memory
10. Deliver webhook (if configured)
```

### Tool Registry

Built-in tools registered in `internal/tools/`:

| Tool | Description |
|------|-------------|
| `http_fetch` | HTTP GET/POST with configurable headers |
| `json_extract` | JSONPath extraction from strings |
| `text_truncate` | Truncate text to token/character limit |
| `shell_exec` | Execute allowlisted diagnostic commands without shell interpretation |
| `file_read` | Read files within `AGENTOS_TOOL_FILE_BASE_DIR` |
| `file_write` | Write files within `AGENTOS_TOOL_FILE_BASE_DIR` |
| `time` | Current UTC timestamp |
| `sleep` | Pause execution for N seconds |
| `agent_invoke` | Delegate to another agent (creates child run with `parent_run_id`) |

Tool output capped at `AGENTOS_EXEC_TOOL_MAX_OUTPUT` (default 1MB).

Current control-plane notes:
- MCP `tools/call` now enforces each tool's `MinClearance` at the server dispatch boundary. Missing auth context or failed clearance lookup denies the call.
- `chat_completion` is a raw upstream-model pass-through after routing metadata injection. Callers must validate returned content before using it to drive tools or actuators.
- HTTP MCP fetch tools now use bounded client timeouts and revalidate redirect targets against the same SSRF/private-network rules as the initial request.

### Event Streaming

- Runs emit events to in-memory `EventSink` instances (one per active run)
- Clients connect via SSE at `/v1/runs/{run_id}/events`
- Events are durably persisted to `EventLogStore` (file or postgres)
- Completed sinks are kept with TTL to allow late-connecting SSE clients to drain
- Webhook delivery sends events to `AGENTOS_WEBHOOK_URL` with HMAC-SHA256 signatures

### Resilience

- **Circuit breaker** on model provider calls (status at `/v1/admin/circuit-breaker`)
- **Exponential backoff** for provider retries
- **Auto-retry** on provider error (`AGENTOS_AUTO_RETRY_ON_PROVIDER_ERROR`, links via `retry_of`)
- **Token budgets** per tenant (hourly/daily, configurable)
- **Rate limiting** per tenant (QPS for run creation, QPS for model invocation, concurrent run cap)
- **Concurrent run restoration** on restart (scans in-progress runs to rebuild counters)

---

## 6. Federation

### Overview

Federation allows multiple AgentOS deployments to cooperate as a mesh. Each node is identified by `AGENTOS_STACK_ID`.

### Components

| Component | Description |
|-----------|-------------|
| `Registry` | Manages known peers, loaded from `AGENTOS_PEERS_FILE` (JSON seed file) |
| `Forwarder` | Sends run-forward requests to remote peers with retry and backoff |
| `SSEProxy` | Proxies SSE event streams from remote peers to local clients |
| `forwardIndex` | Persistent index tracking which runs were forwarded to which peers |
| `eventStore` | Local store for replicated events received from peers |
| `JWTVerifier` | Validates inter-node JWT tokens (RS256/ES256/EdDSA) |

### Inter-Node Auth

- **JWT verification** on all federation endpoints (unless `AGENTOS_FED_AUTH_DISABLED=1` for dev)
- **mTLS** support for transport security (`AGENTOS_FED_REQUIRE_MTLS=true`)
- Separate cert/key pairs for server and client roles

### Flow: Cross-Node Run

```
Local Node                           Remote Node
    |                                     |
    |-- POST /v1/federation/runs:forward ->|
    |                                     |-- creates run, starts execution
    |<- 202 Accepted (run_id, events_url) |
    |                                     |
    |-- GET /v1/federation/runs/{id}/events (SSE proxy) ->|
    |<- SSE stream ---------------------- |
    |                                     |
    |<- POST /v1/federation/events:ingest |  (replicated events)
```

---

## 7. Package Map

All shared packages live under `internal/`.

| Package | Responsibility |
|---------|---------------|
| `internal/admin` | Admin handlers (backup-check) |
| `internal/audit` | Audit logging (file, stdout, or stderr sink) |
| `internal/auth` | OIDC validation, token refresh, auth context extraction from requests |
| `internal/compliance` | PII scan, GDPR data export/delete, export job store |
| `internal/config` | Configuration loading from environment variables, profile validation |
| `internal/crypto` | AES-256-GCM encryption at rest (with key rotation support) |
| `internal/deploy` | Docker Compose runner, deployment validation, summary reporting |
| `internal/health` | Health and readiness checks, provider liveness probes |
| `internal/httpx` | HTTP response helpers (JSON, Error with correlation ID) |
| `internal/id` | ID generation using prefixed ULIDs (e.g., `run_01HX...`, `agt_01HX...`) |
| `internal/jobs` | Postgres-backed internal job queue (migration 4) |
| `internal/lifecycle` | Data retention purge (configurable per-entity TTL) |
| `internal/logging` | Structured logging middleware, logger initialization |
| `internal/metrics` | Prometheus metrics registration and HTTP handler |
| `internal/middleware` | Auth, RBAC, CORS, API key resolution, request ID, metrics protection |
| `internal/pii` | PII detection (regex patterns + Luhn check for credit cards) |
| `internal/quota` | Rate limiting (QPS + concurrent) and token budget (hourly/daily windows) |
| `internal/secrets` | Secret file loading (`_FILE` suffix convention for Docker secrets) |
| `internal/storage` | Store interfaces, file backends, factory functions, storage migrator |
| `internal/storage/postgres` | PostgreSQL store implementations, schema migrations, connection pooling |
| `internal/telemetry` | OpenTelemetry tracing initialization and configuration |
| `internal/tenants` | In-memory tenant store with default tenant seeding |
| `internal/tlsconfig` | TLS configuration loading from environment |
| `internal/tokens` | Token counting for budget enforcement |
| `internal/tools` | Built-in tool registry and implementations |
| `internal/types` | Shared domain types (Agent, Run, Event, ModelInvokeRequest, etc.) |
| `internal/usage` | Usage reporting HTTP handlers and query interface |
| `internal/validate` | Input validation helpers |
| `internal/webhook` | Webhook delivery with HMAC-SHA256 signing |

---

## 8. Data Model

### Core Entities

```
Tenant (1) ──── (*) Agent (1) ──── (*) Run (1) ──── (*) Event
   |                  |                  |
   |                  |                  └── retry_of (self-ref)
   |                  |                  └── parent_run_id (delegation)
   |                  |
   |                  └──── (*) AgentVersion (rollback history)
   |                  └──── (*) ConversationMemory
   |                  └──── (*) KVEntry
   |
   └──── (*) TenantMember (principal + role)
   └──── (*) APIKey (hashed, with role + expiry)
   └──── (*) AuditEvent
   └──── (*) UsageRecord
```

### Table Summary

| Table | Primary Key | Tenant-Scoped | Purpose |
|-------|------------|---------------|---------|
| `runs` | `(tenant_id, run_id)` | yes | Run state, options, output, error |
| `agents` | `(tenant_id, agent_id)` | yes | Agent definition, config, status |
| `agent_versions` | `id` (serial) | yes | Version history for rollback |
| `event_log` | `id` (serial), unique `event_id` | yes | Durable event storage |
| `conversation_memory` | `id` (serial) | yes | Per-agent message history |
| `kv_store` | `(tenant_id, agent_id, key)` | yes | Per-agent key-value pairs |
| `tenants` | `tenant_id` | — | Tenant metadata, plan tier |
| `tenant_members` | `(tenant_id, principal_id)` | yes | RBAC membership |
| `api_keys` | `key_id` | yes | Hashed API keys with role binding |
| `audit_events` | `id` (serial) | yes | Audit trail |
| `usage_records` | `id` (serial) | yes | Token usage per model per run |
| `jobs` | `id` | — | Internal job queue (webhook retry, etc.) |

### Key Indexes

- `runs`: by `(tenant_id, run_id)` unique, `(tenant_id, idempotency_key)` unique where non-empty, `(tenant_id, created_at DESC)`
- `event_log`: by `event_id` unique, `(tenant_id, run_id, sequence)`
- `agents`: by `(tenant_id, agent_id)` unique
- `agent_versions`: by `(tenant_id, agent_id, created_at DESC)`
- `api_keys`: by `tenant_id`, by `key_prefix`
- `jobs`: by `(status, next_run_at)` for dequeue

---

## 9. Key Invariants

These are hard-fail criteria for Gate 3 validation, learned from production failures in v1.03 through v1.05. Violating any of these is a blocking failure.

### 1. No silent error suppression

**Rule**: No `_ = err` without `//nolint:errcheck // <reason>`.
**Origin**: v1.04 Track 5 FAIL (only 2/7 items fixed), v1.035 OpenAI shim, v1.04 Track 8.
**Exceptions**: `resp.Body.Close()` in defer, SSE stream writes.

### 2. No unbounded in-memory collections

**Rule**: Every `map` or `slice` that grows with requests must have eviction, TTL, or a size cap.
**Origin**: v1.035 shipped 3 memory leaks: `TokenBudget.windows`, `fileEventLogStore.runs`, `Executor.eventSinks`. Escalation trigger at 3 occurrences.

### 3. Auth must fail closed

**Rule**: When auth/authz code encounters an error (bad input, parse failure, missing claims), it must deny access. Never default to permissive.
**Origin**: v1.035 `MetricsRequireAuth` defaulted false on bad input; v1.05 OIDC middleware fell through on validation failure.

### 4. New packages must have test files

**Rule**: Any new package must ship with a `*_test.go` file. No exceptions.
**Origin**: v1.035 shipped metrics, event log, and SSE with zero tests. v1.04 had 6 packages with zero test files.

### 5. Postgres code must have integration tests

**Rule**: Code that talks to PostgreSQL must have integration tests (`//go:build integration` tag or testcontainers).
**Origin**: v1.03 Track 6 postgres sentinel mismatch that code review could not catch.

### 6. Specs must match implementation scope

**Rule**: Do not write specs describing features beyond what is implemented. If implementation is partial, the spec must say so.
**Origin**: v1.05 Track 6 spec said "Postgres-backed" but implementation was in-memory; Track 8 spec said "streaming" but implemented truncation; Track 13 described a migration CLI that did not exist.
