# Process Health — AgentOS Platform

Tracks per-version metrics for the multi-agent calibration loop. Data sourced from `docs/specs/v{X}/plan.md` and `docs/specs/v{X}/calibration-log.md`.

---

## Per-Version Metrics

| Version | Issues Planned | Issues Scored | TAX Items | FAIL Items | Iterations | Gate 3 Violations | Notes |
|---------|---------------|---------------|-----------|------------|------------|-------------------|-------|
| v1.025  | 11            | 11            | 0         | 0          | 5          | 0                 | CI stood up iteration 5; scores were PENDING-VALIDATION until then |
| v1.03   | 9 (5 planned + 4 retroactive) | 9 | 1 | 0 | 7 | 0 | Tracks 1-5 followed process; tracks 6-9 were retroactively validated |
| v1.035  | 7             | 8 (incl. OpenAI shim) | 4 | 0 | 1 | 3 | 3 memory leaks (unbounded maps), 3 packages with no tests |
| v1.04   | 12            | 12            | 7         | 1          | 1          | 2                 | Track 5 FAIL: 2/7 error handling items. 6 untested packages |
| v1.05   | 13            | 13            | 5         | 1          | 1          | 2                 | Track 12 FAIL: OIDC auth fails open. Security-critical |
| v1.055  | 12            | 12            | 0         | 0          | 1          | 0                 | Remediation version: fixed all auth, memory, error handling gaps |
| v1.06   | 19            | 19            | 0         | 0          | 1          | 0                 | Commercial readiness: RBAC, API keys, tenants, tracing |
| v1.07   | 11            | 11            | 0         | 0          | 1          | 0                 | Production hardening: migrations, versioning, TLS, webhooks |
| v1.075  | 14            | 12            | 0         | 0          | 1          | 0                 | Test coverage push: 9 issues + Helm chart hardening |
| v1.076  | 11            | 9             | 0         | 0          | 1          | 0                 | Gap closure: wire handlers, unbounded map fix, integration tests |
| v1.077  | 5             | 5             | 1         | 0          | 2          | 0                 | Documentation: onboarding, architecture, config schema, traceability |
| v1.078  | 4             | 4             | 10        | 0          | 2          | 0                 | Doc accuracy: API examples, runbooks, CI drift detection |
| v1.079  | 5             | 5             | 3         | 0          | 2          | 0                 | CI validation: API vs OpenAPI, port validation, config drift |
| v1.08   | 4             | 4             | 5         | 0          | 8          | 0                 | API key store, bootstrap CLI, Postgres infra, Open WebUI stack |
| v1.09   | 1             | 1             | 0         | 0          | 2          | 0                 | Classifier-based routing for chat completions |

---

## Trends

### Phase 1: Process Violation Period (v1.03 tracks 6-9 through v1.05)

Versions v1.03 (tracks 6-9), v1.035, v1.04, and v1.05 were shipped as monolithic blasts without per-issue Validator scoring, calibration logging, or plan decomposition. Retroactive validation discovered:

- **v1.035**: 3 memory leaks (unbounded maps in TokenBudget.windows, fileEventLogStore.runs, Executor.eventSinks), 3 packages with zero tests, auth metrics defaults to no-auth on parse error. 4 TAX items.
- **v1.04**: 1 hard FAIL (error handling track: only 2 of 7 items fixed), 7 TAX items, 6 packages with no test files.
- **v1.05**: 1 hard FAIL (OIDC middleware fails open on validation failure — security-critical), 5 TAX items, aspirational specs that didn't match implementation.

Total across violation period: **2 hard FAILs, 16 TAX items, 7 Gate 3 violations.**

### Phase 2: Remediation (v1.055)

v1.055 was a dedicated remediation version. All 12 issues targeted the specific failures from the violation period:
- Fixed all auth-fails-open bugs
- Added eviction to all unbounded maps
- Fixed all suppressed errors
- Added integration test scaffolding
- Added test files for untested packages

Result: 12/12 PASS, 0 TAX, 0 FAIL, 0 Gate 3 violations.

### Phase 3: Sustained Quality (v1.06 through v1.09)

From v1.06 onward, every version has followed the full calibration loop with zero Gate 3 violations:

- **v1.06-v1.076**: All issues scored PASS. Zero TAX, zero FAIL. Complex features (RBAC, API keys, tenants, TLS, webhooks, migrations) shipped cleanly.
- **v1.077-v1.079**: Documentation and CI validation versions. TAX items were minor (wrong cross-reference paths, regex too narrow for CI checks). All resolved within 1-2 iterations.
- **v1.08**: Highest iteration count (8) — reflects the complexity of wiring a new auth subsystem. All TAX items documented in registry; no hard fails.
- **v1.09**: Clean 2-iteration cycle. Architecture simplified from dual-endpoint to single-endpoint with thinking toggle between iterations.

### Key Metric: Gate 3 Violations Over Time

```
v1.035:  ███ (3 violations)
v1.04:   ██  (2 violations)
v1.05:   ██  (2 violations)
v1.055:  ·   (0 — remediation)
v1.06:   ·   (0)
v1.07:   ·   (0)
v1.075:  ·   (0)
v1.076:  ·   (0)
v1.077:  ·   (0)
v1.078:  ·   (0)
v1.079:  ·   (0)
v1.08:   ·   (0)
v1.09:   ·   (0)
```

The process violation window (v1.035-v1.05) is clearly visible, followed by sustained zero-violation operation after the enforcement clauses were added to CLAUDE.md, AI_CONTRACT.md, and AGENTS.md.
