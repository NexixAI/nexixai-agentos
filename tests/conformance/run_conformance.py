#!/usr/bin/env python3
"""
AgentOS Conformance Runner

Validates canonical JSON examples under docs/api/**/examples/*.json against the OpenAPI component schemas.

Design goals:
- Zero interpretation drift: examples MUST conform to OpenAPI schemas.
- Works without codegen: resolves internal and external $ref pointers.
- Fast + deterministic: meant to run in CI on every PR.
"""
from __future__ import annotations

import json
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, Tuple, Optional

import yaml  # PyYAML
from jsonschema import Draft7Validator


REPO_ROOT = Path(__file__).resolve().parents[2]  # tests/conformance/ -> repo
DOCS_API = REPO_ROOT / "docs" / "api"

OPENAPI_FILES = {
    "agent-orchestrator": DOCS_API / "agent-orchestrator" / "openapi.yaml",
    "model-policy": DOCS_API / "model-policy" / "openapi.yaml",
    "federation": DOCS_API / "federation" / "openapi.yaml",
}

# Map example files to (openapi_key, component_schema_name)
EXAMPLE_MAP = {
    # Agent Orchestrator
    "docs/api/agent-orchestrator/examples/runs-create.request.json": ("agent-orchestrator", "RunCreateRequest"),
    "docs/api/agent-orchestrator/examples/runs-create.response.json": ("agent-orchestrator", "RunCreateResponse"),
    "docs/api/agent-orchestrator/examples/event-envelope.json": ("agent-orchestrator", "EventEnvelope"),

    # Model Policy
    "docs/api/model-policy/examples/chat.request.json": ("model-policy", "ModelInvokeRequest"),
    "docs/api/model-policy/examples/chat.response.json": ("model-policy", "ModelInvokeResponse"),
    "docs/api/model-policy/examples/policy-check.request.json": ("model-policy", "PolicyCheckRequest"),
    "docs/api/model-policy/examples/policy-check.response.json": ("model-policy", "PolicyCheckResponse"),

    # Federation
    "docs/api/federation/examples/peer-info.response.json": ("federation", "PeerInfoResponse"),
    "docs/api/federation/examples/peer-capabilities.response.json": ("federation", "PeerCapabilitiesResponse"),
    "docs/api/federation/examples/runs-forward.request.json": ("federation", "FederationForwardRunRequest"),
    "docs/api/federation/examples/runs-forward.response.json": ("federation", "FederationForwardRunResponse"),
}


class ConformanceError(Exception):
    pass


@dataclass(frozen=True)
class RefTarget:
    file: Path
    pointer: str  # JSON pointer like "/components/schemas/Foo"


def _json_pointer_get(doc: Dict[str, Any], pointer: str) -> Any:
    if not pointer.startswith("/"):
        raise ConformanceError(f"Invalid JSON pointer (expected '/...'): {pointer}")
    cur: Any = doc
    for part in pointer.lstrip("/").split("/"):
        part = part.replace("~1", "/").replace("~0", "~")
        if isinstance(cur, dict) and part in cur:
            cur = cur[part]
        else:
            raise ConformanceError(f"Pointer not found: {pointer} (missing '{part}')")
    return cur


def _parse_ref(ref: str, base_file: Path) -> RefTarget:
    # internal: "#/components/schemas/Foo"
    # external: "../agent-orchestrator/openapi.yaml#/components/schemas/Foo"
    if "#" not in ref:
        raise ConformanceError(f"Unsupported $ref without '#': {ref}")
    file_part, frag = ref.split("#", 1)
    target_file = (base_file.parent / file_part).resolve() if file_part else base_file.resolve()
    pointer = frag
    if pointer.startswith("/"):
        ptr = pointer
    elif pointer.startswith("/") is False and pointer.startswith("/") is False:
        # If frag is like "/components/..." already that's handled; otherwise must start with "/"
        ptr = pointer
    if not ptr.startswith("/"):
        # OpenAPI refs usually start with "/"; allow "#/..." form (already split => frag starts with "/")
        ptr = "/" + ptr.lstrip("/")
    return RefTarget(file=target_file, pointer=ptr)


def _load_openapi(path: Path) -> Dict[str, Any]:
    if not path.exists():
        raise ConformanceError(f"OpenAPI file missing: {path}")
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict) or "openapi" not in data or "components" not in data:
        raise ConformanceError(f"Invalid OpenAPI structure in {path}")
    return data


def _deref_schema(schema: Any, base_file: Path, cache: Dict[Tuple[Path, str], Any], openapi_cache: Dict[Path, Dict[str, Any]]) -> Any:
    """
    Recursively replace $ref with the referenced schema dict.
    This produces a self-contained JSON Schema for jsonschema validation.
    """
    if isinstance(schema, dict) and "$ref" in schema:
        ref = schema["$ref"]
        tgt = _parse_ref(ref, base_file)
        key = (tgt.file, tgt.pointer)
        if key in cache:
            return cache[key]
        if tgt.file not in openapi_cache:
            openapi_cache[tgt.file] = _load_openapi(tgt.file)
        doc = openapi_cache[tgt.file]
        resolved = _json_pointer_get(doc, tgt.pointer)
        # Place a temporary placeholder in cache to prevent recursion loops
        cache[key] = {}  # will be overwritten below
        deref_resolved = _deref_schema(resolved, tgt.file, cache, openapi_cache)
        cache[key] = deref_resolved
        return deref_resolved

    if isinstance(schema, dict):
        out = {}
        for k, v in schema.items():
            out[k] = _deref_schema(v, base_file, cache, openapi_cache)

        # OpenAPI 3.0 `nullable: true` is not standard JSON Schema; translate it so jsonschema can validate.
        if out.get("nullable") is True:
            out.pop("nullable", None)
            return {"anyOf": [out, {"type": "null"}]}

        return out
    if isinstance(schema, list):
        return [_deref_schema(x, base_file, cache, openapi_cache) for x in schema]
    return schema


def _get_component_schema(openapi: Dict[str, Any], name: str) -> Any:
    comps = openapi.get("components", {})
    schemas = comps.get("schemas", {})
    if name not in schemas:
        raise ConformanceError(f"Schema not found in components.schemas: {name}")
    return schemas[name]


def _validate_instance(instance: Any, schema: Any, context: str) -> None:
    # jsonschema expects boolean schemas at times; keep it simple
    validator = Draft7Validator(schema)
    errors = sorted(validator.iter_errors(instance), key=lambda e: e.path)
    if errors:
        lines = [f"Schema validation failed for {context}:"]
        for e in errors[:20]:
            path = "/" + "/".join(str(p) for p in e.path) if e.path else "(root)"
            lines.append(f" - {path}: {e.message}")
        if len(errors) > 20:
            lines.append(f" - ... and {len(errors)-20} more")
        raise ConformanceError("\n".join(lines))


def main() -> int:
    # Load OpenAPI specs
    openapis: Dict[str, Dict[str, Any]] = {}
    for key, path in OPENAPI_FILES.items():
        openapis[key] = _load_openapi(path)

    # Validate every mapped example
    cache: Dict[Tuple[Path, str], Any] = {}
    openapi_cache: Dict[Path, Dict[str, Any]] = {OPENAPI_FILES[k].resolve(): v for k, v in openapis.items()}

    failures = []
    for rel, (api_key, schema_name) in EXAMPLE_MAP.items():
        example_path = REPO_ROOT / rel
        if not example_path.exists():
            failures.append(f"Missing example file: {rel}")
            continue

        try:
            instance = json.loads(example_path.read_text(encoding="utf-8"))
        except Exception as e:
            failures.append(f"Invalid JSON in {rel}: {e}")
            continue

        base_file = OPENAPI_FILES[api_key].resolve()
        schema = _get_component_schema(openapis[api_key], schema_name)
        deref = _deref_schema(schema, base_file, cache, openapi_cache)

        try:
            _validate_instance(instance, deref, f"{rel} vs {api_key}:{schema_name}")
        except Exception as e:
            failures.append(str(e))

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1

    print("Conformance OK: all examples validate against OpenAPI component schemas.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
