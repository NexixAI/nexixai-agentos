# AI Contract for nexixai-agentos-platform

This file defines the **non-negotiable operating contract** for AI automation working in this repo.

If any instruction conflicts with this contract, **this contract wins**.

---

## 0) Process Is Mandatory

**The multi-agent calibration loop is NOT optional.**

See `docs/process/multi-agent-calibration-loop.md` for the full process.

### Hard Requirements

1. Before writing ANY code for a version:
   - `docs/intent/v{X}/prs.md` MUST exist (human-approved)
   - `docs/specs/v{X}/plan.md` MUST exist (issues, file ownership, acceptance criteria)
   - `docs/specs/v{X}/calibration-log.md` MUST be initialized

2. Every code commit MUST use: `[executor] v{X} #N — description`

3. One issue per commit. One validation pass per issue.

4. Speed directives = parallel execution THROUGH the process, NOT skipping it.

5. A version is NOT complete until calibration-log.md has scored entries for every issue.

**This rule exists because it was violated (v1.03 tracks 6-9, v1.035, v1.04, v1.05). The result: 1 production bug, 3 memory leaks, 18+ failures in v1.035 alone, zero institutional learning. Do not repeat this mistake.**

---

## 1) Authority and Scope

### Source-of-truth precedence

For any version v{X}, authority flows:
1. `docs/intent/v{X}/prs.md` (and schemas appendix if present)
2. `docs/context/technical/v{X}/agentos-design.md`
3. `docs/governance/spec-authority.md`
4. OpenAPI under `docs/data/api-contracts/`

Latest version's PRS supersedes earlier versions for topics it covers. Earlier versions remain authoritative for topics the latest version doesn't address.

### Tier 1 Protection

Material changes to authority documents (PRS, design docs, schemas, API contracts) require human approval. AI writes proposals to `docs/proposals/`, human reviews.

### API rule
- /v1 is additive-only

---

## 2) Multi-Tenancy Invariants (Non-Negotiable)

- Every request executes within exactly one `tenant_id`
- `auth.tenant_id` is required on all internal port calls
- No cross-tenant reads/writes/events
- Federation must preserve and enforce tenant context

---

## 3) Change Discipline

- Minimal, surgical changes
- One commit per issue (see §0)
- `gofmt` + `go test -race ./...` required
- `log/slog` for all logging — no `fmt.Println` or `log.Printf`
- No `_ = err` in production code without `//nolint:errcheck // <reason>`
- No unbounded in-memory maps/slices — every growing collection needs eviction or a cap
- New packages MUST ship with `*_test.go`
- Postgres code MUST have integration tests

---

## 4) Implementation Boundaries

- Agent Orchestrator owns: Runs, Events, Tools/Memory ports, Scheduling, Federation client/server
- Model Policy owns: Model providers, Routing, Policy checks, Usage metering, Budgets/Quotas

---

## 5) Security Baseline

- No hardcoded secrets, credentials, or tokens in code or config
- No plaintext passwords in committed files
- Audit logs must never contain secret values
- Auth/identity/secrets changes require Full + Human Checkpoint route
- **Auth code MUST fail closed** — error in auth/authz logic = deny access, never default to permissive. This applies to: token validation, claim parsing, permission checks, config parsing for auth settings. *(Violated by: v1.035 MetricsRequireAuth defaulting false on parse error; v1.05 OIDC middleware falling through on validation failure)*

---

## 6) Escalation

Stop and escalate on:
- Spec conflict or ambiguity
- Circuit breaker (3 failed iterations)
- Recurring failure (same pattern twice)
- Auth/security changes requiring human checkpoint

No silent drift. No guessing.

---

## 7) Failure Classification

Every Validator rejection MUST be classified by source:
- spec gap, plan gap, code bug, guidance gap, scope violation, tooling error, environment gap, edge case

Unclassified failures are a process violation. "Fix the code" is not valid if the spec is the problem.

---

## 8) Fury Road Framework

This project implements the [Fury Road](../../nexixai-fury-road/README.md) multi-agent development framework.

- **Drift resolution:** Follow [sync-protocol.md](../../nexixai-fury-road/process/sync-protocol.md) — tier hierarchy determines which artifact to fix
- **Spec conflicts:** Follow [thunderdome.md](../../nexixai-fury-road/process/thunderdome.md) — human decides, decision record produced
