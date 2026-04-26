# API Reference

Complete HTTP API for all AgentOS services. Structured for human reading and AI automation.

<!-- machine-readable: format=api-reference, version=v1.07 -->

---

## Common Patterns

### Authentication

All tenant-scoped endpoints require one of:
- **Header**: `X-Tenant-Id: tnt_<id>` (dev mode or with `AGENTOS_ALLOW_DEV_HEADERS`)
- **Bearer token**: `Authorization: Bearer <jwt>` (OIDC mode, tenant extracted from JWT claim)
- **API key**: `Authorization: Bearer aos_live_<key>` (tenant derived from key metadata)

### RBAC Roles

Endpoints marked with a role require at minimum that role:
- `viewer` — read-only access
- `developer` — CRUD agents, create/cancel runs, read usage
- `admin` — full access + API key management
- `owner` — full access + member management

### Pagination

List endpoints support cursor-based pagination:
- `?limit=N` — max items per page (default 50, max 200)
- `?after=<cursor>` — cursor from previous response

### Response Envelope

All JSON responses include `correlation_id` for request tracing.

### Error Responses

```json
{
  "error": "<error message>",
  "correlation_id": "<request-id>"
}
```

| Status | Meaning |
|--------|---------|
| 400 | Invalid request body or parameters |
| 401 | Missing or invalid authentication |
| 403 | Insufficient permissions (RBAC) |
| 404 | Resource not found |
| 409 | Conflict (duplicate, invalid state transition) |
| 429 | Rate limit or quota exceeded (`Retry-After` header included) |
| 500 | Internal server error |
| 501 | Feature not configured (e.g., KV store disabled) |
| 502 | Upstream provider error |

---

## Agent Orchestrator (default :8081)

<!-- service: agent-orchestrator -->

### Health

<!-- endpoint: GET /health -->
| | |
|---|---|
| **Method** | `GET` |
| **Path** | `/health` |
| **Auth** | None |
| **Response** | `{"status":"ok"}` |
| **Status** | `200` |

<!-- endpoint: GET /v1/health -->
| | |
|---|---|
| **Method** | `GET` |
| **Path** | `/v1/health` |
| **Auth** | None |
| **Response** | Health check with dependency status |
| **Status** | `200` |

<!-- endpoint: GET /v1/ready -->
| | |
|---|---|
| **Method** | `GET` |
| **Path** | `/v1/ready` |
| **Auth** | None |
| **Response** | `{"ready":true}` or `{"ready":false}` |
| **Status** | `200` (ready) or `503` (not ready) |

---

### Agents

<!-- endpoint: GET /v1/agents -->
**List agents**

```
GET /v1/agents?limit=50&after=<cursor>
X-Tenant-Id: tnt_demo
```

| Field | Value |
|-------|-------|
| **Auth** | `viewer` |
| **Query** | `limit` (int, default 50, max 200), `after` (cursor) |
| **Response** | `{"agents":[...],"has_more":false,"correlation_id":"..."}` |

<!-- endpoint: POST /v1/agents -->
**Create agent**

```
POST /v1/agents
X-Tenant-Id: tnt_demo
Content-Type: application/json

{
  "agent_id": "my-agent",
  "name": "My Agent",
  "description": "optional",
  "config": {}
}
```

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Constraints** | `agent_id`: 3-64 chars, alphanumeric/hyphen/underscore. `name`: 1-256 chars. |
| **Response** | `201` with `{"agent":{...},"correlation_id":"..."}` |
| **Errors** | `409` if agent already exists |

<!-- endpoint: GET /v1/agents/{agent_id} -->
**Get agent**

```
GET /v1/agents/{agent_id}
X-Tenant-Id: tnt_demo
```

| Field | Value |
|-------|-------|
| **Auth** | `viewer` |
| **Response** | `{"agent":{...},"correlation_id":"..."}` |

<!-- endpoint: PUT /v1/agents/{agent_id} -->
**Update agent** — increments version, creates version snapshot.

```
PUT /v1/agents/{agent_id}
X-Tenant-Id: tnt_demo
Content-Type: application/json

{
  "name": "Updated Name",
  "description": "updated",
  "config": {}
}
```

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Response** | `200` with updated agent (new version) |

<!-- endpoint: DELETE /v1/agents/{agent_id} -->
**Delete agent**

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Response** | `204` No Content |

---

### Agent Versioning

<!-- endpoint: GET /v1/agents/{agent_id}/versions -->
**List version history**

```
GET /v1/agents/{agent_id}/versions?limit=20&after=<cursor>
```

| Field | Value |
|-------|-------|
| **Auth** | `viewer` |
| **Response** | `{"versions":[{"id":1,"version":"v1","config":{...},"created_at":"..."}],"has_more":false}` |

<!-- endpoint: POST /v1/agents/{agent_id}/rollback -->
**Rollback to previous version** — fails if agent has active (queued/running) runs.

```
POST /v1/agents/{agent_id}/rollback
Content-Type: application/json

{"version": "v1"}
```

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Response** | `200` with rolled-back agent |
| **Errors** | `409` if active runs exist |

---

### Runs

<!-- endpoint: POST /v1/agents/{agent_id}/runs -->
**Create run**

```
POST /v1/agents/{agent_id}/runs
X-Tenant-Id: tnt_demo
Content-Type: application/json

{
  "input": "What is the weather?",
  "context": {},
  "run_options": {
    "timeout_ms": 300000,
    "max_retries": 3
  },
  "idempotency_key": "optional-uuid"
}
```

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Response** | `201` with `{"run":{"run_id":"run_...","status":"queued","events_url":"/v1/runs/run_.../events",...}}` |
| **Idempotency** | If `idempotency_key` matches existing run, returns `200` with existing run |
| **Quota** | Checks QPS limit, concurrent runs, token budget. Returns `429` if exceeded. |

<!-- endpoint: GET /v1/agents/{agent_id}/runs -->
**List runs**

```
GET /v1/agents/{agent_id}/runs?limit=50&after=<cursor>&status=completed
```

| Field | Value |
|-------|-------|
| **Auth** | `viewer` |
| **Query** | `limit`, `after`, `status` (filter: queued/running/completed/failed/canceled/error) |
| **Response** | `{"runs":[...],"has_more":false}` |

<!-- endpoint: POST /v1/agents/{agent_id}/runs:batch -->
**Batch create runs** (max 50)

```
POST /v1/agents/{agent_id}/runs:batch
Content-Type: application/json

{"runs": [{"input": "query 1"}, {"input": "query 2"}]}
```

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Response** | `201` with `{"runs":[...]}` |

<!-- endpoint: GET /v1/runs/{run_id} -->
**Get run**

| Field | Value |
|-------|-------|
| **Auth** | `viewer` |
| **Response** | `{"run":{...}}` |

<!-- endpoint: POST /v1/runs/{run_id}:cancel -->
**Cancel run** — run must be `queued` or `running`.

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Response** | `200` with `{"run":{"status":"canceled",...}}` |
| **Errors** | `409` if run is already terminal |

<!-- endpoint: POST /v1/runs/{run_id}/retry -->
**Retry failed run** — creates a new run with `retry_of` linking to original.

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Response** | `201` with `{"run":{"retry_of":"original_run_id",...}}` |
| **Errors** | `409` if original run is not in `failed` or `error` state |

---

### Event Streaming

<!-- endpoint: GET /v1/runs/{run_id}/events -->
**Stream stored events (SSE)** — replays all events for a completed or in-progress run.

```
GET /v1/runs/{run_id}/events
Accept: text/event-stream
Last-Event-ID: 5    (optional, for reconnection)
```

Response (SSE):
```
id: 1
event: agentos.event
data: {"event":{"event_id":"evt_...","sequence":1,"type":"agentos.run.step.completed",...}}

id: 2
event: agentos.event
data: ...
```

<!-- endpoint: GET /v1/runs/{run_id}/stream -->
**Live event stream (SSE)** — streams events in real-time during execution. Sends heartbeats every 15s. Terminates with `{"done":true}` when run completes.

```
GET /v1/runs/{run_id}/stream?from_sequence=0
```

---

### Memory & KV

<!-- endpoint: DELETE /v1/agents/{agent_id}/memory -->
**Clear conversation memory**

| Field | Value |
|-------|-------|
| **Response** | `{"cleared":true}` |
| **Errors** | `501` if memory store not configured |

<!-- endpoint: GET /v1/agents/{agent_id}/kv -->
**List KV keys** → `{"keys":["key1","key2"]}`

<!-- endpoint: GET /v1/agents/{agent_id}/kv/{key} -->
**Get KV value** → `{"key":"...","value":"..."}`

<!-- endpoint: PUT /v1/agents/{agent_id}/kv/{key} -->
**Set KV value** — body: `{"value":"..."}`. Max value size: 64KB. Max keys per agent: 1000.

<!-- endpoint: DELETE /v1/agents/{agent_id}/kv/{key} -->
**Delete KV entry** → `{"deleted":"key"}`

---

### OpenAI-Compatible Endpoints

<!-- endpoint: POST /v1/chat/completions -->
**Chat completions** — drop-in replacement for OpenAI's chat API.

```
POST /v1/chat/completions
Authorization: Bearer <token>
Content-Type: application/json

{
  "model": "nvidia/Llama-3.3-70B-Instruct-FP8",
  "messages": [{"role": "user", "content": "Hello"}],
  "stream": false,
  "temperature": 0.7,
  "max_tokens": 1024
}
```

| Field | Value |
|-------|-------|
| **Auth** | `developer` |
| **Streaming** | Set `"stream": true` for SSE chunks. Terminal: `data: [DONE]` |
| **Response** | Standard OpenAI chat completion format |

<!-- endpoint: POST /v1/embeddings -->
**Embeddings** — proxied to model provider.

```
POST /v1/embeddings
Content-Type: application/json

{"model": "text-embedding-ada-002", "input": "Hello world"}
```

<!-- endpoint: GET /v1/models -->
**List models** → OpenAI-compatible model list.

<!-- endpoint: GET /v1/models/{model_id} -->
**Get model** → single model object.

---

### Tenant Management

<!-- endpoint: GET /v1/admin/tenants -->
**List all tenants** (platform admin)

| Field | Value |
|-------|-------|
| **Auth** | Scope `tenants:admin` |
| **Response** | `{"tenants":[...]}` |

<!-- endpoint: POST /v1/admin/tenants -->
**Create tenant**

```
POST /v1/admin/tenants
Content-Type: application/json

{
  "tenant_id": "tnt_acme",
  "name": "Acme Corp",
  "policy": {}
}
```

| Field | Value |
|-------|-------|
| **Auth** | Scope `tenants:admin` |
| **Response** | `201` with created tenant |

<!-- endpoint: GET /v1/admin/tenants/{tenant_id} -->
**Get tenant** — Auth: `tenants:admin`

<!-- endpoint: PUT /v1/admin/tenants/{tenant_id} -->
**Update tenant** — Auth: `tenants:admin`

<!-- endpoint: DELETE /v1/admin/tenants/{tenant_id} -->
**Delete tenant** — Auth: `tenants:admin`

---

### API Keys

<!-- endpoint: GET /v1/tenants/api-keys -->
**List API keys** (masked)

```
GET /v1/tenants/api-keys?limit=50&after=<cursor>
```

Response: `{"api_keys":[{"key_id":"...","key_prefix":"<AGENTOS_KEY>...","created_at":"...","expires_at":"..."}]}`

<!-- endpoint: POST /v1/tenants/api-keys -->
**Create API key** — returns the full key ONCE. Store securely.

<!-- endpoint: DELETE /v1/tenants/api-keys/{key_id} -->
**Revoke API key**

---

### Admin Operations

<!-- endpoint: GET /v1/admin/audit-log -->
**Query audit log**

```
GET /v1/admin/audit-log?limit=50&after=<cursor>&start=2026-01-01T00:00:00Z&action=agents.create
```

| Field | Value |
|-------|-------|
| **Auth** | Scope `tenants:admin` |
| **Query** | `limit`, `after`, `start`, `end`, `action` |

<!-- endpoint: GET /v1/admin/circuit-breaker -->
**Circuit breaker status** → `{"state":"closed|open|half_open","failures":0,"recovery_timeout":"30s"}`

<!-- endpoint: GET /v1/admin/backup-check -->
**Backup status** — reports last backup timestamp and DB health.

<!-- endpoint: POST /v1/admin/purge -->
**Trigger data retention purge** — deletes data older than configured retention periods.

<!-- endpoint: GET /v1/admin/pii-scan -->
**PII scan** — `?run_id=...` scans a specific run for PII.

---

### Usage & Billing

<!-- endpoint: GET /v1/usage -->
**Usage report** — token consumption by model/day.

| Field | Value |
|-------|-------|
| **Auth** | `developer` |

<!-- endpoint: GET /v1/usage/export -->
**Usage CSV export**

| Field | Value |
|-------|-------|
| **Auth** | `admin` |

---

### Auth

<!-- endpoint: POST /v1/auth/refresh -->
**Refresh access token** — exchanges refresh token for new access token (OIDC).

---

### Tenant Members (RBAC)

<!-- endpoint: POST /v1/tenants/members -->
**Add tenant member** — `{"principal_id":"...","role":"developer"}`

<!-- endpoint: DELETE /v1/tenants/members/{principal_id} -->
**Remove tenant member**

---

### Data Export & Deletion (GDPR)

<!-- endpoint: POST /v1/tenants/data-export -->
**Export tenant data** — async, returns `202`.

<!-- endpoint: POST /v1/tenants/data-delete -->
**Delete all tenant data** — async, returns `202`.

---

### Metrics

<!-- endpoint: GET /metrics -->
**Prometheus metrics** — protected by auth if `AGENTOS_METRICS_REQUIRE_AUTH=true`.

---

## Model Policy (default :8082)

<!-- service: model-policy -->

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/v1/health` | None | Health check |
| `GET` | `/v1/models` | None | List available models |
| `POST` | `/v1/models:invoke` | Tenant | Direct model invocation with policy enforcement |
| `POST` | `/v1/policy:check` | Tenant | Check if action is allowed by policy |
| `GET` | `/metrics` | Protected | Prometheus metrics |

---

## Federation (default :8083)

<!-- service: federation -->

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/v1/federation/health` | None | Health check |
| `GET` | `/v1/federation/peer` | None | Local peer identity and endpoints |
| `GET` | `/v1/federation/peer/capabilities` | None | Supported capabilities |
| `GET` | `/v1/federation/peers` | None | List registered peers |
| `POST` | `/v1/federation/peers` | None | Register a new peer |
| `POST` | `/v1/federation/runs:forward` | Tenant | Forward run to remote peer |
| `GET` | `/v1/federation/runs/{run_id}/events` | Tenant | Stream events for forwarded run (SSE) |
| `POST` | `/v1/federation/events:ingest` | Tenant | Ingest events from remote peer |
| `GET` | `/metrics` | Protected | Prometheus metrics |

### Forward Run

```
POST /v1/federation/runs:forward
X-Tenant-Id: tnt_demo
Content-Type: application/json

{
  "forward": {
    "target_selector": {"stack_id": "stk_remote"},
    "run_request": {
      "agent_id": "my-agent",
      "input": "hello from remote"
    }
  }
}
```

Response:
```json
{
  "forwarded": {
    "remote_stack_id": "stk_remote",
    "remote_run_id": "run_...",
    "remote_events_url": "https://.../v1/runs/run_.../events",
    "status": "forwarded"
  }
}
```
