# AGENT_START_HERE.md — AgentOS Platform

You are an AI agent joining the AgentOS platform project. This document is your single entry point. Read it completely before doing any work.

---

## Current State

- **Current version**: v1.08
- **Active plan**: `docs/specs/v1.08/plan.md`
- **Active calibration log**: `docs/specs/v1.08/calibration-log.md`
- **Architecture**: `docs/context/technical/CURRENT.md`
- **Config reference**: `docs/context/technical/guides/configuration.md`
- **Config schema (machine-parseable)**: `docs/data/config-schema.yaml`

---

## Required Reading Order

Read these files **in this order** before writing any code:

| # | File | What you learn | Time |
|---|------|---------------|------|
| 1 | **This file** | Entry point, current state, reading order | 2 min |
| 2 | `CLAUDE.md` | Two modes (planning vs execution), hard stops, project structure, key invariants | 3 min |
| 3 | `AGENTS.md` | Commit rules, file scope, spec discipline, calibration-learned hard-fail criteria | 3 min |
| 4 | `AI_CONTRACT.md` | Non-negotiable operating rules, authority precedence, security baseline | 4 min |
| 5 | `docs/governance/spec-authority.md` | Source-of-truth hierarchy, conflict resolution, locked zones | 3 min |
| 6 | `docs/specs/v{CURRENT}/plan.md` | Your issue assignments, file ownership, acceptance criteria | 5 min |
| 7 | `docs/specs/v{CURRENT}/calibration-log.md` | What's been scored, what's pending | 2 min |

**If you are only doing planning/design work** (gap analysis, brainstorming, drafting PRS), read files 1-5. No process constraints apply.

**If you are writing code**, all 7 files are mandatory and the calibration loop process governs your work.

---

## The Calibration Loop (Summary)

Full specification: `docs/process/multi-agent-calibration-loop.md`

```
1. PRS exists        → docs/intent/v{X}/prs.md
2. Plan exists       → docs/specs/v{X}/plan.md (issues, files, acceptance criteria)
3. Calibration log   → docs/specs/v{X}/calibration-log.md (initialized)
4. Executor writes code → one issue per commit, format: [executor] v{X} #N — description
5. Validator scores  → independent verification against plan + specs
6. Calibration log updated → scored entries for every issue
7. Version closed    → all issues scored, Gate 1 (tests) passed, no unresolved escalations
```

**Speed = parallelism through the process, NEVER skipping gates.**

---

## Hard-Fail Criteria (Gate 3)

These were learned from production failures in v1.03–v1.05 and are now non-negotiable:

1. **No `_ = err`** without `//nolint:errcheck // <reason>`
2. **No unbounded in-memory collections** — every map/slice that grows with requests needs eviction/TTL/cap
3. **Auth MUST fail closed** — errors in auth code → deny access, never default permissive
4. **New packages MUST have test files** — no `*_test.go` = hard fail
5. **Postgres code MUST have integration tests** — `//go:build integration` tag required
6. **Specs MUST match implementation scope** — no aspirational specs

---

## Project Layout

```
cmd/agentos/               → main binary entry point
agentorchestrator/          → Agent Orchestrator service (runs, events, tools, memory, KV, admin)
modelpolicy/                → Model Policy service (providers, routing, budgets, circuit breaker)
federation/                 → Federation service (peer registry, forwarding, SSE proxy)
internal/
  admin/                    → Admin handlers (backup-check)
  audit/                    → Audit logging
  auth/                     → OIDC validation, token refresh, auth context
  compliance/               → PII scan, data export/delete (GDPR)
  config/                   → Configuration loading from env vars
  crypto/                   → AES-256-GCM encryption
  deploy/                   → Deployment validation
  health/                   → Health/readiness checks
  httpx/                    → HTTP response helpers
  id/                       → ID generation (prefixed ULIDs)
  jobs/                     → Internal job queue (postgres-backed)
  lifecycle/                → Data retention purge
  logging/                  → Structured logging middleware
  metrics/                  → Prometheus metrics
  middleware/               → Auth, RBAC, CORS, API key, request ID middleware
  pii/                      → PII detection (regex + Luhn)
  quota/                    → Rate limiting + token budget
  secrets/                  → Secret file loading (_FILE suffix)
  storage/                  → Dual-backend storage (file + postgres), factory pattern
  storage/postgres/         → PostgreSQL implementations, migrations
  telemetry/                → OpenTelemetry tracing
  tenants/                  → Tenant CRUD API
  tlsconfig/                → TLS configuration
  tokens/                   → Token counting
  tools/                    → Built-in tool registry
  types/                    → Shared domain types
  usage/                    → Usage reporting API
  validate/                 → Input validation
  webhook/                  → Webhook delivery
docs/context/technical/ops/      → Runbooks (backup, security, access)
docs/data/api-contracts/         → OpenAPI 3.0 specs (per service) + JSON examples
docs/context/technical/          → Architecture docs (versioned + CURRENT.md)
docs/context/technical/guides/   → Operator guides (config, deployment, onboarding, integration, API ref)
docs/specs/                      → Execution plans + calibration logs (per version)
docs/process/                    → Methodology docs (calibration loop, AI dev guide, traceability)
docs/intent/                     → Product requirements (per version)
docs/proposals/                  → Change proposals
docs/rfcs/                       → RFCs
deploy/                     → Docker Compose, Kubernetes, Helm, observability configs
```

---

## Key Tools and Commands

```bash
# Go binary location
~/go-sdk/go/bin/go

# Build
~/go-sdk/go/bin/go build ./...

# Test (must pass before any commit)
~/go-sdk/go/bin/go test -race ./...

# Integration tests (requires postgres)
~/go-sdk/go/bin/go test -race -tags integration ./internal/storage/postgres/...

# Federation tests need this env var
AGENTOS_FED_AUTH_DISABLED=1 ~/go-sdk/go/bin/go test -race ./federation/...

# Git remote is 'nexixai', not 'origin'
git push nexixai main
```

---

## Artifact Traceability

See `docs/model/artifact-traceability.md` for how PRS → design → plan → calibration-log → code → tests relate to each other, and how to trace any invariant back to the failure that created it.

---

## What NOT to Do

- Do not write code without a plan.md for the current version
- Do not commit multiple issues in one commit
- Do not add aspirational features to specs
- Do not skip the Validator pass
- Do not rewrite README.md from scratch
- Do not use `fmt.Println` or `log.Printf` (use `log/slog`)
- Do not create unbounded maps that grow with requests
