# AgentOS v1.05 Design — Commercial Grade

This document defines the **architecture decisions** for v1.05 commercial-grade capabilities. It inherits the v1.02, v1.025, v1.03, and v1.04 designs unchanged and adds production-grade tooling, APIs, operational features, and standards-based authentication.

---

## 1) Run list/history endpoint

### 1.1 Query architecture

The `ListByAgent` method on `RunStore` accepts `(tenantID, agentID, limit, afterRunID)` and returns runs scoped to the tenant-agent pair.

```go
// internal/storage/run_store.go
ListByAgent(ctx context.Context, tenantID, agentID string, limit int, afterRunID string) ([]types.Run, error)
```

The handler queries `limit+1` rows to determine whether additional pages exist. If `len(results) > limit`, `has_more` is true and the extra row is trimmed before returning.

### 1.2 Status filtering

Status filtering is performed in-memory after the query, not as a database predicate. This is acceptable because:
1. The `limit+1` fetch pattern already caps the working set
2. Adding a `WHERE status = ?` clause would complicate the cursor-based pagination (the cursor is a run_id, not a composite key)
3. For the expected volume per agent (hundreds, not millions), in-memory filtering is negligible

If this becomes a bottleneck, a future version can add a composite index on `(tenant_id, agent_id, status, created_at)`.

### 1.3 Pagination cursor

The `after` cursor is a run ID. The store implementation queries rows with `created_at < (SELECT created_at FROM runs WHERE run_id = ?)`. This is stable across insertions and does not require offset-based pagination.

---

## 2) Postgres EventLogStore wiring

### 2.1 Factory pattern

The storage factory was extended to include `NewEventLogStore(cfg, dataDir)`:

```go
// internal/storage/factory.go
func NewEventLogStore(cfg config.StorageConfig, dataDir string) (EventLogStore, error) {
    switch strings.ToLower(cfg.Backend) {
    case "postgres":
        return postgres.NewEventLogStore(cfg.Postgres)
    case "file", "":
        return NewFileEventLogStore(dataDir)
    }
}
```

The server constructor calls `storage.NewEventLogStore(storageCfg, "data")` instead of hardcoding `NewFileEventLogStore("data")`. The `dataDir` parameter is retained for the file backend fallback path.

### 2.2 Event replay on reconnect

SSE event streaming checks in-memory event sinks first (for active runs), then falls through to the durable event log (for completed runs). This two-tier lookup ensures:
- Active runs get immediate SSE delivery from memory
- Reconnecting clients (via `Last-Event-ID` header) can replay from the durable store even after the in-memory sink has been garbage collected

---

## 3) New built-in tools

### 3.1 Package layout

```
internal/tools/
  ├── registry.go        (existing — updated to register new tools)
  ├── types.go           (existing — Tool interface)
  ├── http_fetch.go      (existing)
  ├── json_extract.go    (existing)
  ├── text_summarize.go  (existing)
  ├── shell_exec.go      (new — shell command execution)
  ├── file_read.go       (new — sandbox file reading)
  ├── file_write.go      (new — sandbox file writing)
  ├── regex_match.go     (new — regex pattern matching)
  └── math_eval.go       (new — safe arithmetic evaluation)
```

### 3.2 Shell execution security model

`shell_exec` uses a deny-list rather than an allow-list because:
1. An allow-list would be too restrictive for general agent use cases — agents need to run diverse commands (`ls`, `cat`, `grep`, `curl`, `python`, etc.)
2. The deny-list blocks a small, well-defined set of destructive commands (`rm`, `chmod`, `kill`, `mkfs`, etc.)
3. The tool resolves paths (e.g., `/usr/bin/rm` -> `rm`) before checking the deny-list, preventing path-based bypass
4. Additional protection layers exist: subprocess timeout (max 120s), output size limits (64KB), and the overall run-level timeout

The deny-list is a compile-time constant (`defaultDeniedCommands`) to prevent runtime tampering.

### 3.3 Sandbox file access (safePath)

File tools share a common `safePath()` function that enforces directory confinement:

```go
func safePath(requestedPath string) (string, error) {
    base := allowedBaseDir()
    cleaned := filepath.Clean(requestedPath)
    if filepath.IsAbs(cleaned) {
        if !strings.HasPrefix(cleaned, base) {
            return "", error // outside sandbox
        }
        return cleaned, nil
    }
    abs := filepath.Join(base, cleaned)
    if !strings.HasPrefix(abs, base) {
        return "", error // path traversal
    }
    return abs, nil
}
```

Key decisions:
- `filepath.Clean` normalizes `../` sequences before path joining
- The resolved path is re-checked with `HasPrefix` to catch traversal that survives cleaning
- The base directory is created on first access (`os.MkdirAll`)
- Base directory is configurable via `AGENTOS_TOOL_FILE_BASE_DIR` (default `/tmp/agentos-sandbox`)

### 3.4 Math evaluator design

`math_eval` uses a hand-written recursive descent parser rather than a library because:
1. Security: no `eval`, no reflection, no code execution paths
2. Simplicity: the grammar is small enough that a hand-written parser is clearer than a grammar DSL
3. Control: we can precisely define which functions and operators are allowed

The parser follows standard precedence: unary > multiplicative > additive. Functions are parsed as identifiers followed by parenthesized argument lists. Constants (`pi`, `e`) are identifiers without parentheses.

---

## 4) Parallel tool execution

### 4.1 Why pre-allocated slice (no mutex)

The agent loop uses a pre-allocated results slice indexed by position, not a shared slice protected by a mutex:

```go
results := make([]toolResult, len(choice.Message.ToolCalls))
var wg sync.WaitGroup

for i, tc := range choice.Message.ToolCalls {
    results[i].tc = tc
    wg.Add(1)
    go func(idx int, tc modelpolicy.ToolCall) {
        defer wg.Done()
        results[idx].result = runToolRegistry.Execute(...)
    }(i, tc)
}
wg.Wait()
```

This design avoids mutex contention because:
1. Each goroutine writes to a distinct index (`results[idx]`), so there are no data races
2. The slice is pre-allocated before any goroutine starts, so there is no slice growth during concurrent access
3. `wg.Wait()` provides the happens-before guarantee — all writes are visible after Wait returns
4. Results are naturally in the correct order (matching the original tool call sequence) without sorting

Alternative considered and rejected: `sync.Mutex`-protected append to a shared slice. This would require post-hoc sorting by index and adds unnecessary contention for a simple fan-out pattern.

### 4.2 Event emission ordering

Tool call events (`step.tool_call`) are emitted sequentially before launching goroutines. Tool result events (`step.tool_result`) are emitted sequentially after `wg.Wait()`, iterating the results slice in order. This ensures event consumers see a consistent, ordered stream even though execution was concurrent.

### 4.3 Per-tool error isolation

If a tool fails, its error is captured as a string result (e.g., `"error: tool_error ..."`) and returned to the model as a tool observation. The agent loop does NOT abort on individual tool failures — the model can retry or handle the error. The only exception is context cancellation (run timeout or cancellation), which terminates all tool goroutines via context propagation.

---

## 5) Graceful shutdown (ServerPair)

### 5.1 ServerPair architecture

```go
// agentorchestrator/listen.go

type ServerPair struct {
    HTTP     *http.Server
    internal *Server
}

func (sp *ServerPair) Shutdown(ctx context.Context) error {
    httpErr := sp.HTTP.Shutdown(ctx)    // 1. Stop accepting connections
    if sp.internal.executor != nil {
        sp.internal.executor.Shutdown(ctx) // 2. Drain executor jobs
    }
    return httpErr
}
```

The two-phase shutdown ensures:
1. No new requests arrive while draining
2. In-flight model calls can complete within the context deadline
3. The caller controls the drain timeout via the context

### 5.2 Why two-phase (not concurrent)

HTTP shutdown and executor shutdown are sequential, not concurrent, because:
1. New HTTP requests could create new runs during executor drain, making the drain non-terminating
2. Sequential shutdown is simpler to reason about for operators
3. The HTTP shutdown is typically fast (sub-second) because it just closes the listener and waits for in-flight HTTP handlers

### 5.3 Executor shutdown internals

```go
func (e *Executor) Shutdown(ctx context.Context) {
    close(e.queue)                    // Prevent new submissions
    done := make(chan struct{})
    go func() { e.wg.Wait(); close(done) }()
    select {
    case <-done:                      // All workers finished
    case <-ctx.Done():                // Deadline exceeded
    }
}
```

Workers exit their loop when the queue channel is closed and drained. The `sync.WaitGroup` tracks active workers, not active jobs — each worker decrements when it exits its processing loop.

---

## 6) Rate limiter persistence

### 6.1 Restoration strategy

On startup, the server iterates all known tenants and queries their in-progress runs:

```go
for _, t := range tenantStore.List() {
    runs, _ := runStore.List(ctx, t.TenantID)
    for _, r := range runs {
        if r.Status == "running" || r.Status == "queued" {
            concurrentCounts[r.TenantID]++
        }
    }
}
srv.limiter.WithInitialConcurrent(concurrentCounts)
```

This is a scan-at-startup approach, not continuous persistence. The trade-off:
- **Pro**: No additional writes during normal operation (no performance impact)
- **Pro**: Works with any RunStore backend (file or postgres)
- **Con**: If the process crashes mid-run, the next restart may over-count (runs stuck in "running" state). This is acceptable because the runs will eventually time out and be marked as failed.

### 6.2 QPS rate limiter

The QPS rate limiter uses in-memory token buckets that are NOT persisted. QPS limits reset on restart. This is acceptable because QPS limits are per-second and transient by nature — there is no meaningful state to restore.

---

## 7) OpenAI compatibility

### 7.1 Model by ID endpoint

`GET /v1/models/{model_id}` returns model metadata in OpenAI format. Currently, only the default model (`AGENTOS_MODEL_DEFAULT`) is known. Unknown models return 404.

This endpoint is required for client libraries (openai-python, langchain) that query model metadata before sending requests.

### 7.2 Embeddings proxy architecture

`POST /v1/embeddings` is a transparent HTTP proxy to the upstream provider:

```
Client -> AgentOS /v1/embeddings -> {baseURL}/v1/embeddings -> Provider
```

Design decisions:
- **Transparent proxy**: request body is forwarded as-is, response is forwarded as-is (including status code)
- **No caching**: embeddings are not cached (results depend on input, model state, etc.)
- **Auth forwarding**: the configured `AGENTOS_MODEL_API_KEY` is set as `Authorization: Bearer` on the upstream request
- **Body limit**: 1MB to prevent abuse
- **Timeout**: uses the configured model timeout (`AGENTOS_MODEL_TIMEOUT`)

### 7.3 Streaming compatibility

The chat completions endpoint supports both streaming and non-streaming modes:
- If the provider implements `ModelStreamProvider`, true SSE streaming is used
- If the provider only implements `ModelProvider` (non-streaming), the response is wrapped in a single SSE chunk followed by `[DONE]`
- This ensures compatibility with clients that always request `stream: true`

---

## 8) Tool output truncation

### 8.1 Two-level truncation

Tool output is truncated at two levels:

1. **Per-tool level**: Each tool implementation may enforce its own limit (e.g., `shell_exec` at 64KB, `file_read` at 1MB). This is the tool's internal concern.
2. **Executor level**: `Executor.truncateToolOutput()` applies a global limit (`AGENTOS_TOOL_MAX_OUTPUT`, default 32KB). This catches tools that produce large output regardless of their internal limits.

The executor-level truncation runs inside the tool goroutine (after `Execute` returns, before storing the result). This ensures the truncated string is what gets appended to the message history and sent to the model.

### 8.2 Truncation marker

Truncated output ends with:
```
\n... [output truncated at 32768 bytes]
```

The marker includes the byte count to help the model understand the truncation context.

---

## 9) Environment variable documentation

### 9.1 .env.example structure

The `.env.example` file is organized by category:
1. Server configuration (port, log level, shutdown timeout)
2. Storage backend (postgres DSN, file paths)
3. Model provider (base URL, API key, timeout)
4. Quota and rate limiting (QPS, concurrency, token budget)
5. Tool configuration (file base dir, max output)
6. Authentication (OIDC issuer, audience, tenant claim)
7. Federation (peers file, stack ID, region)
8. PII detection (mode, enabled)

Secret values use descriptive placeholders, not real values.

---

## 10) Batch run API

### 10.1 Quota enforcement strategy

Quotas are checked per-item in the batch loop, not pre-checked for the entire batch. This means:
- A batch of 10 runs where the 6th exceeds QPS will create 5 runs and return 429
- The 429 response is returned immediately — the 5 already-created runs are NOT rolled back
- This is a pragmatic choice: rolling back already-submitted executor jobs is complex and the runs are already persisted

### 10.2 Agent resolution

The agent is resolved once before the batch loop and reused for all runs. This avoids N database lookups for N runs in the batch. If the agent is not found, a minimal agent stub (`{AgentID, TenantID}`) is used (matching the behavior of single run creation).

### 10.3 Audit

A single audit entry is emitted for the entire batch with `action: "runs.batch_create"` and a `count` field. Individual run audit entries are NOT created for batch items (the batch entry serves as the audit trail).

---

## 11) Dynamic federation peer discovery

### 11.1 Registry architecture

```go
// federation/peer_registry.go

type Registry struct {
    mu     sync.RWMutex
    Local  PeerInfo
    peers  map[string]PeerInfo    // stack_id -> peer
    health map[string]*PeerHealth // stack_id -> health state
    stopCh chan struct{}
}
```

The registry uses a `sync.RWMutex` for thread-safe access:
- Read lock for `Get`, `List`, `HealthyPeers`, `GetHealth`
- Write lock for `AddPeer`, `RemovePeer`, `updateHealth`

### 11.2 Health check architecture

Health checks run in a dedicated goroutine started by `StartHealthChecks(interval)`:

```
StartHealthChecks(30s)
  └── goroutine: ticker loop
        ├── checkAllPeers()           // immediate initial check
        └── for range ticker.C:
              └── checkAllPeers()
                    └── for each peer:
                          └── checkPeer(peer)
                                ├── GET {base_url}/health
                                ├── measure latency
                                └── updateHealth(stackID, healthy, err, latency)
```

Design decisions:
- **Sequential peer checks**: Peers are checked sequentially, not concurrently. For the expected peer count (<10), this is simpler and avoids goroutine explosion. If federation grows to many peers, this can be parallelized.
- **5-second timeout per peer**: prevents slow peers from blocking the health check cycle
- **HTTP DefaultClient**: used for health checks (no custom transport needed for simple GET /health)
- **DEBUG-level logging for failures**: health check failures are expected in degraded environments and should not pollute production logs

### 11.3 Graceful degradation

`HealthyPeers()` returns only peers with `Healthy == true`. Callers (federation event forwarding, cross-cluster routing) use this to skip unreachable peers. An unhealthy peer is not removed — it remains in the registry and will be re-checked on the next interval. This supports transient failures without requiring re-registration.

---

## 12) OIDC/OAuth token validation

### 12.1 Why OIDC defers JWKS crypto verification

The v1.05 OIDC validator does NOT perform cryptographic signature verification against the JWKS endpoint. Instead, it validates claims only (issuer, audience, expiry). The JWKS URI is discovered and cached for future use.

Rationale:
1. Go's standard library does not include JWK-to-crypto-key parsing
2. Adding a JWK library (e.g., `lestrrat-go/jwx`) introduces a significant dependency for a v1.05 feature that is being added as a P2 (enterprise) track
3. The current validation is sufficient for the v1.05 use case: the token is transmitted over TLS, the issuer is verified, and expiry is enforced
4. Cryptographic verification will be added in v1.06 with a proper JWK library

### 12.2 Middleware integration

```go
// internal/middleware/auth.go

func WithAuth(next http.Handler) http.Handler {
    // If OIDC configured and Bearer token present:
    //   1. Validate token claims
    //   2. Extract tenant_id from claims
    //   3. Extract principal_id from sub claim
    //   4. Extract scopes from scope claim
    // If validation fails: log warning, fall through to header-based auth
    // If no Bearer token: use header-based auth (unchanged)
}
```

The OIDC validator is initialized once at package init time (`var oidcValidator *auth.OIDCValidator`). This ensures discovery is lazy and configuration is read once.

### 12.3 Tenant extraction cascade

1. Check configured claim (`AGENTOS_OIDC_TENANT_CLAIM`, default `tenant_id`)
2. Try fallback claims: `tid`, `org_id`, `organization_id`
3. If no tenant found in claims, fall through to header-based extraction

This cascade supports multiple OIDC providers with different claim conventions.

### 12.4 Backward compatibility design

The middleware never rejects based on OIDC validation failure. This is a deliberate design for zero-downtime migration:

1. Operator configures OIDC env vars
2. Clients start sending Bearer tokens alongside existing headers
3. If OIDC validation succeeds, tenant/principal are extracted from claims
4. If OIDC validation fails, header-based values are used
5. Once all clients are sending valid tokens, operators can optionally add a strict mode (v1.06+)

---

## 13) File store deprecation

### 13.1 Warning mechanism

```go
// internal/storage/factory.go

var fileBackendWarningOnce sync.Once

func warnFileBackend(store string) {
    fileBackendWarningOnce.Do(func() {
        slog.Warn("file-based storage backend is not recommended for production",
            "store", store,
            "recommendation", "set AGENTOS_STORAGE_BACKEND=postgres",
            "migration", "use 'agentos migrate file-to-postgres'",
        )
    })
}
```

`sync.Once` ensures the warning is logged exactly once per process, regardless of how many store types use the file backend. The first store to initialize triggers the warning; subsequent stores are silent.

### 13.2 Why once, not per-store

A per-store warning would log 4-5 warnings on startup (RunStore, AgentStore, MemoryStore, KVStore, EventLogStore), which is noisy. A single warning is sufficient to alert operators.

---

## 14) Testing strategy

### 14.1 Test coverage by track

| Track | Test file(s) | Test approach |
|-------|-------------|---------------|
| 1 — Run list | `agentorchestrator/server_test.go` | HTTP handler test with mock store |
| 2 — Event log factory | `internal/storage/factory_test.go` | Test backend selection |
| 3 — New tools | `internal/tools/*_test.go` | Unit tests per tool |
| 4 — Parallel tools | `agentorchestrator/agent_loop_test.go` | Mock tools with variable latency |
| 5 — Graceful shutdown | `agentorchestrator/listen_test.go` | Start server, submit jobs, shutdown |
| 6 — Limiter persist | `agentorchestrator/server_test.go` | Verify restoration from mock store |
| 7 — OpenAI compat | `agentorchestrator/openai_compat_test.go` | HTTP handler tests |
| 8 — Truncation | `agentorchestrator/executor_test.go` | Test truncation function |
| 10 — Batch runs | `agentorchestrator/server_test.go` | HTTP handler test |
| 11 — Federation | `federation/peer_registry_test.go` | Health check with mock HTTP |
| 12 — OIDC | `internal/auth/oidc_test.go` | Token validation edge cases |
| 13 — Deprecation | `internal/storage/factory_test.go` | Verify warning logged once |

### 14.2 Race detection

All concurrent code (parallel tool execution, health check goroutines, registry access) MUST pass `go test -race ./...`. The pre-allocated slice pattern in Track 4 and the `sync.RWMutex` in Track 11 are specifically designed to be race-free.
