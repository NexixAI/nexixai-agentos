# AI-Assisted Development Guide

This guide explains how to use the AI-assisted development system in the NexixAI AgentOS platform. The system is designed to prevent "AI confidently doing the wrong thing" by enforcing explicit execution boundaries, declared intent, and verifiable proof.

---

## 1. Overview

### What is AI JCL?

**AI Job Control Language (JCL)** is a disciplined, deterministic approach to AI-assisted implementation work. It borrows from classic mainframe operational discipline: explicit inputs, explicit steps, explicit failure conditions, and observable outputs.

Key principles:
- **Explicit execution** — Jobs complete fully or fail loudly; no partial success
- **Declared intent** — Specs before code; change classification is binding
- **Strict control** — Humans define, AI executes; no inference or "helpful" behavior
- **Verifiable proof** — Raw command output required; no summarized claims

For the full philosophy, see [RFC-0001 AI JCL](../rfcs/RFC-0001-ai-jcl-2025.md).

### Why This System Exists

Modern AI coding systems are optimized for plausibility and velocity. They produce things that *look* right but may silently introduce:
- Undocumented behavior changes
- Compatibility stubs that become technical debt
- Fake proof ("tests passed" without evidence)
- Scope creep and drive-by refactors

AI JCL exists to reintroduce discipline. Speed without control is not progress—it is deferred failure.

---

## 2. Key Documents

Before using this system, familiarize yourself with these documents:

| Document | Purpose | Location |
|----------|---------|----------|
| **SPEC_AUTHORITY.md** | Conflict resolution rules and locked zones | [docs/governance/spec-authority.md](../governance/spec-authority.md) |
| **AI_CONTRACT.md** | Binding operating contract for AI | [/AI_CONTRACT.md](/AI_CONTRACT.md) |
| **AGENTS.md** | Automation behavior constraints | [/AGENTS.md](/AGENTS.md) |
| **AI Implementor Standard** | Change classification rules | [ai-implementor-standard.md](ai-implementor-standard.md) |
| **RFC-0001 AI JCL** | Philosophy and templates | [../rfcs/RFC-0001-ai-jcl-2025.md](../rfcs/RFC-0001-ai-jcl-2025.md) |

### Document Precedence

If documents conflict, follow this order:
1. docs/governance/spec-authority.md
2. AI_CONTRACT.md
3. Normative specs (PRS, Schemas, OpenAPI)
4. Everything else

---

## 3. Execution Prompts

The `docs/automation/prompts/` folder contains the exact prompts used to run AI work in this repo:

| Prompt | Purpose | When to Use |
|--------|---------|-------------|
| [phase-runner.md](../automation/prompts/phase-runner.md) | Execute v1.02 phases (0–16) | During initial platform implementation |
| [track-runner.md](../automation/prompts/track-runner.md) | Execute post-v1.02 tracks | For deployment/operability improvements |

### Notes
- The repo's conflict rules live in `docs/governance/spec-authority.md`
- The repo-wide behavior constraints for automation live in `/AI_CONTRACT.md` (kept at repo root on purpose)

---

## 4. How to Run a Phase

Phases are sequential implementation steps for the core platform (v1.02 Phases 0–16 are complete).

### Step 1: Check the Next Phase

Read `configs/NEXT_PHASE.json`:
```json
{
  "plan_version": "v1.02",
  "next": 16
}
```

### Step 2: Paste the Phase Runner Prompt

Copy the entire contents of [docs/automation/prompts/phase-runner.md](../automation/prompts/phase-runner.md) into your AI tool (Codex, Claude, etc.).

### Step 3: Let AI Execute

The AI will follow this workflow:
1. **Preflight** — Sync to origin/main, create branch `phase-{N}`
2. **Implement** — Make minimal changes per the phase doc
3. **Test** — Run `go test ./...` and federation E2E
4. **Report** — Document what changed, commands run, exit codes
5. **Commit + PR** — One commit, one PR, then STOP

### Step 4: Review and Merge

Human reviews the PR. If approved, merge to main.

### Step 5: Update NEXT_PHASE.json

**Maintainer only**: After merge, increment `next` in `configs/NEXT_PHASE.json`.

---

## 5. How to Run a Track

Tracks are post-v1.02 work items for deployment parity, config validation, secrets, and packaging.

### Step 1: Check the Next Track

Read `configs/NEXT_TRACK.json`:
```json
{
  "plan_version": "v1.02",
  "next_track": 1,
  "tracks": {
    "1": { "slug": "local-deploy-parity", "doc": "docs/specs/v1.02/tracks/track-01-local-deploy-parity.md" }
  }
}
```

### Step 2: Paste the Track Runner Prompt

Copy the entire contents of [docs/automation/prompts/track-runner.md](../automation/prompts/track-runner.md) into your AI tool.

### Step 3: Let AI Execute

The AI will:
1. **Preflight** — Sync to origin/main, create branch `track-{N}-{slug}`
2. **Implement** — Follow the track doc exactly
3. **Test** — Run required gates (go test, compose smoke, etc.)
4. **Report** — Document changes and verification
5. **Commit + PR** — One commit, one PR, then STOP

### Step 4: Review and Merge

Human reviews the PR. Track work must preserve all Phase 0–16 invariants.

### Step 5: Update NEXT_TRACK.json

**Maintainer only**: After merge, increment `next_track` in `configs/NEXT_TRACK.json`.

---

## 6. Hard Guardrails (Non-Negotiable)

These rules cannot be violated under any circumstances:

### Locked Zones
The following paths are **immutable without explicit authorization**:
- `docs/intent/**`
- `docs/data/api-contracts/**`
- `docs/governance/spec-authority.md`

If AI needs to modify these, it must **STOP and report**.

### Execution Rules
- **One commit per phase/track** — No multi-commit PRs
- **One PR per phase/track** — No bundling work
- **After opening PR: STOP** — Do not start the next phase/track
- **Docker-in-network invariant** — CI must not curl localhost; use Docker DNS

### Proof Requirements
- **Raw output required** — Paste command output verbatim
- **No summarization** — "Tests passed" is meaningless without evidence
- **Silence is data** — Empty output must still be shown

### Failure Behavior
- **Missing files: STOP** — Do not guess or create stubs
- **Ambiguous instructions: STOP** — Do not infer intent
- **Test failures: Fix and re-run** — Do not amend, create new commit

---

## 7. Change Classification

Every AI-driven change must declare its classification **before execution**:

```
CHANGE CLASSIFICATION

Change Type: Hygiene / Rename / Documentation / Functional / Spec
Behavioral Change: YES / NO
API Contract Change: YES / NO
Schema Change: YES / NO
Data Migration Required: YES / NO
Risk Level: Low / Medium / High
Rollback Strategy: Revert commit(s)
```

### Rules
- Once declared, these constraints are **binding**
- If AI violates them, the job is **invalid** even if tests pass
- Hygiene-only changes must NOT alter behavior, logic, or contracts

---

## 8. Verification & Proof

### Why Raw Output is Required

One of the core failures of AI-assisted development is **fake proof**. Statements like "tests passed" or "no issues found" are meaningless unless backed by raw output.

### What Fake Proof Looks Like

**Bad:**
> I ran the tests and they all passed. The build is clean.

**Good:**
```
$ go test ./...
ok      github.com/nexixai/agentos/agentorchestrator    0.015s
ok      github.com/nexixai/agentos/modelpolicy          0.012s
ok      github.com/nexixai/agentos/federation           0.018s
```

### Verification Template

Include this in PR descriptions:
```
VERIFICATION (RAW OUTPUT)

$ go test -count=1 ./...
<PASTE OUTPUT>

$ docker compose -f deploy/local/compose.yaml config
<PASTE OUTPUT>

$ rg -n "FORBIDDEN_PATTERN" .
<PASTE OUTPUT OR EMPTY>
```

---

## 9. Troubleshooting

### "AI violated a guardrail"
- The work is invalid regardless of test results
- Discard the changes and restart with correct constraints
- Review what caused the violation

### "Phase/track doc is missing"
- AI must STOP and report
- Do not create placeholder docs
- Check `configs/NEXT_PHASE.json` or `configs/NEXT_TRACK.json` for correct paths

### "Tests failed"
- Fix the issue in the current branch
- Create a NEW commit (do not amend)
- Re-run all gates
- Only proceed when green

### "Spec conflict discovered"
- AI must STOP and escalate
- Do not attempt to resolve by modifying normative docs
- Human must decide resolution per docs/governance/spec-authority.md

### "Stale base detected"
- The branch is behind origin/main
- Reset to origin/main and restart
- Never work on a stale base

---

## 10. Quick Reference

### Pre-Flight Checklist

Before starting any AI-assisted work:

- [ ] Read `configs/NEXT_PHASE.json` or `configs/NEXT_TRACK.json`
- [ ] Verify the phase/track doc exists
- [ ] Ensure local repo is synced with origin/main
- [ ] Paste the appropriate runner prompt (phase-runner.md or track-runner.md)
- [ ] Confirm branch, canon paths, and STOP conditions

### Common Commands

```bash
# Sync with main
git fetch origin && git checkout main && git reset --hard origin/main && git clean -fd

# Create phase branch
git checkout -b phase-{N}

# Create track branch
git checkout -b track-{N}-{slug}

# Run Go tests
go test ./...

# Run federation E2E
docker compose -f deploy/local/compose.federation-2node.yaml \
  -f deploy/local/compose.federation-2node.ADDENDUM.yaml \
  up --build --abort-on-container-exit federation-e2e

# Tear down
docker compose -f deploy/local/compose.federation-2node.yaml \
  -f deploy/local/compose.federation-2node.ADDENDUM.yaml \
  down -v
```

### Document Links

| Document | Path |
|----------|------|
| SPEC_AUTHORITY | [docs/governance/spec-authority.md](../governance/spec-authority.md) |
| AI_CONTRACT | [/AI_CONTRACT.md](/AI_CONTRACT.md) |
| AGENTS | [/AGENTS.md](/AGENTS.md) |
| RFC-0001 AI JCL | [docs/rfcs/RFC-0001-ai-jcl-2025.md](../rfcs/RFC-0001-ai-jcl-2025.md) |
| Phase Runner | [docs/automation/prompts/phase-runner.md](../automation/prompts/phase-runner.md) |
| Track Runner | [docs/automation/prompts/track-runner.md](../automation/prompts/track-runner.md) |
| AI Implementor Standard | [docs/process/ai-implementor-standard.md](ai-implementor-standard.md) |
| Plan Index | [docs/specs/v1.02/INDEX.md](../specs/v1.02/INDEX.md) |
| Track Index | [docs/specs/v1.02/tracks/INDEX.md](../specs/v1.02/tracks/INDEX.md) |

---

## Summary

AI-assisted development in this repo follows **AI Job Control Language** discipline:

1. **Read the phase/track doc** — Know exactly what to implement
2. **Paste the runner prompt** — Establishes execution contract
3. **Let AI execute** — Preflight → Implement → Test → Report → Commit → STOP
4. **Verify with raw proof** — No summarized claims
5. **Human reviews and merges** — AI does not merge its own work

Speed without control is not progress—it is deferred failure.
