# Versioning Scheme — AgentOS

## Format: `MAJOR.MINOR.PATCH`

All version bumps follow [Semantic Versioning](https://semver.org/) adapted for the LatticeOS calibration process.

---

## Version Bumps

| Type | Bump | When | Commit prefix | Calibration loop? | Example |
|------|------|------|---------------|-------------------|---------|
| **Major** | X.0.0 | Breaking changes, new paradigm, new API surface | `[executor] vX.0 #N` | Full — PRS + plan + cal log | v1.0 → v2.0 (MCP platform) |
| **Minor** | X.Y.0 | New features, new tools, additive capabilities | `[executor] vX.Y #N` | Yes — plan + cal log | v2.0 → v2.1 (HTTP + RAG tools) |
| **Patch** | X.Y.Z | Bug fixes, no new features | `[fix] vX.Y.Z — description` | Light — cal log entry only | v2.0.1 (sandbox timeout fix) |
| **Security** | Patch minimum | Auth fixes, credential rotation, vulnerability patches | `[security] vX.Y.Z — description` | Yes — always, even for one-line changes | v2.0.2 (clearance bypass fix) |

## Non-Versioned Changes

| Type | Commit prefix | Tracking | Notes |
|------|---------------|----------|-------|
| **Docs** | `[docs]` | Git history | README, design docs, process docs, LatticeOS framework |
| **Infra** | `[infra]` | `infrastructure/ops-log.md` | Deploy scripts, stack YAMLs, CI, TLS, networking, model swaps |
| **Test** | `[test]` | Git history | Test-only changes, benchmarks, fixtures |

## Rules

1. **v1.x is closed.** All new work is v2.x+.
2. **Major bumps require human-approved PRS** (Tier 1 document).
3. **Minor bumps require plan.md + calibration-log.md** in `docs/specs/vX.Y/`.
4. **Patch bumps require a calibration log entry** but can reuse the existing plan.
5. **Security patches always get a version bump** and calibration log entry, even for a one-line fix. Auth is a hard gate — no silent security fixes.
6. **`[infra]` and `[docs]` never bump the version.** They are tracked in ops-log.md and git history respectively.
7. **Commit-msg hook enforces prefixes.** Commits touching `nexixai-agentos-platform/` code must use one of: `[executor] vX.Y #N`, `[fix] vX.Y.Z`, `[security] vX.Y.Z`, `[docs]`, `[infra]`, `[test]`, or `Merge`.
8. **Each minor version has its own directory** in `docs/specs/vX.Y/` with plan.md and calibration-log.md.
9. **Master roadmap** for a major version lives in `docs/specs/vX.0/plan.md`.

## Artifact Structure

```
docs/
  intent/
    v2.0/prs.md              ← shared PRS for all v2.x (Tier 1, human-approved)
  specs/
    v2.0/plan.md             ← master roadmap + v2.0 execution issues
    v2.0/calibration-log.md  ← v2.0 cal log
    v2.1/plan.md             ← v2.1 scoped issues only
    v2.1/calibration-log.md  ← v2.1 cal log
    ...
```

## CI Enforcement

- **commit-msg hook**: Validates prefix format on every commit
- **validate-calibration**: Checks latest `docs/specs/v*/` has scored results matching planned issues
- **lint-invariants**: Blocks `_ = err`, missing tests, auth fail-open
- **validate-circuit-breaker**: Blocks issues with >= 3 iterations
- **validate-gate1-pipeline**: Blocks PENDING-VALIDATION states
