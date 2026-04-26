# AGENTS — Implementation Rules (AI Automation)

This repo uses the **multi-agent calibration loop** process. See `docs/process/multi-agent-calibration-loop.md`.

## MANDATORY: Read Before Working

1. Read `CLAUDE.md` — contains hard stops and project structure
2. Read `AI_CONTRACT.md` — non-negotiable operating rules
3. Read the current version's `docs/specs/v{X}/plan.md` — your issue assignments and file ownership

**If plan.md does not exist for the version you're about to work on, STOP. Create it first.**

## Authority (must follow)

Read and follow `docs/governance/spec-authority.md`. If specs conflict, follow the precedence order in `AI_CONTRACT.md §1`.

## Spec Discipline

- `/v1` APIs are **additive-only**
- Do not invent new fields or semantics in code
- If something is ambiguous, file a proposal to `docs/proposals/` — do not guess

## Multi-Tenancy Invariants (non-negotiable)

- Every request executes within exactly one `tenant_id`
- No cross-tenant reads/writes/events
- Federation must preserve and enforce tenant context

## Implementation Boundaries

- Agent Orchestrator owns: Runs, Events, Tools/Memory ports, Scheduling, Federation client/server
- Model Policy owns: Model providers, Routing, Policy checks, Usage metering, Budgets/Quotas

## Commit Rules

- Format: `[executor] v{X} #N — description`
- One issue per commit
- `Fixes #N` in body to close git issues
- Multi-track commits are PROCESS VIOLATIONS

## File Scope

- Only modify files declared in your issue's file scope
- New files are permitted only if not claimed by another active issue
- Violations are hard failures (Gate 0)

## Calibration-Learned Invariants (hard-fail in Gate 3)

These rules were learned from retroactive validation of v1.03–v1.05. They are now hard-fail criteria.

### Error Handling
- No `_ = err` in production code. Handle it, propagate it, or annotate with `//nolint:errcheck // <reason>`.
- Acceptable exceptions: `resp.Body.Close()` in defer, SSE stream writes to already-closed connections.
- If you're not sure whether to handle an error, handle it. The Validator will reject `_ = err`.

### Bounded Collections
- Every `map` or `slice` that grows with requests MUST have eviction, TTL, or a size cap.
- Common pattern: `sync.Map` with periodic cleanup goroutine, or LRU with max entries.
- If you create a `map[string]something` in a struct, ask: "does this grow without bound?" If yes, add eviction.

### Auth Fails Closed
- When auth code hits an error, DENY access. Never default to permissive.
- `strconv.Atoi` fails on auth config? Default to `true` (require auth), not `false`.
- Token validation fails? Reject the request. Don't fall through to a weaker auth method.

### Test Requirements
- New package → must have `*_test.go`. No exceptions.
- Postgres code → must have integration test (testcontainers or `//go:build integration`).
- Code review alone cannot catch SQL bugs, sentinel mismatches, or schema issues.

### Spec Accuracy
- Don't write specs that promise more than you'll implement.
- If implementation is partial, say so in the spec: "deferred to v{X+1}".
- Aspirational features go in "Future Work", not requirements.

## Documentation Discipline

- When creating API examples or documentation referencing API contracts, **read the relevant OpenAPI spec first**. Do not write examples from memory.
  - Agent Orchestrator: `docs/data/api-contracts/agent-orchestrator/openapi.yaml`
  - Model Policy: `docs/data/api-contracts/model-policy/openapi.yaml`
  - Federation: `docs/data/api-contracts/federation/openapi.yaml`
- Field names, enum values, response wrappers, and endpoint paths MUST match the spec exactly.
- If you find a discrepancy between the spec and the code, file it — do not silently "fix" the example to match your assumption.
- *(Learned from: v1.078 #1 — 9 TAX items from writing API examples without consulting OpenAPI specs. Federation examples were entirely wrong, model list used OpenAI format instead of AO schema, tenants had nonexistent fields.)*

## README Rules

- Never rewrite `README.md` from scratch
- Preserve tone/sections; edit only impacted sections

## Process Framework

Process docs in [nexixai-latticeos](../nexixai-latticeos/):

- [Calibration Loop](../nexixai-latticeos/process/multi-agent-calibration-loop.md)
- [Drift Detection](../nexixai-latticeos/process/drift-detection.md)
- [Sync Protocol](../nexixai-latticeos/process/sync-protocol.md)
- [Conflict Resolution](../nexixai-latticeos/process/conflict-resolution.md)
