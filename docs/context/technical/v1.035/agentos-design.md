# AgentOS v1.035 Design — Operational Maturity

This document defines the **architecture decisions** for v1.035 operational maturity capabilities. It inherits the v1.03 design unchanged and specifies the new observability, resilience, persistence, and specification layers.

**Note**: v1.035 was implemented before this design doc was written. This document was drafted retroactively from the completed code to restore the authority chain.

---

## 1) Enhanced observability metrics

### 1.1 Metrics package extension

All new metrics are added to the existing `internal/metrics/metrics.go` package, which owns the shared Prometheus registry.

```go
// internal/metrics/metrics.go (extended)

// Model call metrics
modelCallDuration   *prometheus.HistogramVec   // labels: model, provider, status
modelTokensUsed     *prometheus.CounterVec      // labels: tenant, model, direction

// Tool metrics
toolExecDuration    *prometheus.HistogramVec   // labels: tool, status

// Run lifecycle metrics
runLifecycle        *prometheus.CounterVec      // labels: tenant, transition
runsActive          *prometheus.GaugeVec        // labels: tenant
runSteps            *prometheus.HistogramVec    // labels: tenant

// Storage metrics
memoryOps           *prometheus.CounterVec      // labels: operation, status
kvOps               *prometheus.CounterVec      // labels: operation, status
```

### 1.2 Instrumentation points

Metrics are recorded via helper functions called from the relevant code paths:

| Helper function | Called from | Purpose |
|----------------|------------|---------|
| `ObserveModelCall(model, provider, status, duration)` | `modelpolicy` provider wrappers | Model call latency |
| `AddModelTokens(tenant, model, prompt, completion)` | `agentorchestrator/agent_loop.go` | Token accounting |
| `ObserveToolExec(tool, status, duration)` | `internal/tools/registry.go` | Tool duration |
| `IncRunLifecycle(tenant, transition)` | `agentorchestrator/executor.go` | State transitions |
| `IncRunsActive(tenant)` / `DecRunsActive(tenant)` | `agentorchestrator/executor.go` | Active run gauge |
| `ObserveRunSteps(tenant, steps)` | `agentorchestrator/executor.go` | Steps per run |
| `IncMemoryOp(operation, status)` | Memory store implementations | Memory ops |
| `IncKVOp(operation, status)` | KV store implementations | KV ops |

### 1.3 Design rationale

- **Counter vs histogram**: Counters for events that only need rate (ops, tokens). Histograms for latency where percentiles matter (model calls, tool execution, run steps).
- **Label cardinality**: Labels are bounded. `tenant` could grow but is bounded by deployment. `model` and `tool` are bounded by configuration. No unbounded labels (no run_id, no request_id).
- **Single registry**: All metrics share one Prometheus registry to avoid double-registration panics and ensure a single `/metrics` handler.

---

## 2) Model provider retry with backoff

### 2.1 Architecture

Retry is implemented as a provider wrapper (`ResilientProvider`) in `modelpolicy/resilience.go`. It composes with the circuit breaker (same file) and wraps the inner provider.

```
Caller → ResilientProvider.ChatComplete()
           ├── attempt 0: check circuit → call inner.ChatComplete()
           ├── attempt 1: backoff + jitter → check circuit → call inner
           └── attempt N: backoff + jitter → check circuit → call inner
```

### 2.2 Provider wrapping

```go
// modelpolicy/resilience.go

type ResilientProvider struct {
    inner   provider
    retry   RetryConfig
    breaker *circuitBreaker
}
```

The `ResilientProvider` implements the `provider` interface so it can be used as a drop-in replacement. The factory in `modelpolicy/providers.go` wraps the real provider with `NewResilientProvider()` when retry is configured.

### 2.3 Backoff algorithm

```go
func backoffDuration(attempt int, cfg RetryConfig) time.Duration {
    base := float64(cfg.BaseBackoff) * math.Pow(cfg.BackoffScale, float64(attempt))
    if base > float64(cfg.MaxBackoff) {
        base = float64(cfg.MaxBackoff)
    }
    // +/-25% jitter
    jitter := base * 0.25 * (2*rand.Float64() - 1)
    return time.Duration(base + jitter)
}
```

With defaults (base=500ms, scale=2.0, max=30s):
- Attempt 0: immediate
- Attempt 1: ~500ms (375-625ms with jitter)
- Attempt 2: ~1s (750ms-1.25s with jitter)

### 2.4 Retry-After handling

When the provider returns HTTP 429, the `ProviderError` carries a `RetryAfter` field. If `retryAfterDuration(err)` exceeds the computed backoff, the longer value is used. This prevents violating the provider's rate limit signal.

### 2.5 Trade-offs

- **Retry on connection errors**: All connection/timeout errors are treated as retryable. This is aggressive but appropriate for model providers where transient network issues are common.
- **No retry on Invoke**: The legacy `Invoke` path is not wrapped with retry because it is the backward-compatible stub path, and callers may have their own retry logic.
- **Jitter range**: +/-25% was chosen as a balance between spread and predictability. Wider jitter (e.g., full random) provides better spread but makes debugging harder.

---

## 3) Circuit breaker

### 3.1 State machine implementation

```go
// modelpolicy/resilience.go

type circuitBreaker struct {
    mu             sync.Mutex
    state          CircuitState    // Closed, Open, HalfOpen
    failures       int
    lastFailure    time.Time
    halfOpenActive int
    cfg            CircuitBreakerConfig
}
```

### 3.2 State transitions

```
                       ┌─────────────────────────────────┐
                       │                                 │
                       ▼                                 │
                   ┌────────┐  failures >= threshold  ┌──────┐
            ──────►│ Closed │ ───────────────────────►│ Open │
                   └────────┘                         └──────┘
                       ▲                                 │
                       │ success                         │ recovery timeout
                       │                                 │
                   ┌──────────┐◄─────────────────────────┘
                   │ HalfOpen │
                   └──────────┘
                       │ failure
                       └─────────────────────────────────►Open
```

### 3.3 allow() logic

1. **Closed**: always allow.
2. **Open**: check if `time.Since(lastFailure) >= recoveryTimeout`. If yes, transition to HalfOpen and allow one probe. If no, reject.
3. **HalfOpen**: allow up to `halfOpenMax` concurrent probes. Additional requests are rejected.

### 3.4 Integration with retry

The circuit breaker is checked inside the retry loop. On each attempt:
1. Check `breaker.allow()`
2. If denied, set `lastErr = ErrCircuitOpen` and continue to next attempt (the backoff may allow the circuit to transition)
3. If allowed, call the inner provider
4. On success: `breaker.recordSuccess()` (resets to closed)
5. On retryable failure: `breaker.recordFailure()` (increments failure count)

### 3.5 Design rationale

- **Per-provider, not per-tenant**: The circuit breaker protects the downstream provider. A single failing provider affects all tenants equally, so the breaker is scoped to the provider, not the tenant.
- **Mutex-based**: A simple mutex was chosen over atomic operations for clarity. The critical section is minimal (~5 field reads/writes) so contention is negligible.
- **No sliding window**: The failure counter does not use a time window — it resets only on success. This means a slow trickle of failures over a long period can eventually open the circuit. This was deemed acceptable because a consistently failing provider should be flagged regardless of time span.

---

## 4) Token budget enforcement

### 4.1 Architecture

```
internal/quota/
  ├── token_budget.go       (TokenBudget struct, Check/Record/Remaining)
  ├── token_budget_test.go
  └── limiter.go            (existing QPS/concurrency limiter)
```

### 4.2 Window management

```go
type TokenBudget struct {
    mu      sync.Mutex
    windows map[string]*tokenWindow  // key: tenant_id
    limit   int                      // max tokens/hour (0 = unlimited)
    store   UsageRecorder            // optional persistence
}

type tokenWindow struct {
    tokens    int
    windowEnd time.Time
}
```

Windows are created lazily on first access per tenant. When `time.Now()` exceeds `windowEnd`, the window is reset. If a `UsageRecorder` is attached, the new window loads prior usage from the store for the current hour.

### 4.3 Persistence integration

The `UsageRecorder` interface decouples the budget from storage:

```go
type UsageRecorder interface {
    Record(ctx context.Context, tenantID string, tokens int, timestamp time.Time) error
    HourlyUsage(ctx context.Context, tenantID string, hour time.Time) (int, error)
}
```

This is implemented by `postgres.UsageStore`. When persistence fails, the budget continues to operate on in-memory data (fail-open with slog.Error).

### 4.4 HTTP enforcement flow

```
POST /v1/agents/{agent_id}/runs
  ├── Check QPS quota      → 429 if exceeded
  ├── Check concurrency    → 429 if exceeded
  ├── Check token budget   → 429 "token_budget_exceeded" if exceeded
  └── Accept run
```

The token budget check occurs after QPS and concurrency checks. On failure, the concurrency slot is released before returning 429.

### 4.5 Trade-offs

- **In-memory with optional persistence**: Fast path is always in-memory. Persistence is best-effort. This means a restart without persistence resets budgets, which is a known limitation documented in the PRS.
- **1-hour fixed windows**: Simpler than sliding windows. A tenant could burst at the end of one window and beginning of the next, consuming 2x the limit across a 2-minute span. This was deemed acceptable for v1.035.

---

## 5) Durable event log

### 5.1 Interface

```go
// internal/storage/event_log_store.go

type EventLogStore interface {
    Append(ctx context.Context, event types.EventEnvelope) error
    QueryFromSequence(ctx context.Context, tenantID, runID string, afterSequence int) ([]types.EventEnvelope, error)
    Close() error
}
```

### 5.2 File-based backend

```go
// internal/storage/event_log_store.go

type fileEventLogStore struct {
    mu      sync.Mutex
    dataDir string
    runs    map[string][]types.EventEnvelope  // key: tenantID/runID
}
```

Events are held in memory and persisted to `{dataDir}/event_log/{tenantID}/{runID}.json` on each Append. This provides durability for single-instance deployments.

### 5.3 PostgreSQL backend

```go
// internal/storage/postgres/event_log_store.go

type EventLogStore struct {
    db *sql.DB
}
```

Uses parameterized queries against the `event_log` table. The schema (in `migrations.go`):

```sql
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_event_log_event_id ON event_log (event_id);
CREATE INDEX IF NOT EXISTS idx_event_log_run_seq ON event_log (tenant_id, run_id, sequence);
```

### 5.4 EventSink integration

The `EventSink` (in `agentorchestrator/events.go`) can be created with `NewEventSinkWithLog()` to write events to both the in-memory sink and the durable log. The SSE handler reads from the sink first, falling back to the durable log for completed runs.

### 5.5 Design rationale

- **Dual write (memory + durable)**: In-memory for low-latency SSE streaming to active clients. Durable for reconnection after disconnect or restart.
- **File backend as default**: The file backend was chosen as the default because it requires no external dependencies. PostgreSQL is available when configured.
- **Sequence-based cursors**: Integer sequences are simple, sortable, and work naturally with `Last-Event-ID`. UUIDs would require timestamp-based ordering and are more complex.

---

## 6) SSE reconnect with cursor

### 6.1 Architecture

The SSE reconnection is implemented directly in `agentorchestrator/server.go` within the `handleEvents` method.

### 6.2 Reconnection flow

```
Client connects:
  GET /v1/runs/{run_id}/events
  [optional] Last-Event-ID: 5

Server:
  1. Parse Last-Event-ID → afterSequence = 5
  2. Check in-memory EventSink → sink.EventsFromSequence(5)
  3. If no events, check EventLogStore → eventLog.QueryFromSequence(tenantID, runID, 5)
  4. If still no events and afterSequence == 0 → emit synthetic fallback
  5. Write SSE frames with id: field

Client receives:
  id: 6
  event: agentos.event
  data: {"event": {...}}

  id: 7
  event: agentos.event
  data: {"event": {...}}
```

### 6.3 SSE frame format

```
id: {sequence}\n
event: agentos.event\n
data: {json EventEnvelope}\n
\n
```

The `id:` field uses the event's integer sequence number. Browsers and SSE clients will automatically send this as `Last-Event-ID` on reconnection.

### 6.4 Design rationale

- **Buffered writer**: A `bufio.Writer` is used for SSE output to batch small writes. The writer is flushed after all events, followed by `http.Flusher.Flush()` to push to the network.
- **Fallback chain**: Memory → durable log → synthetic. This ensures the client always gets a response, even if the run completed and the in-memory sink was garbage collected.
- **No long-polling**: The current implementation writes all available events and ends. True long-lived SSE with blocking reads is possible but not implemented in v1.035.

---

## 7) OpenAPI specification

### 7.1 File location

`docs/data/api-contracts/openapi.yaml` — the consolidated specification covering all Agent Orchestrator endpoints.

### 7.2 Design decisions

- **OpenAPI 3.0.3**: Chosen for broad tooling support (Swagger UI, code generators, validation libraries).
- **Consolidated file**: A single YAML file rather than per-service files. This simplifies consumption and ensures cross-references work.
- **Schema reuse**: Common schemas (`Agent`, `Run`, `ErrorResponse`) are defined in `components/schemas` and referenced via `$ref`. This eliminates duplication and ensures consistency.
- **Security schemes**: Two schemes documented — `BearerAuth` and `TenantHeader`. Both are applied globally with per-endpoint overrides (e.g., `/health` has no auth).

### 7.3 Schema accuracy

All schemas were derived from the Go type definitions in `internal/types/`. The following type mappings were used:

| Go type | OpenAPI type |
|---------|-------------|
| `string` | `type: string` |
| `int` | `type: integer` |
| `bool` | `type: boolean` |
| `map[string]any` | `type: object` |
| `*T` (pointer) | optional field (no `required`) |
| `time.Time` / RFC3339 string | `type: string, format: date-time` |

---

## 8) Component interaction summary

```
                    ┌──────────────────────────────────┐
                    │       Agent Orchestrator          │
                    │   agentorchestrator/server.go     │
                    │                                   │
                    │  ┌─────────┐   ┌──────────────┐  │
    HTTP ──────────►│  │ Router  │──►│ handleEvents │  │
                    │  └─────┬───┘   │ (SSE+cursor) │  │
                    │        │       └──────┬───────┘  │
                    │        ▼              │          │
                    │  ┌──────────┐   ┌─────▼──────┐  │
                    │  │ Executor │   │ EventSink  │  │
                    │  │ (runs)   │──►│ + EventLog │  │
                    │  └────┬─────┘   └────────────┘  │
                    │       │                          │
                    │  ┌────▼────────────┐             │
                    │  │ TokenBudget     │             │
                    │  │ (quota check)   │             │
                    │  └─────────────────┘             │
                    └───────────┬──────────────────────┘
                                │
                    ┌───────────▼──────────────────────┐
                    │     Model Policy Service          │
                    │                                   │
                    │  ┌────────────────────────────┐  │
                    │  │ ResilientProvider           │  │
                    │  │  ├── RetryConfig            │  │
                    │  │  └── CircuitBreaker         │  │
                    │  │       └── inner provider    │  │
                    │  └────────────────────────────┘  │
                    └──────────────────────────────────┘
                                │
                    ┌───────────▼──────────────────────┐
                    │     Metrics (Prometheus)          │
                    │  internal/metrics/metrics.go      │
                    │                                   │
                    │  model_call_duration_seconds      │
                    │  model_tokens_total               │
                    │  tool_execution_duration_seconds   │
                    │  run_lifecycle_total               │
                    │  runs_active                       │
                    │  run_steps_total                   │
                    │  memory_operations_total           │
                    │  kv_operations_total               │
                    └──────────────────────────────────┘
```
