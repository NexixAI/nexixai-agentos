# Access Guide

Service URLs, ports, and endpoint reference for AgentOS deployments.

<!-- machine-readable: format=access-guide, version=v1.07 -->

---

## Local Development (Docker Compose)

<!-- deployment: local -->

| Service | Container Port | Host Port | Base URL |
|---------|---------------|-----------|----------|
| Agent Orchestrator | 8081 | 50081 | http://127.0.0.1:50081 |
| Model Policy | 8082 | 50082 | http://127.0.0.1:50082 |
| Federation | 8083 | 50083 | http://127.0.0.1:50083 |

**Note**: Use `127.0.0.1` (not `localhost`) to avoid IPv6 issues. On Windows, use `curl.exe -4`.

---

## Health Endpoints

<!-- automation: health-check -->

| Service | Liveness | Readiness |
|---------|----------|-----------|
| Agent Orchestrator | `GET /health` or `GET /v1/health` | `GET /v1/ready` |
| Model Policy | `GET /v1/health` | `GET /v1/ready` |
| Federation | `GET /v1/federation/health` | `GET /v1/ready` |

**Liveness** returns `200` if the process is running.
**Readiness** returns `200` only when fully initialized (DB connected, executor ready).

---

## Agent Orchestrator Endpoints

<!-- service: agent-orchestrator, port: 8081 -->

### Health & Metrics

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/health` | None | Liveness probe |
| `GET` | `/v1/health` | None | Health with dependency checks |
| `GET` | `/v1/ready` | None | Readiness probe |
| `GET` | `/metrics` | Protected | Prometheus metrics |

### Agents

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/v1/agents` | viewer | List agents (paginated) |
| `POST` | `/v1/agents` | developer | Create agent |
| `GET` | `/v1/agents/{id}` | viewer | Get agent |
| `PUT` | `/v1/agents/{id}` | developer | Update agent (creates version) |
| `DELETE` | `/v1/agents/{id}` | developer | Delete agent |
| `GET` | `/v1/agents/{id}/versions` | viewer | Version history |
| `POST` | `/v1/agents/{id}/rollback` | developer | Rollback to version |

### Runs

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/v1/agents/{id}/runs` | developer | Create run |
| `GET` | `/v1/agents/{id}/runs` | viewer | List runs (paginated, filterable) |
| `POST` | `/v1/agents/{id}/runs:batch` | developer | Batch create (max 50) |
| `GET` | `/v1/runs/{id}` | viewer | Get run status |
| `POST` | `/v1/runs/{id}:cancel` | developer | Cancel queued/running run |
| `POST` | `/v1/runs/{id}/retry` | developer | Retry failed run |
| `GET` | `/v1/runs/{id}/events` | viewer | Stream stored events (SSE) |
| `GET` | `/v1/runs/{id}/stream` | viewer | Live event stream (SSE) |

### Memory & KV

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `DELETE` | `/v1/agents/{id}/memory` | — | Clear conversation memory |
| `GET` | `/v1/agents/{id}/kv` | — | List KV keys |
| `GET` | `/v1/agents/{id}/kv/{key}` | — | Get KV value |
| `PUT` | `/v1/agents/{id}/kv/{key}` | — | Set KV value |
| `DELETE` | `/v1/agents/{id}/kv/{key}` | — | Delete KV entry |

### OpenAI-Compatible

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/v1/chat/completions` | developer | Chat completions |
| `POST` | `/v1/embeddings` | — | Embeddings (proxied) |
| `GET` | `/v1/models` | — | List models |
| `GET` | `/v1/models/{id}` | — | Get model |

### Tenant & Admin

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/v1/admin/tenants` | tenants:admin | List tenants |
| `POST` | `/v1/admin/tenants` | tenants:admin | Create tenant |
| `GET` | `/v1/admin/tenants/{id}` | tenants:admin | Get tenant |
| `PUT` | `/v1/admin/tenants/{id}` | tenants:admin | Update tenant |
| `DELETE` | `/v1/admin/tenants/{id}` | tenants:admin | Delete tenant |
| `GET` | `/v1/tenants/api-keys` | tenant | List API keys (masked) |
| `POST` | `/v1/tenants/api-keys` | tenant | Create API key |
| `DELETE` | `/v1/tenants/api-keys/{id}` | tenant | Revoke API key |
| `POST` | `/v1/tenants/members` | owner | Add member |
| `DELETE` | `/v1/tenants/members/{id}` | owner | Remove member |
| `GET` | `/v1/admin/audit-log` | tenants:admin | Query audit log |
| `GET` | `/v1/admin/circuit-breaker` | tenants:admin | Circuit breaker status |
| `GET` | `/v1/admin/backup-check` | tenants:admin | Backup status |
| `POST` | `/v1/admin/purge` | tenants:admin | Trigger data purge |
| `GET` | `/v1/admin/pii-scan` | tenants:admin | PII scan |

### Usage, Auth & GDPR

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/v1/usage` | developer | Usage report |
| `GET` | `/v1/usage/export` | admin | CSV export |
| `POST` | `/v1/auth/refresh` | — | Refresh access token |
| `POST` | `/v1/tenants/data-export` | tenant | Export data (async, 202) |
| `POST` | `/v1/tenants/data-delete` | tenant | Delete data (async, 202) |

---

## Model Policy Endpoints

<!-- service: model-policy, port: 8082 -->

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/v1/health` | None | Health check |
| `GET` | `/v1/models` | None | List models |
| `POST` | `/v1/models:invoke` | Tenant | Model invocation with policy |
| `POST` | `/v1/policy:check` | Tenant | Policy check |
| `GET` | `/metrics` | Protected | Prometheus metrics |

---

## Federation Endpoints

<!-- service: federation, port: 8083 -->

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/v1/federation/health` | None | Health check |
| `GET` | `/v1/federation/peer` | None | Local peer identity |
| `GET` | `/v1/federation/peer/capabilities` | None | Capabilities |
| `GET` | `/v1/federation/peers` | None | List peers |
| `POST` | `/v1/federation/peers` | None | Register peer |
| `POST` | `/v1/federation/runs:forward` | Tenant | Forward run |
| `GET` | `/v1/federation/runs/{id}/events` | Tenant | Stream federated events |
| `POST` | `/v1/federation/events:ingest` | Tenant | Ingest events |
| `GET` | `/metrics` | Protected | Prometheus metrics |

---

## Quick Verification

```bash
# Health (all services)
curl -sf http://127.0.0.1:50081/v1/health && echo "AO: OK"
curl -sf http://127.0.0.1:50082/v1/health && echo "MP: OK"
curl -sf http://127.0.0.1:50083/v1/federation/health && echo "Fed: OK"

# Readiness
curl -sf http://127.0.0.1:50081/v1/ready && echo "AO ready"

# Create test run
curl -X POST http://127.0.0.1:50081/v1/agents/demo-agent/runs \
  -H "X-Tenant-Id: tnt_demo" \
  -H "Content-Type: application/json" \
  -d '{"input":"health check"}'
```

Reports from `agentos validate` are written to `reports/<timestamp>/`.
