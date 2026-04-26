# Conformance Tests (Phase 4)

These tests enforce "spec-first" discipline by validating canonical JSON examples against the OpenAPI schemas.

## Run locally

From repo root:

```bash
python -m pip install -r tests/conformance/requirements.txt
python tests/conformance/run_conformance.py
```

## What it checks

- Parses OpenAPI specs:
  - docs/data/api-contracts/agent-orchestrator/openapi.yaml
  - docs/data/api-contracts/model-policy/openapi.yaml
  - docs/data/api-contracts/federation/openapi.yaml
- Validates canonical example JSON payloads under docs/data/api-contracts/**/examples/ against component schemas.
- Resolves both internal and external `$ref` pointers (including federation refs back to agent-orchestrator schemas).

If any example no longer matches the contract, the run exits non-zero (CI will fail).
