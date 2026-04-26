# Federation v1 → Production Notes (Phase 9+)

This doc addresses the gap between "prove it works" federation (Phase 8) and production-grade federation semantics.

## What we improved (Phase 9+ patch)
### Persistent forward index
- Forward mappings (tenant_id + run_id → remote peer + events URL) are now persisted to:
  - `data/federation/forward-index.json` (default)
  - configurable via `AGENTOS_FED_FORWARD_INDEX_FILE`
- This prevents losing proxy targets on restart.

### Retry + backpressure (baseline)
- Forwarding (remote Agent Orchestrator run-create) retries transient failures:
  - network errors
  - 5xx responses
- Configure via:
  - `AGENTOS_FED_FORWARD_MAX_ATTEMPTS` (default 3)
  - `AGENTOS_FED_FORWARD_BASE_BACKOFF_MS` (default 250ms)
- SSE proxy does a small retry on initial connection (3 attempts) and dedupes by event_id.

### Replay/cursoring (baseline)
- Federation events endpoint supports optional query param:
  - `from_sequence` (integer, >=0)
- Behavior:
  - drop events with `sequence <= from_sequence` (applies to proxy and stored modes)
- This is additive and does not change event envelope schemas.

## Remaining “production” work (future hardening)
### Durable event storage
Current ingested events store is in-memory.
Production should use:
- an append-only log per (tenant_id, run_id)
- durable dedupe index (event_id) + monotonic sequence enforcement
- replay API with cursor/token semantics

### Exactly-once vs at-least-once
Today the system is effectively **at-least-once** with best-effort dedupe.
To claim exactly-once end-to-end you need:
- durable, transactional dedupe
- idempotent side-effects downstream
- explicit replay windows and retention

### Reconnect semantics for SSE
For clean reconnects, production should support:
- client-provided cursor (e.g., `from_sequence` or opaque cursor token)
- remote Agent Orchestrator support for replay/cursoring, or local buffering with retention

### Policy + trust boundary
Production federation should include:
- peer identity and verification (mTLS recommended first)
- per-peer allowlists, capability negotiation, and audit trails
- rate limiting and quotas per peer

Updated: 2025-12-21
