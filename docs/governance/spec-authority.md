# Spec Authority — NexixAI AgentOS

This file defines the **source-of-truth documents** and **conflict resolution rules** for implementing AgentOS.
It exists to prevent “interpretation drift” between specs, code, automation, and generated artifacts.

---

## Normative documents (v1.02)

**Primary**
1. `docs/data/schemas/v1.02/schemas-appendix.md`
   Source of truth for **JSON payload shapes**, required fields, event envelopes, auth context, federation payloads.

2. OpenAPI specs (once populated; must match the Schemas Appendix)
   - `docs/data/api-contracts/agent-orchestrator/openapi.yaml`
   - `docs/data/api-contracts/model-policy/openapi.yaml`
   - `docs/data/api-contracts/federation/openapi.yaml`

**Secondary**
3. `docs/intent/v1.02/prs.md`
   Source of truth for **behavioral requirements**, quality attributes, deployment UX, observability, and acceptance criteria.

4. `docs/context/technical/v1.02/agentos-design.md`
   Source of truth for **internal architecture decisions**: module boundaries, ports/adapters, persistence strategy, multi-tenancy enforcement points, and federation mechanics.

5. Examples + conformance tests (once added)
   - `docs/data/api-contracts/*/examples/*.json`
   - `tests/conformance/*`

Legacy v1.02 paths have been removed; use only the canonical locations above.

---

## Process documents (non-normative)

These documents guide execution and tracking but **never override normative specs**:
- `docs/specs/v1.02/INDEX.md`
- `docs/specs/v1.02/tracks/INDEX.md`
- `docs/specs/v1.02/execution-plan.md`
- `docs/specs/v1.02/progress-log.md`
- `docs/specs/v1.02/pr-checklist.md`

Plans are **implementation guidance**, not authority.

---

## Conflict resolution (strict)

If documents disagree, the precedence order is:

1) Schemas Appendix  
2) OpenAPI specs  
3) PRS  
4) Design doc  
5) Examples / conformance tests  
6) Everything else

Rules:
- **Do not invent fields, semantics, or behavior in code.**
- Update the **highest-precedence document first**, then reconcile lower layers.
- Code that contradicts a higher-precedence document is considered **invalid**, even if tests pass.

---

## Versioning rules

- `/v1` APIs are **additive-only**.
- New optional fields are allowed.
- Existing field meaning or behavior MUST NOT change.
- Breaking changes require `/v2` documents, schemas, and endpoints.

---

## Multi-tenancy invariants (non-negotiable)

- Every request is executed within **exactly one** `tenant_id`.
- `tenant_id` MUST be present in Auth Context for all internal calls.
- No cross-tenant reads, writes, events, or federation leakage.
- Federation MUST preserve and enforce tenant context end-to-end.

Violations of tenant isolation are **release-blocking defects**.

---

## Locked zones (immutable without explicit authorization)

The following paths are **authoritative and locked**:

- `docs/intent/**`
- `docs/data/api-contracts/**`
- `docs/governance/spec-authority.md`

Rules:
- These files MUST NOT be modified unless explicitly instructed.
- Automation MUST treat changes to these paths as a hard stop requiring approval.
- Drift in locked zones is considered a **spec violation**, not an implementation choice.

---

## CI / execution invariants (non-negotiable)

These invariants define how AgentOS is built and validated:

- All multi-service tests MUST run **inside a Docker network**.
- CI runners MUST NOT curl or connect to localhost services.
- Service-to-service communication MUST use Docker DNS names.
- Federation E2E tests MUST execute in-network with real containers.
- A change that violates these rules is invalid even if local tests pass.

These rules exist to prevent false confidence and environment-specific behavior.

---

## README update rules (DO NOT OVERWRITE)

When a change affects usage, APIs, docs layout, build, deploy, or runbooks:

Rules:
1. **Do not rewrite READMEs from scratch.**
2. Preserve structure, tone, and ordering unless explicitly instructed.
3. Make **minimal diffs** limited to impacted sections.
4. Do not delete user-authored content unless clearly incorrect.
5. Keep spec links stable:
   - `docs/intent/v1.02/prs.md`
   - `docs/data/schemas/v1.02/schemas-appendix.md`
6. New content must be appended, not reorganized.

---

## Authority boundaries (human vs automation)

This project is designed for **spec-driven automation**, not human execution.

- Humans define: vision, PRS, schemas, design, plans, acceptance criteria.
- Automation (Codex) implements code, wiring, CI, scaffolding, and tests.
- Humans MUST NOT be used as a manual execution engine for repetitive edits, scaffolding, or CI wiring.
- If automation cannot proceed, it MUST stop and request clarification.
- Piecemeal execution without an approved plan is forbidden.

If this boundary is violated, execution must halt and be corrected.

---

## Implementation guidance for automation / Codex

Automation MUST:

- Treat `docs/governance/spec-authority.md` as binding authority.
- Follow the precedence rules strictly.
- Prefer updating specs over guessing in code.
- Keep changes minimal, reversible, and well-scoped.
- Update documentation alongside code when behavior or usage changes.
- Execute work **phase-by-phase**, never opportunistically or piecemeal.

Failure to follow these rules constitutes **spec drift**.

---
