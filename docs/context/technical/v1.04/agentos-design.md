# AgentOS v1.04 Design — Production Integrity

This document defines the **architecture decisions** for v1.04 production integrity improvements. It inherits the v1.03 design unchanged and specifies how gaps between documented capabilities and actual implementation are closed.

**Note**: This design document was written retroactively after v1.04 implementation was complete. It documents the architecture decisions that were made during implementation, verified against the actual code.

---

## 1) Health check wiring (Track 1)

### 1.1 Problem

`healthpkg.NewChecker(version)` is created in `server.go` but zero checks are registered. `/v1/health` always returns healthy regardless of actual system state.

### 1.2 Decision: Wire existing DBCheck + add ProviderCheck

The health check framework from v1.025 is correct — only the wiring is missing. Two checks are registered at server construction time:

```go
// agentorchestrator/server.go — in New()

// DB check: registered when using postgres backend (via storage layer)
// This check already exists in health/checker.go as DBCheck() — just unwired.

// Provider check: registered when model base URL is configured
if modelCfg.BaseURL != "" {
    hc.Register(healthpkg.ProviderCheck("model_provider", false, modelCfg.BaseURL))
}
```

### 1.3 ProviderCheck implementation

```go
// internal/health/checker.go — new function

func ProviderCheck(name string, required bool, baseURL string) Check {
    return Check{
        Name:     name,
        Required: required,
        Fn: func(ctx context.Context) CheckResult {
            // HTTP HEAD to baseURL with 5s timeout
            // Success: healthy with latency
            // Failure: unhealthy with error message
        },
    }
}
```

The provider check uses HTTP HEAD (not a real model invocation) to minimize cost while verifying network reachability.

### 1.4 DB check wiring

The DB health check (`health.DBCheck`) is wired by the storage layer. When `AGENTOS_STORAGE_BACKEND=postgres`, the factory returns a store that exposes its `*sql.DB` for health checking. The server calls `hc.Register(healthpkg.DBCheck("database", true, db))` with `required: true` — a failed DB check returns HTTP 503.

---

## 2) Model provider fail-fast (Track 2)

### 2.1 Problem

The `newProvider()` function defaults to `stubProvider` when `AGENTOS_MODEL_PROVIDER` is empty. A misconfigured production deploy silently serves echo responses.

### 2.2 Decision: Empty provider = error

```go
// modelpolicy/providers.go — updated newProvider()

func newProvider(cfg config.ModelConfig) (provider, error) {
    switch cfg.Provider {
    case "openai":
        p = newOpenAIProvider(cfg)
    case "anthropic":
        p = newAnthropicProvider(cfg)
    case "stub":
        return &stubProvider{}, nil
    case "":
        return nil, fmt.Errorf("AGENTOS_MODEL_PROVIDER is required (valid: openai, anthropic, stub)")
    default:
        return nil, fmt.Errorf("unknown model provider %q (valid: openai, anthropic, stub)", cfg.Provider)
    }
    return NewResilientProvider(p, ...), nil
}
```

The validation chain is:
1. `config.LoadModelConfig()` loads `AGENTOS_MODEL_PROVIDER` from env
2. `cfg.Validate()` checks required fields per provider type
3. `newProvider(cfg)` rejects empty/unknown provider values
4. `server.New()` propagates the error and fails startup

### 2.3 Agent loop model resolution

The hardcoded `"local-stub-llm"` in `agent_loop.go` is removed. The agent loop resolves model ID in this order:
1. Agent's configured `Config.ModelID`
2. Global `AGENTOS_MODEL_DEFAULT`
3. Error: `"no model configured for agent and no default model set"`

---

## 3) Postgres agent config persistence (Track 3)

### 3.1 Problem

The `agents` table DDL in `migrations.go` originally lacked a `config` column. Agents saved via Postgres lost their configuration on round-trip.

### 3.2 Decision: JSONB config column with backward-compatible migration

```sql
-- In migrations.go DDL (CREATE TABLE)
CREATE TABLE IF NOT EXISTS agents (
    ...
    config      JSONB NOT NULL DEFAULT '{}',
    ...
);

-- Backward-compatible migration for existing tables
DO $$ BEGIN
    ALTER TABLE agents ADD COLUMN IF NOT EXISTS config JSONB NOT NULL DEFAULT '{}';
EXCEPTION WHEN others THEN NULL;
END $$;
```

### 3.3 Serialization helpers

```go
// internal/storage/postgres/agent_store.go

func marshalConfig(cfg *types.AgentConfig) ([]byte, error) {
    if cfg == nil {
        return []byte("{}"), nil
    }
    return json.Marshal(cfg)
}

func unmarshalConfig(data []byte) *types.AgentConfig {
    if len(data) == 0 || string(data) == "{}" {
        return nil
    }
    var cfg types.AgentConfig
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil
    }
    return &cfg
}
```

All CRUD operations (`Create`, `Get`, `List`, `Save`) include the `config` column. The `Save()` upsert uses `ON CONFLICT DO UPDATE SET config = EXCLUDED.config` to preserve config through updates.

---

## 4) Test coverage architecture (Track 4)

### 4.1 Decision: Test-only track, no production code changes

Track 4 adds test files only. Any bugs discovered during testing are filed as separate issues (or addressed by their respective tracks).

### 4.2 Test organization

```
agentorchestrator/
  ├── openai_compat_test.go    (new — proxy tests)
  ├── agent_loop_test.go       (new — execution loop tests)
  └── server_test.go           (update — more coverage)

internal/storage/
  ├── file_run_store_test.go   (new)
  ├── file_memory_store_test.go (new)
  ├── file_kv_store_test.go    (new)
  └── postgres/*_test.go       (integration tests)

internal/middleware/
  ├── auth_test.go             (new)
  ├── requestid_test.go        (new)
  └── metrics_protect_test.go  (new)
```

### 4.3 Mock strategy

- Model provider: mock HTTP server returning configurable `ChatResponse` payloads
- Storage: in-memory implementations or file backends with `t.TempDir()`
- Tools: mock tool implementations for agent loop tests

---

## 5) Error handling patterns (Track 5)

### 5.1 Decision: Per-site error handling strategy

Each `_ = expr` site receives a specific fix based on context:

| Site | Strategy | Rationale |
|------|----------|-----------|
| `httpx.go` JSON encode | `slog.Warn` | Headers already sent, can't return HTTP error |
| `audit/logger.go` writes | `fmt.Fprintf(os.Stderr, ...)` + counter | Can't use main logger (circular); stderr is always available |
| `id/id.go` RNG read | `panic` | `crypto/rand.Read` failure indicates broken OS |
| `metrics/metrics.go` registrations | `prometheus.MustRegister` | Duplicate = code bug, should crash on startup |
| `federation/forward_index.go` | `slog.Error` + continue | Graceful degradation — empty index is acceptable |

### 5.2 No new error types

Error handling uses existing `slog`, `fmt.Fprintf(os.Stderr, ...)`, and `prometheus.MustRegister`. No new error wrapper types are introduced.

---

## 6) Structured logging migration (Track 6)

### 6.1 Decision: Replace stdlib log with log/slog

All `log.Printf/Fatal/Println` and `fmt.Print*` in production code are replaced with `log/slog` equivalents from Go 1.21+ stdlib.

### 6.2 Structured fields convention

```go
// Agent loop logging pattern
slog.Error("model invocation failed",
    "tenant_id", job.run.TenantID,
    "agent_id",  job.run.AgentID,
    "run_id",    job.run.RunID,
    "step",      step,
    "err",       err,
)
```

Standard fields per context:
- Agent loop: `tenant_id`, `agent_id`, `run_id`, `step`
- Event persistence: `event_id`, `run_id`, `err`
- Startup: `addr`, `version`, `component`

### 6.3 Fatal error pattern

```go
// cmd/agentos/main.go — startup errors
slog.Error("failed to initialize", "component", "storage", "err", err)
os.Exit(1)
```

`log.Fatal` is replaced with `slog.Error` + `os.Exit(1)` to maintain structured output in fatal paths.

---

## 7) Token budget persistence (Track 7)

### 7.1 Architecture

```
TokenBudget (in-memory cache, fast path)
    │
    ├── Record() ──→ in-memory update + async persist to UsageRecorder
    │
    └── getOrCreateWindow() ──→ loads from UsageRecorder on window creation
```

### 7.2 UsageRecorder interface

```go
// internal/quota/token_budget.go

type UsageRecorder interface {
    Record(ctx context.Context, tenantID string, tokens int, timestamp time.Time) error
    HourlyUsage(ctx context.Context, tenantID string, hour time.Time) (int, error)
}
```

### 7.3 Persistence strategy

- `Record()` persists synchronously (not async/batched) for simplicity. Performance is acceptable for single-instance deployments.
- On window creation, `getOrCreateWindow()` loads the current hour's total from the store.
- Persistence failures are logged at ERROR level but do not block the request (in-memory state remains authoritative for the current process).

### 7.4 Storage backend

The existing `usage_records` table (created in v1.025 migrations) stores individual token usage events. `HourlyUsage` aggregates with `SUM(tokens) WHERE recorded_at >= hour AND recorded_at < hour + 1h`.

### 7.5 WithStore pattern

```go
tb := quota.NewTokenBudgetFromEnv()
if usageStore != nil {
    tb.WithStore(usageStore)
}
```

`WithStore()` is called at server construction time. When no store is set, the budget operates in memory-only mode (backward compatible).

---

## 8) Postgres EventLogStore (Track 8)

### 8.1 Package location

```
internal/storage/postgres/
  └── event_log_store.go    (new)
```

### 8.2 Implementation

```go
type EventLogStore struct {
    db *sql.DB
}

func (s *EventLogStore) Append(event types.Event) error {
    const q = `INSERT INTO event_log (tenant_id, agent_id, run_id, event_id, sequence, event_type, payload)
               VALUES ($1, $2, $3, $4, $5, $6, $7)`
    // ...
}

func (s *EventLogStore) QueryByRun(tenantID, runID string, fromSeq int) ([]types.Event, error) {
    const q = `SELECT ... FROM event_log
               WHERE tenant_id = $1 AND run_id = $2 AND sequence > $3
               ORDER BY sequence ASC`
    // ...
}

func (s *EventLogStore) QueryBySequence(fromSeq int, limit int) ([]types.Event, error) {
    const q = `SELECT ... FROM event_log
               WHERE id > $1 ORDER BY id ASC LIMIT $2`
    // ...
}
```

### 8.3 Sequence numbering

The `event_log.id` (BIGSERIAL) provides global ordering. The `sequence` column provides per-run ordering. Both are set by the caller (the event system assigns sequence numbers before calling Append).

### 8.4 Factory integration

```go
// internal/storage/factory.go — updated

func NewEventLogStore(cfg config.StorageConfig, dataDir string) (EventLogStore, error) {
    switch cfg.Backend {
    case "postgres":
        return postgres.NewEventLogStore(cfg.Postgres)
    default:
        return NewFileEventLogStore(dataDir)
    }
}
```

---

## 9) Tool renaming (Track 9)

### 9.1 Decision: Rename with backward compatibility alias

```go
// internal/tools/registry.go

func NewRegistry() *Registry {
    r := &Registry{tools: make(map[string]Tool)}
    truncate := &TextTruncateTool{}
    r.Register(truncate)
    r.RegisterAlias("text_summarize", truncate) // backward compat
    // ...
}
```

The alias logs a WARN when resolved: `"tool 'text_summarize' is deprecated, use 'text_truncate'"`.

### 9.2 File rename

`internal/tools/text_summarize.go` is renamed to `internal/tools/text_truncate.go`. The struct is renamed from `TextSummarizeTool` to `TextTruncateTool`.

---

## 10) JWT library migration (Track 10)

### 10.1 Decision: golang-jwt/jwt/v5

The hand-rolled JWT parsing in `federation/jwt.go` is replaced with `github.com/golang-jwt/jwt/v5`.

### 10.2 Architecture

```go
// federation/jwt.go

type jwtClaimsPayload struct {
    jwt.RegisteredClaims
    TenantID    string `json:"tenant_id"`
    PrincipalID string `json:"principal_id"`
}

func (v *JWTVerifier) Verify(tokenStr string) (*JWTClaims, error) {
    token, err := jwt.ParseWithClaims(tokenStr, &jwtClaimsPayload{}, func(token *jwt.Token) (any, error) {
        return v.publicKey, nil
    })
    // ...
}
```

### 10.3 Security properties

- Algorithm validation: `jwt.ParseWithClaims` validates the `alg` header against the key type
- `alg: none` is rejected by default (golang-jwt does not accept unsigned tokens)
- Expiry validation: `jwt.RegisteredClaims` validates `exp` automatically
- Signature verification: delegated to the library's key-algorithm matching

### 10.4 Key parsing

Public key parsing uses `x509.ParsePKIXPublicKey` (PKIX) and `x509.ParsePKCS1PublicKey` (RSA) from Go stdlib. PEM decoding via `encoding/pem`. The verifier accepts RSA, ECDSA, and Ed25519 keys (whatever `x509.ParsePKIXPublicKey` returns).

---

## 11) Dockerfile hardening (Track 11)

### 11.1 Final Dockerfile structure

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download                    # no || true
COPY . .
RUN CGO_ENABLED=0 go build -o /out/agentos ./cmd/agentos

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl && \
    addgroup -S agentos && adduser -S agentos -G agentos && \
    mkdir -p /data && chown agentos:agentos /data
COPY --from=build /out/agentos /usr/local/bin/agentos
WORKDIR /data
USER agentos
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
    CMD curl -sf http://localhost:9091/health || exit 1
ENTRYPOINT ["agentos"]
```

### 11.2 Key changes from original

| Aspect | Before | After |
|--------|--------|-------|
| User | root | `agentos` (non-root) |
| HEALTHCHECK | none | curl to `/health` every 30s |
| `go mod download` | `\|\| true` (swallowed errors) | strict (fails build) |
| `go.sum` | not committed | committed and copied |
| `.dockerignore` | none | excludes `.git`, `docs/`, `*.md` |

### 11.3 .dockerignore

```
.git
docs/
*.md
.gitlab-ci.yml
deploy/
.vscode/
.claude/
```

---

## 12) Token estimation (Track 12)

### 12.1 Decision: Centralized word-based heuristic

```go
// internal/tokens/estimate.go

func EstimateTokens(text string) int {
    if text == "" {
        return 0
    }
    words := len(strings.Fields(text))
    if words == 0 {
        return 4  // whitespace-only
    }
    est := int(float64(words) * 1.3)
    if est < 4 {
        est = 4
    }
    return est
}
```

### 12.2 Rationale

- `words * 1.3` is closer to real BPE tokenization than `len/4` for English text
- `strings.Fields` handles multiple whitespace types correctly
- Minimum of 4 tokens accounts for message framing overhead
- A full tiktoken-go dependency was considered but rejected for v1.04 (adds ~5MB binary size, complex CGo build). The word heuristic is sufficient for budget enforcement.

### 12.3 Centralization

All callers import `internal/tokens` and call `tokens.EstimateTokens()`. The package also provides `EstimateTokensFromMessages([]Message) int` for chat message slices, adding 4 tokens per message for role/boundary overhead.

---

## 13) Migration safety

### 13.1 Schema migration strategy

All schema changes use `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE ADD COLUMN IF NOT EXISTS`. Migrations are idempotent and safe to run on every startup (existing v1.025 pattern).

### 13.2 No destructive migrations

v1.04 adds columns and tables only. No columns are dropped, renamed, or have their types changed. This ensures zero-downtime upgrades for single-instance deployments.

---

## 14) Dependency changes

### 14.1 Added dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/golang-jwt/jwt/v5` | v5.3.1 | Standard JWT verification (Track 10) |

### 14.2 No removed dependencies

All existing dependencies are retained.
