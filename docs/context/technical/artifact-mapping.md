# Artifact Mapping — Fury Road ↔ Source Code

This document maps Fury Road canonical artifact paths to the source code locations they govern. Use this when tracing a requirement or design decision to its implementation.

---

## Governance → Source

| Artifact | Governs |
|----------|---------|
| `docs/governance/spec-authority.md` | Conflict resolution rules for all artifacts |
| `docs/governance/ai-contract.md` | AI agent behavioral constraints (copy of root `AI_CONTRACT.md`) |
| `CLAUDE.md` | Claude Code steering — project invariants, hard stops |
| `AGENTS.md` | Implementation guidance for all AI agents |
| `AI_CONTRACT.md` | Non-negotiable operating rules (primary copy) |

## Intent → Source

| Artifact | Governs |
|----------|---------|
| `docs/intent/v{X}/prs.md` | Requirements → endpoints in `agentorchestrator/`, `modelpolicy/`, `federation/` |

## Data → Source

| Artifact | Governs |
|----------|---------|
| `docs/data/schemas/v{X}/schemas-appendix.md` | Type definitions → structs in `internal/` and service packages |
| `docs/data/api-contracts/openapi.yaml` | Root API spec → all HTTP handlers |
| `docs/data/api-contracts/agent-orchestrator/` | Agent Orchestrator API → `agentorchestrator/handler*.go` |
| `docs/data/api-contracts/model-policy/` | Model Policy API → `modelpolicy/handler*.go` |
| `docs/data/api-contracts/federation/` | Federation API → `federation/handler*.go` |

## Context → Source

| Artifact | Governs |
|----------|---------|
| `docs/context/technical/v{X}/agentos-design.md` | Architecture decisions → package structure, service boundaries |
| `docs/context/technical/guides/configuration.md` | Config reference → `internal/config/` |
| `docs/context/technical/guides/deployment.md` | Deploy procedures → `deploy/` |
| `docs/context/technical/ops/access.md` | Service URLs → `cmd/agentos/`, `deploy/local/` |
| `docs/context/technical/ops/backup-restore.md` | Backup procedures → `internal/admin/backup.go` |
| `docs/context/technical/ops/security-hardening.md` | Security config → `internal/auth/`, `internal/middleware/` |

## Specs → Source

| Artifact | Governs |
|----------|---------|
| `docs/specs/v{X}/plan.md` | Issue decomposition with file scope → specific source files per issue |
| `docs/specs/v{X}/calibration-log.md` | Scoring history → no direct source mapping (process artifact) |

## Process → Source

| Artifact | Governs |
|----------|---------|
| `docs/process/multi-agent-calibration-loop.md` | Execution process → no direct source mapping |
| `docs/process/ai-development-guide.md` | How to run AI work → `docs/automation/prompts/` |
| `docs/model/artifact-traceability.md` | Traceability rules → this document |

## Key Source Packages

For reverse lookup (source → governing artifact):

| Source Package | Primary Governing Artifacts |
|----------------|----------------------------|
| `cmd/agentos/` | PRS (CLI requirements), design doc (architecture) |
| `agentorchestrator/` | PRS (agent/run requirements), OpenAPI spec, schemas |
| `modelpolicy/` | PRS (model routing requirements), OpenAPI spec |
| `federation/` | PRS (federation requirements), OpenAPI spec, design doc |
| `internal/auth/` | PRS (auth requirements), security hardening guide |
| `internal/storage/` | PRS (persistence requirements), design doc (storage architecture) |
| `internal/tools/` | PRS (tool requirements), schemas (tool definitions) |
| `deploy/` | Deployment guide, ops runbooks |
| `tests/` | Plan acceptance criteria, conformance spec |
