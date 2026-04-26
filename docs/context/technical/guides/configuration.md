# Configuration Reference

All AgentOS configuration is done via environment variables. This document is structured for both human reading and automated parsing by AI agents, deployment scripts, and configuration management tools.

<!-- machine-readable: format=env-var-table, version=v1.07 -->

---

## Conventions

- **Secret loading**: Most secrets support a `_FILE` suffix. When set, the value is read from the file at that path (for Docker secrets, Vault agent, etc.). The `_FILE` variant takes precedence.
- **Defaults**: Listed defaults apply when the variable is unset or empty.
- **Required**: Variables marked required will cause startup failure if missing (in prod profile).
- **Profile**: `AGENTOS_PROFILE` controls default behavior — `dev` is permissive, `prod` is strict.

---

## Core

<!-- env-group: core -->

| Variable | Type | Default | Required | Description |
|----------|------|---------|----------|-------------|
| `AGENTOS_PROFILE` | string | `dev` | no | Runtime profile: `dev`, `demo`, `prod`. Controls default strictness. |
| `AGENTOS_SERVICE` | string | | yes | Service name: `agent-orchestrator`, `model-policy`, `federation`. Used in logging and audit. |
| `AGENTOS_DEFAULT_TENANT` | string | | no | Default tenant ID seeded on startup. Set to `tnt_demo` for local dev. |
| `AGENTOS_LOG_FORMAT` | string | text | no | Log format: `json` (production) or empty/text (human-readable). |
| `AGENTOS_SHUTDOWN_TIMEOUT` | duration | `30s` | no | Graceful shutdown timeout. In-flight requests drain within this window. |
| `AGENTOS_MAX_BODY_SIZE` | int | `1048576` | no | Max HTTP request body size in bytes (default 1MB). |
| `AGENTOS_HEALTH_PORT` | int | `8081` | no | Port used in Docker HEALTHCHECK. |
| `AGENTOS_ALLOW_DEV_HEADERS` | bool | `false` | no | Allow dev headers (X-Tenant-Id, X-Principal-Id) in prod. **Unsafe in production.** |

---

## Storage

<!-- env-group: storage -->

| Variable | Type | Default | Required | Description |
|----------|------|---------|----------|-------------|
| `AGENTOS_STORAGE_BACKEND` | string | `file` | no | Storage backend: `file` or `postgres`. |

### File Backend

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_RUN_STORE_FILE` | path | `data/agent-orchestrator/runs.json` | File path for run storage. |
| `AGENTOS_AGENT_STORE_DIR` | path | `data/agents` | Directory for agent definitions. |
| `AGENTOS_MEMORY_STORE_DIR` | path | `data/memory` | Directory for conversation memory. |
| `AGENTOS_KV_STORE_DIR` | path | `data/kv` | Directory for key-value store. |
| `AGENTOS_EXPORT_DIR` | path | | Directory for GDPR data exports. |

### PostgreSQL Backend

| Variable | Type | Default | Required (if postgres) | Description |
|----------|------|---------|------------------------|-------------|
| `AGENTOS_DB_HOST` | string | | yes | PostgreSQL hostname. |
| `AGENTOS_DB_PORT` | int | `5432` | no | PostgreSQL port. |
| `AGENTOS_DB_NAME` | string | | yes | Database name. |
| `AGENTOS_DB_USER` | string | | yes | Database username. |
| `AGENTOS_DB_PASSWORD` | string | | yes | Database password. Supports `_FILE` suffix. |
| `AGENTOS_DB_PASSWORD_FILE` | path | | no | Path to file containing database password. Takes precedence over `AGENTOS_DB_PASSWORD`. |
| `AGENTOS_DB_SSLMODE` | string | `require` | no | PostgreSQL SSL mode: `disable`, `require`, `verify-ca`, `verify-full`. |
| `AGENTOS_DB_POOL_SIZE` | int | `10` | no | Connection pool size. |
| `AGENTOS_DB_DSN` | string | | no | Full PostgreSQL DSN. Overrides individual DB_* vars. Used in integration tests. |
| `AGENTOS_MIGRATION_DRY_RUN` | bool | `false` | no | Log migration SQL without applying. Set `true` to preview. |

---

## Model Provider

<!-- env-group: model -->

| Variable | Type | Default | Required | Description |
|----------|------|---------|----------|-------------|
| `AGENTOS_MODEL_PROVIDER` | string | | yes (for execution) | Provider type: `openai`, `anthropic`, `stub`. |
| `AGENTOS_MODEL_BASE_URL` | string | | yes (for openai) | Base URL for model API (e.g., `http://vllm:8000/v1`). |
| `AGENTOS_MODEL_API_KEY` | string | | depends | API key for model provider. Supports `_FILE` suffix. |
| `AGENTOS_MODEL_API_KEY_FILE` | path | | no | Path to file containing API key. Takes precedence. |
| `AGENTOS_MODEL_DEFAULT` | string | | no | Default model name (e.g., `nvidia/Llama-3.3-70B-Instruct-FP8`). |
| `AGENTOS_MODEL_TIMEOUT` | duration | `60s` | no | HTTP timeout for model API calls. |

---

## Execution Engine

<!-- env-group: execution -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_EXEC_WORKERS` | int | `5` | Number of executor worker goroutines. |
| `AGENTOS_EXEC_DEFAULT_MAX_STEPS` | int | `10` | Default max steps per run (agent loop iterations). |
| `AGENTOS_EXEC_DEFAULT_TIMEOUT` | duration | `300s` | Default wall-clock timeout per run. |
| `AGENTOS_EXEC_TOOL_MAX_OUTPUT` | int | `1048576` | Max bytes per tool result output (1MB). |
| `AGENTOS_AUTO_RETRY_ON_PROVIDER_ERROR` | bool | `false` | Auto-retry failed runs on provider error (max 1 retry, 5s delay). |
| `AGENTOS_MAX_DELEGATION_DEPTH` | int | `5` | Max depth for agent-to-agent delegation chains. |

---

## Quota & Rate Limiting

<!-- env-group: quota -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_QUOTA_RUN_CREATE_QPS` | float | `20` | Max run creation requests per second per tenant. |
| `AGENTOS_QUOTA_CONCURRENT_RUNS` | int | `999999` | Max concurrent runs per tenant. |
| `AGENTOS_QUOTA_INVOKE_QPS` | float | `20` | Max model invocation requests per second per tenant. |

---

## Authentication & Authorization

<!-- env-group: auth -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_AUTH_MODE` | string | `open` | Auth mode: `open` (dev), `oidc` (production). |
| `AGENTOS_METRICS_REQUIRE_AUTH` | bool | `false` | Require authentication for `/metrics` endpoint. Set `true` in production. |

### OIDC

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_OIDC_ISSUER_URL` | string | | OIDC provider issuer URL (e.g., `https://auth.example.com`). |
| `AGENTOS_OIDC_AUDIENCE` | string | | Expected JWT audience claim. |
| `AGENTOS_OIDC_TENANT_CLAIM` | string | `tenant_id` | JWT claim containing the tenant ID. |
| `AGENTOS_OIDC_TOKEN_ENDPOINT` | string | | Token endpoint for refresh (auto-discovered from issuer if unset). |

---

## TLS

<!-- env-group: tls -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_TLS_CERT` | path | | Path to TLS certificate file (PEM). Both cert and key must be set together. |
| `AGENTOS_TLS_KEY` | path | | Path to TLS private key file (PEM). |

---

## Federation

<!-- env-group: federation -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_STACK_ID` | string | `stk_local` | Local stack/peer identifier. |
| `AGENTOS_ENVIRONMENT` | string | | Environment name: `dev`, `staging`, `prod`. |
| `AGENTOS_REGION` | string | | Region identifier (e.g., `us-east-1`, `local`). |
| `AGENTOS_PEERS_FILE` | path | | Path to `peers.seed.json` for peer discovery. |
| `AGENTOS_FED_FORWARD_INDEX_FILE` | path | `data/federation/forward-index.json` | Path to forwarding index (tracks forwarded runs). |
| `AGENTOS_FED_FORWARD_MAX_ATTEMPTS` | int | `3` | Max retry attempts for federation forwards. |
| `AGENTOS_FED_FORWARD_BASE_BACKOFF_MS` | int | `250` | Base backoff in ms for federation retries. |
| `AGENTOS_FED_AUTH_DISABLED` | bool | `false` | Disable JWT auth for federation. **Dev only.** Set `1` to disable. |

### Federation mTLS

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_FED_REQUIRE_MTLS` | bool | `false` | Require mutual TLS for federation connections. |
| `AGENTOS_FED_SERVER_CERT` | path | | Federation server certificate (PEM). |
| `AGENTOS_FED_SERVER_KEY` | path | | Federation server key (PEM). |
| `AGENTOS_FED_CA_CERT` | path | | CA certificate for verifying peer certificates. |
| `AGENTOS_FED_CLIENT_CERT` | path | | Federation client certificate (for outbound mTLS). |
| `AGENTOS_FED_CLIENT_KEY` | path | | Federation client key. |
| `AGENTOS_FED_JWT_PUBLIC_KEY` | path | | Public key for JWT verification (RS256/ES256/EdDSA). |

---

## Webhooks

<!-- env-group: webhooks -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_WEBHOOK_URL` | string | | Webhook endpoint for run lifecycle events (POST). |
| `AGENTOS_WEBHOOK_SECRET` | string | | HMAC-SHA256 secret for signing webhook payloads. Signature sent in `X-Webhook-Signature` header. |
| `AGENTOS_WEBHOOK_STEP_EVENTS` | bool | `false` | Include `run.step` events in webhook delivery (verbose). |
| `AGENTOS_USAGE_WEBHOOK_URL` | string | | Legacy usage webhook URL (fallback if `AGENTOS_WEBHOOK_URL` unset). |

---

## Observability

<!-- env-group: observability -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_OTEL_ENDPOINT` | string | | OpenTelemetry collector endpoint (e.g., `http://otel-collector:4318`). Empty disables tracing. |
| `AGENTOS_OTEL_SERVICE_NAME` | string | `agentos` | Service name in traces. |
| `AGENTOS_OTEL_SAMPLE_RATE` | float | `0.1` | Trace sampling rate (0.0–1.0). |
| `AGENTOS_AUDIT_SINK` | string | `file:data/audit/{SERVICE}.audit.log` | Audit log destination: `stdout`, `stderr`, or `file:/path`. |

---

## Memory & KV Limits

<!-- env-group: memory-kv -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_MEMORY_MAX_MESSAGES` | int | `50` | Max messages per agent memory store. |
| `AGENTOS_MEMORY_MAX_TOKENS` | int | `8192` | Max tokens per agent memory store. |
| `AGENTOS_KV_MAX_VALUE_SIZE` | int | `65536` | Max value size in bytes for KV store (64KB). |
| `AGENTOS_KV_MAX_KEYS_PER_AGENT` | int | `1000` | Max keys per agent in KV store. |

---

## Data Retention

<!-- env-group: retention -->

| Variable | Type | Default | Min | Description |
|----------|------|---------|-----|-------------|
| `AGENTOS_RETENTION_RUNS_DAYS` | int | `90` | `7` | Days to retain completed runs before purge. |
| `AGENTOS_RETENTION_EVENTS_DAYS` | int | `30` | `7` | Days to retain events. |
| `AGENTOS_RETENTION_AUDIT_DAYS` | int | `365` | `90` | Days to retain audit log entries. |
| `AGENTOS_RETENTION_USAGE_DAYS` | int | `730` | `90` | Days to retain usage records. |

---

## PII Detection

<!-- env-group: pii -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_PII_ENABLED` | bool | `true` | Enable PII detection in model requests. |
| `AGENTOS_PII_DEFAULT_MODE` | string | `warn` | PII action: `warn` (log only) or `redact` (mask PII in payloads). |

---

## Encryption

<!-- env-group: encryption -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_ENCRYPTION_KEY` | string | | Base64-encoded 32-byte AES-256-GCM key. Unset = plaintext mode. |
| `AGENTOS_ENCRYPTION_KEY_FILE` | path | | Path to file containing the encryption key. |
| `AGENTOS_ENCRYPTION_KEY_PREVIOUS` | string | | Previous encryption key for key rotation (reads try both keys). |

---

## CORS

<!-- env-group: cors -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_CORS_ORIGINS` | string | | Comma-separated allowed CORS origins. Empty disables CORS headers. Never use `*` in production. |

---

## Tools

<!-- env-group: tools -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_TOOL_FILE_BASE_DIR` | path | `/tmp/agentos-sandbox` | Base directory for `file_read` and `file_write` tools. Restricts file access to this subtree. |

---

## Deployment Validation

<!-- env-group: deploy-validation -->

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `AGENTOS_PROM_URL` | string | | Prometheus URL for deployment validation (`agentos validate`). |
| `AGENTOS_GRAFANA_URL` | string | | Grafana URL for deployment validation. |
| `AGENTOS_LAST_BACKUP_TS` | string | | ISO 8601 timestamp of last backup. Reported by `GET /v1/admin/backup-check`. |

---

## Quick-Copy: Minimal Production Config

```env
# Core
AGENTOS_PROFILE=prod
AGENTOS_SERVICE=agent-orchestrator
AGENTOS_LOG_FORMAT=json

# Storage (Postgres)
AGENTOS_STORAGE_BACKEND=postgres
AGENTOS_DB_HOST=postgres.internal
AGENTOS_DB_PORT=5432
AGENTOS_DB_NAME=agentos
AGENTOS_DB_USER=agentos
AGENTOS_DB_PASSWORD_FILE=/run/secrets/db_password
AGENTOS_DB_SSLMODE=verify-full

# Model
AGENTOS_MODEL_PROVIDER=openai
AGENTOS_MODEL_BASE_URL=http://vllm:8000/v1
AGENTOS_MODEL_API_KEY_FILE=/run/secrets/model_api_key
AGENTOS_MODEL_DEFAULT=nvidia/Llama-3.3-70B-Instruct-FP8

# Auth
AGENTOS_AUTH_MODE=oidc
AGENTOS_OIDC_ISSUER_URL=https://auth.example.com
AGENTOS_OIDC_AUDIENCE=agentos
AGENTOS_METRICS_REQUIRE_AUTH=true

# Observability
AGENTOS_OTEL_ENDPOINT=http://otel-collector:4318
AGENTOS_AUDIT_SINK=file:/var/agentos/audit.log

# Encryption
AGENTOS_ENCRYPTION_KEY_FILE=/run/secrets/encryption_key
```
