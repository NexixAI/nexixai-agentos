
# NexixAI AgentOS

> **Public mirror.** Sanitized public release of an active platform project. Companion to [`nexixai-latticeos`](https://github.com/NexixAI/nexixai-latticeos) (the spec authority) and [`nexixai-trust-engine`](https://github.com/NexixAI/nexixai-trust-engine) (the governance schema + plugin registry).

AgentOS is a **spec-first, multi-tenant, federated agent platform** built by **NexixAI**.

**Current version**: v10.0

---

## What AgentOS Does

AgentOS orchestrates AI agents end-to-end: agent lifecycle, run execution, model routing, tool invocation, memory, event streaming, multi-tenancy, federation, and observability. It exposes an OpenAI-compatible API and a full agent management API.

### Architecture

```
Control Plane (CPU-light)
  ├── Agent Orchestrator (:8081) — agent/run lifecycle, execution, events, RBAC
  ├── Model Policy (:8082)       — model routing, policy, token budgets, PII
  └── Federation (:8083)         — peer discovery, run forwarding, event replication

Worker Nodes (optional, GPU-pinned)
  └── Executors emit events back to Agent Orchestrator
```

### Capabilities Summary

| Area | What's implemented |
|------|--------------------|
| **Agents** | CRUD, versioning with rollback, agent-to-agent delegation |
| **Runs** | Create, cancel, retry, batch, list/filter, idempotency keys |
| **Execution** | Prompt → model call → tool parse/execute → response loop |
| **Tools** | `http_fetch`, `json_extract`, `text_truncate`, `shell_exec`, `file_read`, `file_write`, `time`, `sleep`, `agent_invoke`, `generate_implementation` |
| **Models** | OpenAI-compatible API, multi-provider (vLLM, OpenAI, Anthropic, Ollama). Default: `Qwen/Qwen2.5-Coder-32B-Instruct` via local vLLM |
| **Memory** | Per-agent conversation history + KV store |
| **Events** | SSE streaming (live + replay), durable event storage, webhook delivery |
| **Auth** | OIDC/OAuth2, API keys, RBAC (owner/admin/developer/viewer), mTLS federation |
| **Multi-tenancy** | Full isolation by `tenant_id`, per-tenant quotas, audit logging |
| **Federation** | Peer discovery, run forwarding, event replication across nodes |
| **Observability** | Prometheus metrics, OpenTelemetry tracing, structured slog logging |
| **Intent Router** | v3.1 config-driven router with 6 intents (code, reasoning, ops, search, agent, general), KB-enriched prompts |
| **Resilience** | Circuit breaker (threshold=25), exponential backoff, token budgets, rate limiting |
| **Security** | Encryption at rest (AES-256-GCM), TLS, PII detection, CORS, fail-closed auth |
| **Operations** | Health/readiness probes, data retention, backup/restore, GDPR export/delete |
| **Deployment** | CLI (`agentos up`), Docker Compose, Kubernetes, observability stack |

---

## Quick Start

### Prerequisites

- Docker + Docker Compose v2
- Go 1.22+ (only for building the CLI)

### Build and Run

```bash
# Build the CLI
go build -o agentos ./cmd/agentos

# Deploy locally
./agentos up
./agentos validate
./agentos status

# Tear down
./agentos nuke
```

### Default Endpoints

| Service | URL | Health |
|---------|-----|--------|
| Agent Orchestrator | http://127.0.0.1:50081 | `/v1/health` |
| Model Policy | http://127.0.0.1:50082 | `/v1/health` |
| Federation | http://127.0.0.1:50083 | `/v1/federation/health` |

### First Run

```bash
# Create an agent
curl -X POST http://127.0.0.1:50081/v1/agents \
  -H "X-Tenant-Id: tnt_demo" \
  -H "Content-Type: application/json" \
  -d '{"agent_id":"my-agent","name":"My Agent"}'

# Create a run
curl -X POST http://127.0.0.1:50081/v1/agents/my-agent/runs \
  -H "X-Tenant-Id: tnt_demo" \
  -H "Content-Type: application/json" \
  -d '{"input":"hello"}'

# Stream events (SSE)
curl -N http://127.0.0.1:50081/v1/runs/{run_id}/events \
  -H "X-Tenant-Id: tnt_demo"
```

---

## Documentation

| Document | Purpose |
|----------|---------|
| [Configuration Reference](docs/context/technical/guides/configuration.md) | All environment variables with defaults and descriptions |
| [API Reference](docs/context/technical/guides/api-reference.md) | All HTTP endpoints across all 3 services |
| [Deployment Guide](docs/context/technical/guides/deployment.md) | Docker Compose, Kubernetes, production setup |
| [Onboarding Guide](docs/context/technical/guides/onboarding.md) | Tenant creation, API keys, first agent, first run |
| [Integration Guide](docs/context/technical/guides/integration.md) | Webhooks, OIDC, OpenAI compatibility, federation |
| [Access Guide](docs/context/technical/ops/access.md) | Service URLs and endpoint reference |
| [Backup & Restore](docs/context/technical/ops/backup-restore.md) | pg_dump, WAL archiving, disaster recovery |
| [Security Hardening](docs/context/technical/ops/security-hardening.md) | Dependency scanning, encryption, TLS, CORS |

### Version History

| Version | Focus |
|---------|-------|
| v1.02 | Baseline architecture, multi-tenancy, federation, operator UX |
| v1.025 | Production persistence (Postgres), concurrency, config validation |
| v1.03 | Real execution (model providers, agent loop, tools, memory/KV) |
| v1.035 | Operational maturity (metrics, retry/CB, token budgets, durable events) |
| v1.04 | Production integrity (health checks, fail-fast, test coverage, logging) |
| v1.055 | Security audit fixes (fail-closed auth, bounded collections, validation) |
| v1.05 | Commercial grade (run history, new tools, OIDC, file→postgres wiring) |
| v1.06 | Commercial readiness (RBAC, API keys, tenant API, billing, GDPR, encryption) |
| v1.07 | Production hardening (agent versioning, retry, TLS, webhooks, migrations) |
| v3.1 | Intent-based request router (6 intents, KB-enriched prompts) |
| v8.2 | Security audit and hardening |
| v10.0 | `generate_implementation` MCP tool for code generation |

---

## CLI Reference

```
agentos serve <agent-orchestrator|model-policy|federation> [--addr :PORT]
agentos up       [--compose-file PATH] [--project NAME] [--tenant ID] [--principal ID]
agentos redeploy [--compose-file PATH] [--project NAME]
agentos validate [--agent-orchestrator URL] [--model-policy URL] [--federation URL]
agentos status   [--compose-file PATH] [--project NAME]
agentos nuke     [--compose-file PATH] [--project NAME] [--hard]
agentos tenants list   [--agent-orchestrator URL]
agentos tenants create --id TENANT_ID [--name NAME] [--plan PLAN]
agentos version
```

---

## Multi-Tenancy

All requests execute within exactly one `tenant_id`. Pass tenant context via:

- **Header**: `X-Tenant-Id: tnt_your_tenant`
- **JWT claim**: `tenant_id` or `tid`
- **API key**: tenant derived from key metadata

Default tenant `tnt_demo` is seeded automatically in dev/demo mode.

Per-tenant enforcement: rate limiting (QPS), concurrent run limits, token budgets (hourly/daily), scoped storage, audit logging.

RBAC roles: `owner` > `admin` > `developer` > `viewer`. See [Onboarding Guide](docs/context/technical/guides/onboarding.md).

---

## Repository Layout

### Source Code

```
cmd/agentos/                   CLI entry point
agentorchestrator/             Agent Orchestrator service
modelpolicy/                   Model Policy service
federation/                    Federation service
internal/                      Shared packages (auth, storage, quota, metrics, audit, etc.)
tests/                         Conformance + load tests
deploy/local/                  Docker Compose configs
deploy/k8s/                    Kubernetes manifests (kustomize)
deploy/observability/          Prometheus, Grafana, Tempo
```

### LatticeOS Artifact Structure

This project follows the [LatticeOS](../nexixai-latticeos/README.md) canonical directory layout.

```
docs/governance/               Spec authority, AI contract, coding standards
docs/context/business/         Domain model
docs/context/technical/        Architecture docs by version
docs/context/technical/guides/ User & operator guides
docs/context/technical/ops/    Operations runbooks
docs/intent/                   Product requirements by version (PRS)
docs/data/schemas/             Type definitions by version
docs/data/api-contracts/       OpenAPI specs + examples
docs/specs/                    Plans, calibration logs, tracks by version
docs/rfcs/                     RFCs
docs/process/                  Calibration loop, dev guide, implementor standard
docs/model/                    Artifact traceability
docs/automation/prompts/       Phase/track runner prompts
docs/proposals/                Tier 1 change proposals (pending/approved/rejected)
deprecated/                    Archived artifacts (phase model, redirect pointers)
```

---

## Troubleshooting

### Port conflicts

```bash
lsof -i :50081        # Unix/macOS
netstat -ano | findstr :50081  # Windows
```

### Docker credential helper issues

Remove `"credsStore": "desktop"` from `~/.docker/config.json` if Docker Desktop's credential helper breaks.

### Services not responding

1. `docker compose -f deploy/local/compose.yaml ps`
2. `docker compose -f deploy/local/compose.yaml logs`
3. Use `127.0.0.1` not `localhost` (avoids IPv6)
4. Windows: use `curl.exe -4`

### Smoke tests

```bash
./scripts/smoke-local.sh       # Unix/macOS
.\scripts\smoke-local.ps1      # Windows
```
