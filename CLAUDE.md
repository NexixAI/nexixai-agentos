# CLAUDE.md — AgentOS Platform

Multi-tenant agent orchestration server exposing MCP tools over streamable-HTTP.

## HARD GATE — Before Any Code Change

**You MUST answer these questions before using Edit/Write on any source file. If you cannot answer them, STOP and create the missing artifacts.**

1. **What version is this work?** (e.g., v3.1, v8.2.1). If no version exists, create a PRS first.
2. **What issue number?** (e.g., #1, #3). If no plan.md exists with issues, create the plan first.
3. **What calibration-log.md entry will you write?** If no calibration log exists, create it first.
4. **Does the commit-msg hook pass?** Format: `[executor] v{X} #{N} description`. If you're using `[fix]` or `[infra]` prefix, ask: is this actually a feature? (See FP-012)

No exceptions. Only explicit user break-glass authorization bypasses this gate.

**Self-check after every commit:** Am I about to make my 3rd commit on the same issue without updating the calibration log? If yes, STOP and update it.

---

## Tech & Structure

- **Go binary**: `~/go-sdk/go/bin/go`
- **Main binary**: `cmd/agentos/main.go`
- **Services**: agent-orchestrator (:9091), model-policy
- **Storage**: dual-backend (file + postgres), factory pattern in `internal/storage/`
- **Auth**: OIDC validation in `internal/auth/`, middleware in `internal/middleware/`
- **Federation**: peer registry + health checks in `federation/`
- **Tools**: built-in tool registry in `internal/tools/`

### Authority Documents (Tier 1 — human approval required)

| Document | Purpose |
|----------|---------|
| `AI_CONTRACT.md` | Non-negotiable operating rules |
| `docs/governance/spec-authority.md` | Conflict resolution, precedence |
| `docs/context/technical/...` | Architecture decisions |

> Note: this public mirror does NOT include the project's internal PRS / plan / calibration-log artifacts (`docs/intent/`, `docs/specs/`, `docs/proposals/`, `docs/reports/`). Those are internal development records. The framework's process IS public — see [`nexixai-latticeos`](https://github.com/NexixAI/nexixai-latticeos) for how to apply it in your own project.

### Key Invariants (repo-specific)

- Every request executes within exactly one `tenant_id` — no cross-tenant reads/writes/events
- `/v1` APIs are additive-only
- `log/slog` for all logging (no `fmt.Println` or `log.Printf`)
- `go test -race ./...` must pass before push (enforced by `.git/hooks/pre-push`)

### CI Enforcement (Level 4 Automated Gates)

- **commit-msg hook**: Requires `[executor] v{X} #{N}` format for code commits. Verifies plan.md and calibration-log.md exist. Checks circuit breaker.
- **validate-calibration**: Fails if scored results < planned issues (prevents incomplete version close).
- **lint-invariants**: Checks `_ = err` without nolint, packages without tests, `return nil` in auth error paths.
- **validate-file-ownership**: Warns if modified Go files aren't listed in plan.md issue ownership.
- **validate-circuit-breaker**: Fails pipeline if any issue has >= 3 iterations (requires human escalation).
- **validate-gate1-pipeline**: Ensures every scored result has an explicit Gate 1 PASS.
- **Process health**: `docs/process/process-health.md` tracks per-version metrics.

### Deployment

All deployments through CI/CD — no manual deploys. If no CI job exists for a service, add one before deploying.
