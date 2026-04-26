# Onboarding Guide

Step-by-step guide for tenant creation, API key setup, and first agent run. Designed for both human operators and AI automation agents.

<!-- machine-readable: format=onboarding-guide, version=v1.08 -->

---

## Overview

```
1. Create tenant → 2. Create API key → 3. Create agent → 4. Create run → 5. Stream events
```

---

## Prerequisites

- AgentOS running (see [Deployment Guide](deployment.md))
- `curl` or HTTP client
- Platform admin credentials (for tenant creation)

Base URL used in examples: `http://127.0.0.1:50081` (local dev)

---

## Step 1: Create a Tenant

<!-- automation: step=create-tenant -->

In dev mode, `tnt_demo` is auto-seeded. For production, create tenants explicitly.

### Via CLI

```bash
./agentos tenants create --id tnt_acme --name "Acme Corp"
```

### Via API

```bash
curl -X POST http://127.0.0.1:50081/v1/admin/tenants \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-token>" \
  -d '{
    "tenant_id": "tnt_acme",
    "name": "Acme Corp",
    "policy": {
      "allowed_models": ["*"],
      "max_concurrent_runs": 25,
      "run_create_qps": 10
    }
  }'
```

**Response** (201):
```json
{
  "tenant": {
    "tenant_id": "tnt_acme",
    "name": "Acme Corp",
    "policy": {...}
  },
  "correlation_id": "req_..."
}
```

### Tenant ID Format

- Prefix: `tnt_`
- Characters: alphanumeric + underscore
- Length: 3–64 characters
- Examples: `tnt_acme`, `tnt_dev_team_1`, `tnt_prod`

---

## Step 2: Add Team Members (RBAC)

<!-- automation: step=add-members -->

Assign roles to team members. Roles are cumulative — higher roles include lower permissions.

| Role | Capabilities |
|------|-------------|
| `viewer` | Read agents, runs, usage |
| `developer` | + Create/update/delete agents, create/cancel runs |
| `admin` | + Manage API keys, view audit log |
| `owner` | + Manage team members, delete tenant |

```bash
curl -X POST http://127.0.0.1:50081/v1/tenants/members \
  -H "X-Tenant-Id: tnt_acme" \
  -H "Authorization: Bearer <owner-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "principal_id": "user@acme.com",
    "role": "developer"
  }'
```

---

## Step 3: Create an API Key

<!-- automation: step=create-api-key -->

API keys provide programmatic access without OIDC tokens.

```bash
curl -X POST http://127.0.0.1:50081/v1/tenants/api-keys \
  -H "X-Tenant-Id: tnt_acme" \
  -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI Pipeline Key",
    "role": "developer",
    "expires_in_days": 90
  }'
```

**Response** (201):
```json
{
  "api_key": {
    "key_id": "key_...",
    "key": "<AGENTOS_KEY>...",
    "tenant_id": "tnt_acme",
    "role": "developer",
    "expires_at": "2026-06-10T00:00:00Z"
  }
}
```

**The full key is returned only once.** Store it securely (secret manager, CI secrets).

### Using the API Key

All subsequent requests can authenticate with:

```
Authorization: Bearer <AGENTOS_KEY>...
```

The tenant is derived from the key metadata — `X-Tenant-Id` header is optional when using API keys.

---

## Step 4: Create an Agent

<!-- automation: step=create-agent -->

```bash
curl -X POST http://127.0.0.1:50081/v1/agents \
  -H "Authorization: Bearer <AGENTOS_KEY>..." \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "support-bot",
    "name": "Support Bot",
    "description": "Handles customer support queries",
    "config": {
      "model": "nvidia/Llama-3.3-70B-Instruct-FP8",
      "system_prompt": "You are a helpful customer support agent.",
      "tools": ["http_fetch", "json_extract"],
      "max_steps": 5,
      "temperature": 0.3
    }
  }'
```

**Response** (201):
```json
{
  "agent": {
    "agent_id": "support-bot",
    "tenant_id": "tnt_acme",
    "name": "Support Bot",
    "version": "v1",
    "status": "active",
    "config": {...},
    "created_at": "2026-03-10T..."
  }
}
```

### Agent ID Format

- Characters: alphanumeric, hyphen, underscore
- Length: 3–64 characters
- Must be unique within the tenant

---

## Step 5: Create a Run

<!-- automation: step=create-run -->

```bash
curl -X POST http://127.0.0.1:50081/v1/agents/support-bot/runs \
  -H "Authorization: Bearer <AGENTOS_KEY>..." \
  -H "Content-Type: application/json" \
  -d '{
    "input": "How do I reset my password?"
  }'
```

**Response** (201):
```json
{
  "run": {
    "run_id": "run_abc123",
    "agent_id": "support-bot",
    "tenant_id": "tnt_acme",
    "status": "queued",
    "events_url": "/v1/runs/run_abc123/events",
    "created_at": "2026-03-10T..."
  }
}
```

The run is queued and will be picked up by an executor worker.

---

## Step 6: Stream Events

<!-- automation: step=stream-events -->

### Option A: SSE Stream (real-time)

```bash
curl -N http://127.0.0.1:50081/v1/runs/run_abc123/stream \
  -H "Authorization: Bearer <AGENTOS_KEY>..."
```

Output:
```
id: 1
event: agentos.event
data: {"event":{"type":"agentos.run.started","run_id":"run_abc123",...}}

id: 2
event: agentos.event
data: {"event":{"type":"agentos.run.step.completed","payload":{"output":"To reset your password..."},...}}

data: {"done":true}
```

### Option B: Poll for Completion

```bash
# Check run status
curl http://127.0.0.1:50081/v1/runs/run_abc123 \
  -H "Authorization: Bearer <AGENTOS_KEY>..."

# Get all events after completion
curl http://127.0.0.1:50081/v1/runs/run_abc123/events \
  -H "Authorization: Bearer <AGENTOS_KEY>..."
```

---

## Step 7: Use OpenAI-Compatible API (Alternative)

<!-- automation: step=openai-compat -->

If you just need chat completions without the full agent lifecycle:

```bash
curl -X POST http://127.0.0.1:50081/v1/chat/completions \
  -H "Authorization: Bearer <AGENTOS_KEY>..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "nvidia/Llama-3.3-70B-Instruct-FP8",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

This endpoint is compatible with the OpenAI Python/JS SDK — just change the `base_url`:

```python
import openai

client = openai.OpenAI(
    base_url="http://127.0.0.1:50081/v1",
    api_key="<AGENTOS_KEY>..."
)

response = client.chat.completions.create(
    model="nvidia/Llama-3.3-70B-Instruct-FP8",
    messages=[{"role": "user", "content": "Hello!"}]
)
```

---

## Quick Reference: Common Operations

<!-- automation: quick-reference -->

### List agents
```bash
curl http://127.0.0.1:50081/v1/agents -H "Authorization: Bearer $KEY"
```

### Update agent
```bash
curl -X PUT http://127.0.0.1:50081/v1/agents/support-bot \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"Support Bot v2","config":{"temperature":0.5}}'
```

### View agent versions
```bash
curl http://127.0.0.1:50081/v1/agents/support-bot/versions \
  -H "Authorization: Bearer $KEY"
```

### Rollback agent
```bash
curl -X POST http://127.0.0.1:50081/v1/agents/support-bot/rollback \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"version":"v1"}'
```

### Cancel a run
```bash
curl -X POST http://127.0.0.1:50081/v1/runs/run_abc123:cancel \
  -H "Authorization: Bearer $KEY"
```

### Retry a failed run
```bash
curl -X POST http://127.0.0.1:50081/v1/runs/run_abc123/retry \
  -H "Authorization: Bearer $KEY"
```

### Set agent KV
```bash
curl -X PUT http://127.0.0.1:50081/v1/agents/support-bot/kv/greeting \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"value":"Welcome to Acme support!"}'
```

### Check usage
```bash
curl http://127.0.0.1:50081/v1/usage -H "Authorization: Bearer $KEY"
```

### List API keys
```bash
curl http://127.0.0.1:50081/v1/tenants/api-keys -H "Authorization: Bearer $KEY"
```

### Revoke API key
```bash
curl -X DELETE http://127.0.0.1:50081/v1/tenants/api-keys/key_123 \
  -H "Authorization: Bearer $KEY"
```

### Export tenant data (GDPR)
```bash
curl -X POST http://127.0.0.1:50081/v1/tenants/data-export \
  -H "Authorization: Bearer $KEY"
```

---

## Testing Patterns

<!-- automation: section=testing -->

### Unit Tests

All Go packages have unit tests. Run them with race detection:

```bash
~/go-sdk/go/bin/go test -race ./...
```

This must pass before any commit (enforced by pre-push hook).

### Integration Tests (Postgres)

Code that talks to PostgreSQL has integration tests gated by a build tag. These
require a running Postgres instance.

**Build tag pattern**: Integration test files use the `//go:build integration`
directive at the top of the file. This excludes them from normal `go test` runs.

```go
//go:build integration

package postgres_test

func TestRunStore_Integration(t *testing.T) {
    // ... tests that hit a real database
}
```

**Running locally** (requires Postgres):

```bash
# Option 1: Use the CI env vars against a local postgres
export AGENTOS_STORAGE_BACKEND=postgres
export AGENTOS_DB_HOST=localhost
export AGENTOS_DB_PORT=5432
export AGENTOS_DB_NAME=agentos_test
export AGENTOS_DB_USER=agentos
export AGENTOS_DB_PASSWORD=testpass
export AGENTOS_DB_SSLMODE=disable

~/go-sdk/go/bin/go test -race -tags integration ./internal/storage/postgres/...

# Option 2: Use Docker to start postgres first
docker run -d --name agentos-test-pg \
  -e POSTGRES_DB=agentos_test \
  -e POSTGRES_USER=agentos \
  -e POSTGRES_PASSWORD=testpass \
  -p 5432:5432 postgres:16-alpine

# Wait for ready, then run tests as above
```

**In CI**: The GitHub Actions workflow (`.github/workflows/ci.yml`) runs a
`postgres:16-alpine` service container and sets the env vars automatically.
Integration tests run as a separate step after unit tests.

### Federation Tests

Federation tests require auth to be disabled for local testing:

```bash
AGENTOS_FED_AUTH_DISABLED=1 ~/go-sdk/go/bin/go test -race ./federation/...
```

**Note**: `AGENTOS_FED_AUTH_DISABLED=1` is dev-only. In production, federation
always requires JWT or mTLS authentication.

### Key Invariants for Tests

These are **Gate 3 hard-fail criteria** — code that violates them will not pass
validation:

1. **New packages MUST have test files** — any new `internal/` package must ship with a `*_test.go` file
2. **Postgres code MUST have integration tests** — use `//go:build integration` tag
3. Every `_ = err` must be annotated with `//nolint:errcheck // <reason>`

See `CLAUDE.md` and `AGENTS.md` for the full list of invariants.

---

## Bootstrap & Prod Profile

AgentOS supports a prod profile that enforces real API key authentication on all endpoints. This is the recommended configuration for dogfooding multi-tenant features.

### Prerequisites

- PostgreSQL 16 running and accessible (deploy `infrastructure/stacks/postgres.yml`)
- AgentOS image built with `docker build -t agentos:latest -f Dockerfile .`
- `infrastructure/stacks/agentos.yml` configured with `AGENTOS_PROFILE=prod` and `AGENTOS_STORAGE_BACKEND=postgres`

### First-Time Setup

1. **Deploy postgres stack** via Portainer or `docker compose -f infrastructure/stacks/postgres.yml up -d`

2. **Deploy agentos stack** — it will start but all endpoints require auth

3. **Run bootstrap** to create the first tenant and API key:

```bash
# Set DB connection env vars (matching agentos.yml)
export AGENTOS_STORAGE_BACKEND=postgres
export AGENTOS_DB_HOST=localhost  # or container name if inside Docker network
export AGENTOS_DB_NAME=agentos
export AGENTOS_DB_USER=agentos
export AGENTOS_DB_PASSWORD=agentos-local-dev
export AGENTOS_DB_SSLMODE=disable

agentos bootstrap --tenant-id tnt_demo --name "Demo Tenant" --key-name "Open WebUI"
```

This prints an `aos_live_*` API key. **Save it immediately** — it cannot be retrieved later.

4. **Configure Open WebUI** — set the generated key as `OPENAI_API_KEY` in `infrastructure/stacks/open-webui.yml` and redeploy

5. **Create additional API keys** via the API (authenticated with the bootstrap key):

```bash
curl -X POST http://localhost:9091/v1/tenants/api-keys \
  -H "Authorization: Bearer aos_live_..." \
  -H "Content-Type: application/json" \
  -d '{"name": "CLI access", "role": "developer"}'
```

### What Prod Profile Enforces

- `AGENTOS_DEFAULT_TENANT` must NOT be set (startup fails if it is)
- `AGENTOS_ALLOW_DEV_HEADERS` must NOT be "1"
- All requests must authenticate via API key (`aos_live_*`) or OIDC bearer token
- Unauthenticated requests receive 401 `tenant_id required`

### Revoking Keys

```bash
curl -X DELETE http://localhost:9091/v1/tenants/api-keys/key_abc123 \
  -H "Authorization: Bearer aos_live_..."
```

Revoked keys immediately stop working — the middleware checks `revoked_at` on every request.
