# Contributing to nexixai-agentos

AgentOS follows the LatticeOS calibration-loop process. Contributions go through PRS → plan → calibrate → execute → verify before landing.

## Quick contributions

Typo fixes, small bug fixes, doc clarifications:

1. Fork the repo
2. Make your change on a branch
3. Open a pull request with a clear description
4. CI must pass: `go build ./...`, `go test -race ./...`, `golangci-lint run`

## Larger contributions

New features, architectural changes, new tools, federation changes, governance changes:

1. **Open an issue** describing the change, the motivation, and the affected components
2. **Wait for sign-off** before implementation
3. **Submit a PR** that:
   - References the approved issue
   - Includes tests for new behavior (race-free)
   - Updates documentation if interfaces or invariants change
   - Follows the existing code style (`log/slog` for logging, no `fmt.Println` or `log.Printf`)

## Hard invariants

These cannot be violated:

- Every request executes within exactly one `tenant_id` — no cross-tenant reads/writes/events
- `/v1` APIs are additive-only (no breaking changes)
- All logging via `log/slog`
- `go test -race ./...` must pass before merge
- Identity must come from validated credentials, not caller-controlled headers

## Code of conduct

Be technical, be specific, be respectful. Disagreements are welcome; ad hominem is not.

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0 (see `LICENSE`).
