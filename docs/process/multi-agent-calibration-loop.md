# Multi-Agent Calibration Loop — NexixAI Development Process v5

> This is the AgentOS-specific implementation of the Fury Road process.
> The generic, portable version lives in `nexixai-fury-road/process/multi-agent-calibration-loop.md`.
> The underlying model is documented in `nexixai-fury-road/model/artifact-driven-execution.md`.

## Principle

**Human authority is expressed through approval, not authorship.**

AI agents draft specs, plans, and code. Humans approve material changes to authority documents. Everything else is autonomous. Trust is earned incrementally — v1.02 proved the spec-first model works.

**Failures are inputs, not blame.** Every rejection classifies the root cause, targets the correction, and compounds into the system. The same failure never needs correction twice.

**Parallel by default.** Work is decomposed into issues with declared file ownership. Issues with no file overlap execute simultaneously across multiple agents. The spec is the shared contract — if agents implement against the same spec, they don't need to coordinate.

---

## Document Tiers

### Tier 1: Authority Documents (AI drafts, human approves)

Material changes to these documents require human approval. AI writes proposals to `docs/proposals/`, human reviews and promotes to canonical location.

| Document | Purpose |
|----------|---------|
| `docs/governance/spec-authority.md` | Conflict resolution, precedence rules |
| `AI_CONTRACT.md` | Operational contract for AI agents |
| `docs/rfcs/RFC-0001-ai-jcl-2025.md` | AI execution discipline |
| `docs/intent/v{X}/prs.md` | Behavioral requirements |
| `docs/data/schemas/v{X}/schemas-appendix.md` | Payload shapes |
| `docs/data/api-contracts/*/openapi.yaml` | API contracts |
| `docs/context/technical/v{X}/agentos-design.md` | Architecture decisions |

**"Material"** = changes to requirements, contracts, behavior, or architecture. Typo fixes, formatting, and clarifications are not material — AI commits those directly.

### Tier 2: Plan Documents (AI-autonomous)

One plan doc per version. AI creates, evolves, and updates freely during execution. No human approval needed.

| Document | Purpose |
|----------|---------|
| `docs/specs/v{X}/plan.md` | Execution plan — scope, acceptance criteria, progress |
| `docs/specs/v{X}/calibration-log.md` | Append-only log of calibration loop iterations |
| `docs/specs/v{X}/progress-log.md` | Non-normative execution ledger |
| `docs/automation/prompts/` | Execution prompts and guidance |
| `AGENTS.md` | Implementation guidance |

### Tier 3: Code and Infrastructure (AI-autonomous)

| Artifact | Scope |
|----------|-------|
| `.go` source files | All production and test code |
| `Dockerfile`, `compose.*`, k8s manifests | Deployment configs |
| CI workflow files | Pipeline definitions |
| Shell scripts | Automation |

---

## Proposal Flow (Tier 1 changes)

```
AI identifies need for spec/authority change
         │
         ▼
Write proposal to docs/proposals/
  filename: YYYY-MM-DD-short-description.md
  contents: what to change, why, diff against current
         │
         ▼
Human reviews proposal
         │
    ┌────┴────┐
    │         │
 APPROVED   REJECTED
    │         │
    ▼         ▼
AI applies   AI adjusts
change to    approach or
canonical    drafts revised
location     proposal
```

Proposals accumulate in `docs/proposals/`. Approved proposals are moved to `docs/proposals/approved/` after application. Rejected proposals are moved to `docs/proposals/rejected/` with human's reason noted.

---

## Journey Routing

Not all work needs the same scrutiny. The route through validation gates is determined by **what changed** — not team judgment.

### Route Classification

| Trigger | Route | Gates |
|---------|-------|-------|
| Auth, identity, secrets, access control, mTLS | **Full + Human Checkpoint** | All 5 gates + human review before merge |
| New API endpoints, OpenAPI changes, schema changes | **Full + Contract Check** | All 5 gates + contract conformance |
| Cross-service changes (3+ packages) | **Full** | All 5 gates |
| Single-service feature, no regulated systems | **Standard** | Gates 1, 4, 5 (tests, acceptance, quality) |
| Internal-only (refactor, logging, config) | **Abbreviated** | Gates 1, 5 (tests, quality) |
| Conflicting specs or ambiguous requirements | **Thunderdome** | Structured decision record, then resume |

### Route Detection

Routes are machine-detectable, not discretionary:
- **Path analysis**: changes to `federation/`, `internal/auth/`, `internal/secrets/` → Full + Human Checkpoint
- **File type**: changes to `openapi.yaml`, `schemas-appendix.md` → Full + Contract Check
- **Blast radius**: diff touches 3+ top-level packages → Full
- **OWNERS annotations**: `systems.yaml` marks system classification (when it exists)

The Validator determines the route at the start of each validation pass and records it in the calibration log.

---

## Version Lifecycle

### 1. Version Init

AI drafts the version's spec doc and plan doc. Human approves the spec (Tier 1). Plan doc is immediately active (Tier 2).

Per version, two canonical docs:
- `docs/intent/v{X}/prs.md` — spec (what + why)
- `docs/specs/v{X}/plan.md` — plan (how + acceptance criteria + progress)

Specs evolve across versions. v1.025 inherits v1.02 and adds to it. No need to fork the entire PRS — addendum style.

#### Preconditions Checklist

Before the first code issue executes, the Orchestrator verifies:

- [ ] **CI pipeline is runnable** — at least one test job can clone, build, and run `go test ./...` successfully. If CI is not yet configured, the first "issue" is standing up the pipeline. Code issues scored without CI execution are in PENDING-VALIDATION state (not a score — an incomplete state).
- [ ] **Git issues and milestone created** — tracking infrastructure exists
- [ ] **File ownership computed** — no unresolved overlaps in the first parallel group

> **Why CI first**: v1.025 scored all 11 issues at "95 pending CI" — when CI finally ran, 3 bugs were found. Test isolation failures and import errors are invisible to code review. Gate 1 must actually execute before any score is meaningful.

### 2. Work Decomposition: Plan → Issues → Git Issues

The Orchestrator reads the plan doc and decomposes it into issues. The plan doc is the **source of truth** — git issues are derived from it for tracking and visibility.

```
Plan doc (approved)
         │
         ▼
┌─────────────────────────────┐
│  Orchestrator               │
│  - Reads plan doc           │
│  - Defines issues in plan   │
│  - Declares file ownership  │
│  - Computes dependency graph│
│  - Identifies parallel groups│
│  - Creates git issues from  │
│    plan (derived, not source)│
│  - Creates version milestone│
└────────┬────────────────────┘
         │
         ▼
Plan doc contains (source of truth):
  - Issue definitions with acceptance criteria
  - File scope per issue
  - Dependency graph
  - Parallel group assignments
  - Progress table (updated as issues complete)

Git issues created (derived tracking):
  - Title + description from plan
  - Acceptance criteria (copied from plan)
  - File scope (copied from plan)
  - Dependencies (blocked-by: #N)
  - Route label (full, standard, abbreviated)
  - Version milestone
  - Comments: agent logs, review notes, corrections
```

#### Why Both Plan Doc and Git Issues

The plan doc is what agents read — it contains the file ownership graph, dependency order, and parallel groups in a single parseable artifact. Git issues provide what agents can't:

- **Per-issue comment threads** — agent execution logs, validator feedback, correction notes
- **Labels and milestones** — filterable status tracking across versions
- **Cross-references** — commits link to issues via `Fixes #N`
- **Audit trail** — state transitions (open → in progress → closed) with timestamps
- **Human dashboard** — milestone progress visible without reading docs

The Orchestrator keeps plan.md and git issues in sync. If the plan changes (new issue, scope adjustment), the corresponding git issue is updated.

#### Issue Template

```markdown
## Issue: [title]

**Plan ref**: plan.md §N
**Spec ref**: PRS §X.Y
**Route**: standard | full | abbreviated

### File Scope
Files this issue is allowed to modify:
- `internal/storage/postgres/run_store.go`
- `internal/storage/postgres/run_store_test.go`

### Dependencies
- blocked-by: #12 (storage interfaces must exist first)
- blocks: #15 (production compose needs postgres)

### Acceptance Criteria
- [ ] `Save()` upserts a run row; returns error on constraint violation
- [ ] `Get()` returns `(run, true, nil)` on hit, `(zero, false, nil)` on miss
- [ ] Cross-tenant isolation: tenant A cannot read tenant B's runs
- [ ] `go test -race` passes
```

#### File Ownership Rules

File ownership is the **parallelism constraint**. Two issues with overlapping file scope cannot execute simultaneously.

- Each file is owned by at most one active issue at a time
- The Orchestrator checks for overlaps before assigning parallel groups
- If overlap is unavoidable, issues run sequentially (dependency edge added)
- Shared files (e.g., `go.mod`, `go.sum`) are excluded from ownership checks — they merge cleanly in practice
- `internal/` interface files can be listed as read-only dependencies (not ownership)

#### Parallel Groups

The Orchestrator computes parallel groups from the dependency graph + file ownership:

```
Group 1 (parallel — no file overlap, no deps):
  Issue #1: postgres run_store     → Executor Agent A
  Issue #2: concurrency fixes      → Executor Agent B
  Issue #3: structured logging     → Executor Agent C

Group 2 (after Group 1 merges — depends on #1):
  Issue #4: health check depth     → Executor Agent A
  Issue #5: input validation       → Executor Agent B

Group 3 (after Group 2):
  Issue #6: production compose     → Executor Agent A
```

### 3. Parallel Execution: Calibration Loop

Each issue runs its own calibration loop independently. Multiple loops execute in parallel across agents.

```
Parallel group identified
         │
         ├── Issue #1 ──→ Executor A ──→ Validator ──→ merge to main
         ├── Issue #2 ──→ Executor B ──→ Validator ──→ merge to main
         └── Issue #3 ──→ Executor C ──→ Validator ──→ merge to main
                                                           │
                                              All merged → next group
```

#### Per-Issue Calibration Loop

```
Issue assigned to Executor
         │
         ▼
┌─────────────────────────┐
│  Router                 │
│  - Route from issue label│
└────────┬────────────────┘
         │
         ▼
┌─────────────────────┐
│  Code Executor      │
│  - Reads spec + plan│
│  - Reads issue      │
│  - Implements code  │
│  - Runs tests       │
│  - Pushes branch    │
│    (issue-N)        │
└────────┬────────────┘
         │
         ▼
┌─────────────────────────────┐
│  Validator/Auditor          │
│  - Verifies file scope      │
│  - Runs gates per route     │
│  - Scores each gate         │
│  - Classifies any failures  │
│  - Targets corrections      │
└────────┬────────────────────┘
         │
    ┌────┴────┐
    │         │
  PASS     FAIL/TAX
    │         │
    ▼         ▼
  MERGE    Validator:
  to main   1. Classifies failure source
  (sequential  2. Targets correction
  merge order) 3. Updates plan/guidance
               4. Logs to calibration log
                    │
                 Executor re-pulls,
                 re-executes
                    │
                    └──→ (back to Validator)
                         max 3 iterations
                              │
                         CIRCUIT BREAKER
                         → escalate to human
```

#### Merge Sequencing

Within a parallel group, validated branches merge sequentially:
1. First issue validated → merge to main
2. Second issue: rebase on updated main → re-validate → merge
3. Third issue: rebase on updated main → re-validate → merge

Re-validation after rebase is lightweight — only re-run gates if the rebase introduced conflicts or if the diff changed. If the rebase is clean and the diff is identical, the existing PASS carries forward.

**If rebase fails** (merge conflict): the Orchestrator checks if the conflict is in a file outside the issue's declared scope. If so, it's a file ownership violation — should not happen if ownership was correctly declared. The Orchestrator flags this as a process error and escalates.

### 4. Mid-Version Spec Changes

If during execution the AI discovers a spec gap or ambiguity:
- Write proposal to `docs/proposals/`
- Continue working on non-blocked issues
- Human approves or rejects async
- On approval, AI applies change and resumes blocked work
- Blocked issues remain assigned but paused — not re-queued

### 5. Human Intervention

Human is called back when:
- Circuit breaker fires (3 failed iterations on any issue)
- Spec proposal needs approval
- Route requires human checkpoint (auth, secrets, regulated systems)
- Recurring failure detected (same pattern twice)
- A new version boundary is reached

---

## Agents

### Orchestrator Agent

**Role**: Decomposes plan into issues, manages dependency graph, assigns parallel groups, sequences merges. Syncs plan.md to git issues for tracking.

**Can write (autonomous)**:
- `docs/specs/v{X}/plan.md` — update progress, mark sections complete
- `docs/specs/v{X}/progress-log.md` — log merges and group transitions
- Git issues — create from plan.md, update labels/status, close on completion
- Git milestones — create per version, track progress

**Can execute**:
- Dependency graph computation
- File ownership overlap detection
- Parallel group assignment
- Merge sequencing
- Plan → git issue sync (create issues, update on plan changes)

**Cannot**:
- Write code
- Modify Tier 1 docs directly
- Override file ownership constraints
- Skip dependency edges

### Code Executor Agent (one per active issue)

**Role**: Implements code for a single issue based on spec, plan, and issue description.

**Can write (autonomous)**:
- Files listed in the issue's file scope (and ONLY those files)
- New files not claimed by any other active issue
- Test files for owned source files

**Can write (proposals only)**:
- Tier 1 authority documents — via `docs/proposals/`

**Cannot**:
- Modify files outside its issue's declared scope
- Modify Tier 1 docs directly
- Approve or merge its own work
- Skip tests or bypass CI gates

### Validator/Auditor Agent

**Role**: Audits executor output against spec. Scores gates. Classifies failures. Targets corrections. Verifies file scope compliance.

**Can write (autonomous)**:
- `docs/specs/v{X}/plan.md` — update acceptance criteria, add edge cases, tighten scope
- `docs/specs/v{X}/calibration-log.md` — append iteration entries (append-only)
- `docs/automation/prompts/` — execution prompts
- `AGENTS.md` — implementation guidance

**Can write (proposals only)**:
- Tier 1 authority documents — via `docs/proposals/`

**Can execute (read-only validation)**:
- `go test ./...`, `go test -race ./...`
- Conformance tests, Federation E2E
- Code analysis (grep for patterns, race conditions, error handling)
- Route classification (path analysis, blast radius)
- File scope verification (diff vs issue's declared files)

**Cannot**:
- Modify Tier 1 docs directly
- Modify code files
- Weaken spec requirements to match bad code
- Skip failure classification

**Additional gate (parallel execution)**:
- **Gate 0: File Scope** — executor's diff MUST only touch files declared in the issue. Any file outside scope is a hard fail. This is what makes parallel execution safe.

---

## Gate Scoring

Gates produce scores, not just pass/fail. This distinguishes "hard fail, stop" from "tax and proceed with notes."

### PENDING-VALIDATION State

Issues that pass code review (Gate 2) but have not yet passed Gate 1 (tests) are in **PENDING-VALIDATION** state. This is not a score — it is an incomplete state. PENDING-VALIDATION issues:

- Are NOT eligible for auto-merge
- Do NOT count toward version completion
- MUST be resolved by running Gate 1 (CI or local tests) before the version can close
- Are logged in the calibration log with their review-only score and a `[PENDING-VALIDATION]` marker

> **Learned from v1.025**: All 11 issues were scored "95 pending CI" — a score that implied near-perfection but masked 3 real bugs. The PENDING-VALIDATION state makes the incompleteness explicit.

### Score Thresholds

| Score | Outcome | Effect |
|-------|---------|--------|
| 90-100%, zero hard fails | **Pass** | Full proceed, auto-merge eligible |
| 70-89%, zero hard fails | **Tax** | Proceed with logged concerns, auto-merge eligible |
| 50-69%, zero hard fails | **Tax (caution)** | Proceed, but Validator flags for review |
| <50% OR any hard fail | **Fail** | Stop, classify, correct, re-execute |

### Gate Criteria

Each gate has **hard-fail** criteria (binary, blocks merge) and **scored** criteria (contributes to percentage).

### Gate 0: File Scope (parallel execution only)
| Criterion | Type | Points |
|-----------|------|--------|
| Diff only touches files in issue's declared scope | Hard fail | — |
| No modifications to files owned by other active issues | Hard fail | — |

### Gate 1: Tests
| Criterion | Type | Points |
|-----------|------|--------|
| `go test ./...` — zero failures | Hard fail | 25 |
| `tests/conformance/run_conformance.py` passes | Hard fail | 15 |
| Federation E2E passes (if federation changed) | Hard fail | 15 |
| Test coverage >= 80% on new code | Scored | 10 |
| No skipped tests without justification | Scored | 5 |

### Gate 2: Spec Compliance
| Criterion | Type | Points |
|-----------|------|--------|
| API responses match OpenAPI schemas exactly | Hard fail | 20 |
| JSON payloads match Schemas Appendix | Hard fail | 15 |
| Behavior matches PRS (referenced by section) | Hard fail | 15 |
| No invented fields/semantics not in specs | Hard fail | 10 |
| Spec section referenced in commit message | Scored | 5 |

### Gate 3: Invariants
| Criterion | Type | Points |
|-----------|------|--------|
| No cross-tenant data access | Hard fail | 20 |
| No localhost from CI (Docker-network) | Hard fail | 10 |
| Zero direct Tier 1 doc modifications | Hard fail | 10 |
| No `_ = err` without `//nolint:errcheck // <reason>` | Hard fail | 10 |
| No new unbounded collections (maps/slices growing with requests without eviction or cap) | Hard fail | 10 |
| Auth code fails closed on error (never defaults to permissive) | Hard fail | 10 |
| New packages have `*_test.go` | Hard fail | 5 |
| Postgres code has integration tests | Hard fail | 5 |

> **Why these were elevated from scored to hard-fail (2026-03-08):** `_ = err` and unbounded collections were scored at 5 points each — not enough to prevent recurrence. v1.035 had 3 memory leaks from unbounded maps (escalation trigger). v1.04 Track 5 was literally a track dedicated to fixing `_ = err` and it FAILED (only 2/7 items addressed). v1.05 Track 12 had auth failing open. v1.035 shipped 3 subsystems with zero tests. v1.03 Track 6 had a production bug (postgres sentinel) that integration tests would have caught. Scored criteria that keep recurring must become hard-fail.

### Gate 4: Acceptance Criteria
| Criterion | Type | Points |
|-----------|------|--------|
| Every issue criterion satisfied | Hard fail | 25 |
| Each criterion has corresponding test | Hard fail | 15 |
| No acceptance criteria removed or weakened | Hard fail | 10 |

### Gate 5: Code Quality
| Criterion | Type | Points |
|-----------|------|--------|
| `go test -race` passes | Hard fail | 15 |
| No hardcoded secrets or credentials | Hard fail | 15 |
| No concurrent map access without sync | Scored | 10 |
| No unclosed resources | Scored | 5 |
| Proper error propagation | Scored | 5 |

### Aggregate Score

The overall score is the weighted average across gates applicable to the route. Not all routes run all gates (see Journey Routing). The Validator records the per-gate and aggregate score in the calibration log.

---

## Failure Classification

Every failure in the calibration loop is classified by **source**. This is not optional — unclassified failures are a process violation.

### Failure Sources

| Source | Description | Example |
|--------|-------------|---------|
| **Spec gap** | Spec is incomplete or ambiguous | PRS doesn't define behavior for edge case X |
| **Plan gap** | Acceptance criteria missing or too loose | Plan says "handle errors" but doesn't specify which errors |
| **Code bug** | Code doesn't match what spec + plan require | Off-by-one in pagination, missing tenant filter |
| **Guidance gap** | Agent lacked context to implement correctly | No anti-pattern note for a known pitfall |
| **Scope violation** | Executor modified files outside issue scope | Touched a file owned by another active issue |
| **Tooling error** | Edit tool or automation produced incorrect output | `replace_all` created recursive self-call in helper |
| **Environment gap** | CI/test environment not configured or misconfigured | Tests never ran because Go not installed locally |
| **Edge case** | Scenario too complex for automated correction | Requires human architectural judgment |

### Correction Targets

Each source maps to where the fix goes:

| Source | Correction Target | Who Fixes |
|--------|-------------------|-----------|
| Spec gap | `docs/proposals/` (Tier 1 proposal) | AI proposes, human approves |
| Plan gap | `docs/specs/v{X}/plan.md` | Validator (autonomous) |
| Code bug | Source code | Executor on re-execution |
| Guidance gap | `docs/automation/prompts/`, `AGENTS.md` | Validator (autonomous) |
| Scope violation | Issue file scope (add missing file) | Orchestrator re-checks ownership |
| Tooling error | Process doc anti-pattern guidance | Validator documents in guidance |
| Environment gap | CI/infrastructure config | Orchestrator (precondition checklist) |
| Edge case | Escalation report | Human decides |

The Validator MUST classify the source before targeting the correction. "Fix the code" is not a valid correction if the spec is the problem.

---

## Compounding

**The same failure should never need correction twice.**

When a correction is applied, it becomes permanent knowledge:
- **Plan gap corrected** → acceptance criteria tightened for all future work in this version
- **Guidance gap corrected** → anti-pattern note added to `AGENTS.md` or `docs/automation/prompts/`, applies globally
- **Spec gap corrected** → proposal approved and applied, inherited by future versions
- **Scope violation corrected** → file ownership rules refined, Orchestrator learns
- **Edge case documented** → added to `docs/specs/v{X}/plan.md` as a known constraint

### Compounding Evidence

The calibration log tracks whether corrections are novel or recurring:
- **Novel**: first time this failure pattern has been seen
- **Recurring**: same pattern seen in a previous iteration or version — indicates the earlier correction didn't stick

Recurring failures are escalation triggers even before the circuit breaker fires. If the same failure source appears twice in one version, the Validator MUST flag it for human review.

---

## Automation Eligibility

Some gates start with human checkpoints. Automation is **earned through demonstrated consistency**, not granted by default.

### Earning Automation

A gate checkpoint can be automated when ALL of:
- Override rate below 5% for the last 10 correction records
- No edge-case escalations in the last 5 calibration cycles
- Gate criteria unchanged for the last 3 cycles
- Human approves the automation promotion

### Current Automation Status

| Gate/Checkpoint | Status | Earned via |
|-----------------|--------|------------|
| Full route: human review | Manual | — |
| Standard route: auto-merge | Automated | Default for non-regulated code |
| Abbreviated route: auto-merge | Automated | Default for internal changes |
| Tier 1 proposal approval | Manual | — (may earn automation per-document over time) |

### Tightening and Loosening

Trust is bidirectional:
- **Earn automation**: consistent quality over N cycles → propose removing human checkpoint
- **Lose automation**: recurring failures or edge cases → revert to human checkpoint
- Changes to automation status are logged in the calibration log

---

## Calibration Log

Every iteration of the loop is recorded in `docs/specs/v{X}/calibration-log.md`.

Each entry captures:
- **Issue** — issue number and title
- **Iteration number** (1, 2, 3)
- **Timestamp**
- **Route** — which journey route was assigned
- **What the Executor attempted** — summary of code changes
- **Gate scores** — per-gate and aggregate score
- **Validator result** — PASS, TAX, or FAIL (which gate, which check)
- **Failure classification** — spec gap / plan gap / code bug / guidance gap / scope violation / edge case
- **Correction target** — where the fix went
- **What the Validator changed** — plan doc updates, guidance tweaks, proposals filed
- **Delta** — what improved vs previous iteration
- **Novel or recurring** — has this pattern been seen before

This log is append-only. It serves three purposes:
1. **Debugging** — when the circuit breaker fires, the log shows the full trajectory
2. **Learning** — patterns reveal systemic gaps, recurring failure modes, and which corrections compound
3. **Automation eligibility** — consistent scores over time earn automation promotions

Format:
```
## Issue #3 — Structured Logging | Iteration 1 — YYYY-MM-DD HH:MM UTC

**Route**: Standard
**Executor**: Agent C
**Changes**: replaced fmt.Println with slog calls across 12 files, added logging middleware
**Gate scores**: G0: PASS | G1: 95 | G4: 100 | G5: 85 | Aggregate: 93
**Result**: PASS
**Classification**: n/a (passed)
**Correction**: n/a
**Delta**: first iteration
**Novel/Recurring**: n/a

---

## Issue #1 — PostgreSQL Storage | Iteration 1 — YYYY-MM-DD HH:MM UTC

**Route**: Full
**Executor**: Agent A
**Changes**: implemented postgres run_store, migrations, factory
**Gate scores**: G0: PASS | G1: 100 | G2: 40 (hard fail) | G3: 90 | G4: 75 | G5: 85 | Aggregate: FAIL
**Result**: FAIL — Gate 2: RunStore.List() returns unsorted, spec requires created_at DESC
**Spec ref**: PRS §4.2 run listing must be ordered by creation time descending
**Classification**: code bug
**Correction target**: source code (executor re-implements)
**Validator changes**:
  - plan.md: added explicit note "List() MUST ORDER BY created_at DESC"
**Delta**: first iteration
**Novel/Recurring**: novel

---
```

---

## Rejection Protocol

When the Validator rejects, it MUST:

1. **Score the gate** — numeric score + hard-fail identification
2. **Classify the failure source** — spec gap / plan gap / code bug / guidance gap / scope violation / edge case
3. **Reference the spec section** — "PRS §4.3 requires X, code does Y"
4. **Target the correction** — update the right artifact (plan, guidance, proposal, or escalation)
5. **Log to calibration log** — full entry with issue number, classification, target, and novel/recurring flag
6. **Check for recurrence** — if pattern seen before, flag for human review

The Validator MUST NOT:
- Weaken spec requirements to match bad code
- Remove acceptance criteria to make tests pass
- Add "skip" or "defer" to avoid fixing issues
- Modify Tier 1 docs directly
- Leave a failure unclassified

---

## Circuit Breaker

**3 iterations maximum per issue.**

On circuit breaker:
1. Loop stops for that issue (other issues continue)
2. Validator produces escalation report (the calibration log IS the evidence):
   - Full iteration history already captured in `calibration-log.md`
   - Failure classification pattern (are all 3 failures the same source? different?)
   - Validator adds root cause assessment (code problem vs spec problem vs systemic)
   - Validator adds recommended next step
3. Human reviews calibration log and decides:
   - Approve pending proposals (if spec is the problem)
   - Provide manual guidance
   - Adjust issue scope
   - Split into smaller issues
4. Issues blocked by the failed issue are paused, not cancelled

**Early escalation**: recurring failure (same pattern twice) triggers human review even before iteration 3.

---

## Commit Convention

```
[orchestrator] v{X} — decompose plan into N issues
[executor]     v{X} #N — description of code change

                         Fixes #N (closes the git issue on merge)
[validator]    v{X} #N — PASS (score: S) | route: R
[validator]    v{X} #N — FAIL (gate G, score: S) | classification: C | correction: T
[proposal]     v{X} #N — propose: short description of spec change
[escalation]   v{X} #N — circuit breaker: reason
```

When git issues are used, executor commits SHOULD include `Fixes #N` (or `Closes #N`) in the commit body to auto-close the corresponding git issue on merge. The `#N` in the prefix refers to the plan issue number; the `Fixes #N` in the body refers to the git issue number (these may differ if the repo has pre-existing issues).

---

## Auto-Merge Criteria

A branch auto-merges to main when ALL of:
- Validator marks PASS or TAX (aggregate score >= 70%, zero hard fails)
- Gate 0 (file scope) passes
- All CI pipelines green
- No Tier 1 docs modified directly (proposals only)
- No manual escalation pending
- Route does not require human checkpoint (or human has approved)
- Branch rebased cleanly on current main

---

## What This Means in Practice

**For the human:**
- Review and approve spec proposals (async, not blocking)
- Review human-checkpoint routes (auth, secrets, regulated systems)
- Intervene on circuit breaker escalations and recurring failures
- Review auto-merged code at your leisure, not as a gate
- Set direction at version boundaries
- Grant or revoke automation eligibility based on calibration history

**For the agents:**
- Orchestrator decomposes work, manages the dependency graph, sequences merges
- Multiple Executors work in parallel on independent issues
- Validator audits each independently, scores gates, classifies failures
- File ownership prevents conflicts — no coordination needed between Executors
- Corrections compound — the system gets smarter every iteration
- Propose spec changes when gaps are found (don't just work around them)
- Never modify authority documents directly

---

## Known Anti-Patterns

Patterns learned from calibration loops. These compound — each entry prevents a class of future failures.

### `replace_all` Self-Reference Trap

**When**: Using an edit tool's `replace_all` mode to replace a pattern (e.g., `New("test")` → `newTestServer(t)`) in a file that contains a helper function whose body includes the same pattern.

**What happens**: `replace_all` matches inside the helper itself, creating a recursive self-call (helper calls itself instead of the original function).

**Fix**: Use targeted single replacements when the replacement text could match the search pattern. Or exclude the helper function from the replacement scope.

**Learned from**: v1.025 Iteration 5, Correction #3 — `newTestServer` called itself instead of `New()`.

### Scoring Without Gate 1

**When**: Code review (Gate 2) assigns a numeric score but Gate 1 (tests) has never run.

**What happens**: The score implies quality that hasn't been verified. Test isolation failures, import errors, and runtime bugs are invisible to code review.

**Fix**: Issues without Gate 1 execution are PENDING-VALIDATION, not scored. See "PENDING-VALIDATION State" above.

**Learned from**: v1.025 Iterations 1–4 — all 11 issues scored "95 pending CI", masking 3 real bugs.

### Process Bypass Under Speed Pressure

**When**: User requests urgency ("do it all now", "stop lagging", "ship it fast") and the agent skips plan decomposition, file ownership, Validator scoring, and calibration logging — shipping monolithic commits with no per-issue validation.

**What happens**: Multiple versions ship without gate scoring or failure classification. Bugs that the Validator would have caught go undetected. The calibration log has no entries, so the system learns nothing. Token spend is wasted because the work may need to be redone.

**Fix**: See "Process Enforcement" section below. Speed directives mean parallel execution *through* the process, not skipping the process.

**Learned from**: v1.03 tracks 6-9, v1.035, v1.04, v1.05 — all shipped as monolithic blasts with no Validator, no gate scoring, no calibration log. Discovered 2026-03-08.

### Auth Fails Open

**When**: Auth/authz code encounters an error (parse failure, missing claim, bad config value) and defaults to permissive (allow access) instead of restrictive (deny access).

**What happens**: Security bypass. Attacker can send malformed input to skip authentication. A single `if err != nil { return false }` in auth code can negate the entire auth layer.

**Fix**: Auth code MUST fail closed. Error = deny. This is now a Gate 3 hard-fail criterion. The Validator MUST check every error path in auth code and verify it results in access denial.

**Learned from**: v1.035 Track 1 — `MetricsRequireAuth()` calls `strconv.Atoi` and defaults to `false` (no auth required) on parse error. v1.05 Track 12 — OIDC middleware falls through to header-based auth when token validation fails, meaning any garbage token still gets access.

### New Subsystem Without Tests

**When**: A new package, module, or subsystem is created with production code but no corresponding test file.

**What happens**: The subsystem has zero coverage. Bugs in core logic (sentinel mismatches, unbounded collections, incorrect error handling) are invisible until production. Code review alone cannot catch these — v1.03 Track 6 proved this.

**Fix**: Every new package MUST ship with `*_test.go`. This is now a Gate 3 hard-fail criterion. The Validator MUST verify that any new directory with `.go` files also has at least one `_test.go` file. For Postgres-specific code, integration tests are required (code review cannot catch SQL/sentinel bugs).

**Learned from**: v1.035 — metrics, event log, SSE all shipped with zero tests (3 subsystems). v1.04 — 6 production packages had zero test files. v1.03 Track 6 — postgres `ErrAgentNotFound` sentinel mismatch caused HTTP 500 instead of 404; an integration test would have caught this immediately.

### Aspirational Specs

**When**: Specs describe features beyond what will actually be implemented in the current version (e.g., "Postgres-backed rate limiter" when only in-memory will ship, "streaming tool output" when only truncation will ship).

**What happens**: Validator scores against the aspirational spec and the implementation fails acceptance criteria. Either the spec gets weakened retroactively (defeating the purpose) or the track gets a TAX/FAIL that was avoidable. The gap between spec and implementation erodes trust in the spec as a reliable contract.

**Fix**: Specs MUST match implementation scope. If the implementation is partial, the spec MUST say so explicitly: "v1.05 implements in-memory rate limiting; Postgres persistence deferred to v1.06." Aspirational features belong in a "Future Work" section, not in the requirements.

**Learned from**: v1.05 Track 6 — spec said "Postgres-backed, survives restarts" but `limiter.go` is purely in-memory. v1.05 Track 8 — spec said "streaming tool results" but implementation is truncation. v1.05 Track 13 — spec required `agentos migrate file-to-postgres` CLI but it was never implemented.

---

## Process Enforcement

**The process is not advisory. It is structural. These rules are hard constraints.**

### Pre-Execution Gate

Before ANY code is written for a version, the following MUST exist:

1. `docs/specs/v{X}/plan.md` — with issues, file ownership, parallel groups, acceptance criteria
2. `docs/specs/v{X}/calibration-log.md` — initialized (even if empty)
3. Git issues created from the plan (derived tracking)

**A code commit without a corresponding plan.md is a process violation.** The agent MUST refuse to write code until the plan exists.

### Commit Format Enforcement

Every code commit MUST use the format:
```
[executor] v{X} #N — description
```

The following commit patterns are **process violations**:
- `Implement v{X}: all N tracks` — proves no per-issue validation
- `v{X} tracks N-M: description` — multi-track commits skip per-issue gates
- Any commit touching files from multiple issues without explicit scope overlap declaration

**One issue per commit. One validation pass per issue.**

### Speed Directives

**User requests for speed mean parallel execution of issues THROUGH the process, NOT skipping the process.**

When the user says "do it all", "stop lagging", "ship fast", or similar:
- Decompose into maximum parallel groups (file ownership permitting)
- Execute all groups concurrently with separate Executor agents
- Each agent runs its own calibration loop independently
- The Validator scores each issue independently
- Total wall-clock time is reduced by parallelism, NOT by skipping gates

**Under no circumstances does urgency justify:**
- Skipping plan decomposition
- Skipping file ownership declaration
- Skipping Validator gate scoring
- Skipping calibration log entries
- Multi-issue commits
- Shipping without per-issue validation

### Version Close Requirements

A version CANNOT be marked complete unless:
1. `docs/specs/v{X}/calibration-log.md` has entries for EVERY issue
2. Every issue has a Validator score (PASS or TAX, no hard fails)
3. Gate 1 (tests) has actually executed (not PENDING-VALIDATION)
4. No unresolved circuit breaker escalations

### Retroactive Validation

If code was shipped without process (process violation detected after the fact):
1. The violation is logged in the calibration log as a **process failure**
2. The existing code is treated as "Iteration 0" — unvalidated
3. The Validator runs all applicable gates against the existing code
4. Failures are classified and corrected through the normal loop
5. The version is not considered complete until retroactive validation passes

This is more expensive than following the process correctly, but less expensive than a full revert.

### Violation Accountability

Process violations are logged permanently in `docs/specs/v{X}/calibration-log.md` with:
- What was skipped
- Why it was skipped (e.g., "speed directive interpreted as process override")
- What the correction was (e.g., "retroactive validation performed")
- Compounding fix applied (e.g., "added enforcement clause to process doc")

---

**The compounding effect:**
- Early versions are expensive (surface gaps in specs, plans, guidance)
- Later versions are cheap (gaps already closed, patterns already captured)
- Calibration log is the institutional memory
- Automation eligibility expands as trust is demonstrated
- Parallel execution scales linearly with independent issues
- The system learns — every failure makes it better

**The evolution:**
- v1.02: human writes specs, single agent executes, human reviews PRs
- v1.025: AI drafts specs, human approves, dual-agent calibration loop, auto-merge
- v1.03 tracks 1-5: full process, calibration log, 3 CI corrections compounded
- v1.03 tracks 6-9 through v1.05: **PROCESS VIOLATION** — monolithic blasts, no validation. Retroactive validation required.
- v1.06+: enforcement clauses prevent recurrence, multi-agent parallel execution through the process

---

## Related Documents

### AgentOS-specific

| Document | Path | Relevance |
|----------|------|-----------|
| CLAUDE.md | `/CLAUDE.md` | Session constraints, hard stops, invariants |
| AGENTS.md | `/AGENTS.md` | Implementation guidance, calibration-learned rules |
| AI Contract | `/AI_CONTRACT.md` | Non-negotiable operating rules |
| SPEC_AUTHORITY | `docs/governance/spec-authority.md` | Conflict resolution and precedence |
| Artifact Traceability | `docs/model/artifact-traceability.md` | AgentOS artifact DAG and trace procedure |

### Fury Road generic (apply across all projects)

| Document | Path | Relevance |
|----------|------|-----------|
| Drift Detection | `nexixai-fury-road/process/drift-detection.md` | How to detect spec-code and other drift |
| Sync Protocol | `nexixai-fury-road/process/sync-protocol.md` | Which artifact to fix when drift is found |
| Thunderdome | `nexixai-fury-road/process/thunderdome.md` | Conflict resolution for same-tier disputes |
| Process Health | `nexixai-fury-road/process/process-health.md` | Metrics for process effectiveness |
| AI Development Guide | `nexixai-fury-road/process/ai-development-guide.md` | Practical guide for using the calibration loop |
