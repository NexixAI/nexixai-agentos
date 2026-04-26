# Track Runner — AI Automation (post-v1.02)

Tracks are **post-v1.02 work items**. They are executed as PRs, but they are **not** PRS phases.

This runner uses **one knob**: `configs/NEXT_TRACK.json` (including `plan_version`). Canonical track docs live under `docs/specs/${plan_version}/tracks/`.

---

## Prompt (paste into AI tool)

You are working in repo `nexixai-agentos-platform`.

You must operate **SEQUENTIALLY by track** using `configs/NEXT_TRACK.json` as the **single source of truth** for what to do next.

### HARD GUARDRAILS (non-negotiable)
- Do NOT change PRS/schemas/OpenAPI or anything under:
  - `docs/intent/`
  - `docs/data/api-contracts/`
- Implement ONLY the current track document referenced by `configs/NEXT_TRACK.json`.
- Keep changes minimal and surgical (no “drive-by refactors”).
- Preserve Docker-in-network invariant: CI must not curl localhost services from the CI host.
- One commit per track.
- One PR per track.
- After opening the PR, STOP (do not start the next track).
- Do NOT change `configs/NEXT_TRACK.json` (that happens only after merge, by the maintainer).

### TRACK SELECTION
1) Read `configs/NEXT_TRACK.json`.
2) Let `TRACK_NUM = next_track` and `plan_version = plan_version`.
3) Resolve `TRACK_DOC = docs/specs/${plan_version}/tracks/track-${TRACK_NUM}-*.md` (or map via `docs/specs/${plan_version}/tracks/INDEX.md`).
4) If the canonical `TRACK_DOC` does not exist: STOP and report.

### BRANCH / PR RULES
- Fetch latest `main`.
- Ensure origin points to the canonical repo:
  - `https://github.com/NexixAI/nexixai-agentos-platform.git`
- Create a fresh branch from `origin/main`:
  - `track-{TRACK_NUM}-{slug}` (slug comes from `configs/NEXT_TRACK.json`)
- Commit message format:
  - `chore(track{TRACK_NUM}): {slug}`
- Push branch and open a PR to `main` titled:
  - `Track {TRACK_NUM}: {slug}`
- Do not merge the PR.

### REQUIRED GATES
- If the track changes Go code: run `go test ./...`.
- If the track touches deploy topology: run the relevant compose up/down smoke (see track doc).
- If the track adds a script: run it locally (or via container runner if it must be CI-safe).

### WORKFLOW (MUST FOLLOW EXACTLY)

A) Preflight
- `git remote set-url origin https://github.com/NexixAI/nexixai-agentos-platform.git`
- `git fetch origin`
- `git checkout main`
- `git reset --hard origin/main`
- `git clean -fd`
- Create branch:
  - `git checkout -b track-{TRACK_NUM}-{slug}`

B) Implement ONLY what TRACK_DOC requires
- Make the smallest set of changes required to satisfy TRACK_DOC.
- If you think you need to touch forbidden directories, STOP and report.

C) Format + tests (required gates)
- Run `gofmt` only on files you changed (unless TRACK_DOC explicitly requires repo-wide formatting).
- Run the gates required by TRACK_DOC.

D) Report (include in final message)
- Exact `TRACK_NUM` and `TRACK_DOC` implemented
- Changed files list
- Commands run + exit codes
- Any failures + fixes
- What remains (if anything)

E) Commit + PR
- Stage only relevant changes.
- Create exactly ONE commit.
- Push branch to origin.
- Open PR to main (`gh pr create` if available).
- STOP.

BEGIN NOW.
