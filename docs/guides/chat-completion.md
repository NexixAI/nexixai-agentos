# Chat Completion — MCP + HTTP

**Since**: v2.0 (text-only); v11.0 (MCP multipart); **v11.1 (HTTP multipart parity)**

## Transports

AgentOS exposes chat completion through **two transports** with identical
multipart semantics:

| Transport | Endpoint | Authz | Since |
|-----------|----------|-------|-------|
| **MCP tool** | `chat_completion` | Tier-based: `ClearanceInternal` (T1) text, `ClearanceExecute` (T2) multimodal | v11.0 |
| **HTTP / OpenAI-compat** | `POST /v1/chat/completions` | OIDC scopes: multimodal requires `chat:multimodal` | v11.1 |

Both transports share the same validator, audit emitter, metrics, and
classifier-bypass behavior (`internal/multimodal/`). The only difference
is the authz model each transport uses natively.

## Summary

Send a chat-completion request through the AgentOS routing pipeline. For
text-only callers, AgentOS applies classifier-based routing (intent
detection + `enable_thinking` injection). For multimodal callers
(messages containing images), AgentOS bypasses the classifier and routes
directly to the vision-capable model while preserving the multipart
envelope end-to-end.

The tool / endpoint is a raw upstream-model passthrough: callers must
validate returned content before using it to drive tools or actuators.

## MCP Tool Metadata

| Field | Value |
|-------|-------|
| Name | `chat_completion` |
| Min clearance | `ClearanceInternal` (T1) for all calls |
| Min clearance for multimodal | `ClearanceExecute` (T2) runtime check |
| Trust contract | Returns raw upstream output; validate before driving actuators |

## HTTP Endpoint Metadata (v11.1)

| Field | Value |
|-------|-------|
| Path | `POST /v1/chat/completions` |
| Auth | OIDC-validated tenant + scopes |
| Scope for multimodal | `chat:multimodal` (required when any message has `image_url` parts) |
| Providers supporting multimodal | OpenAI-compatible (SGLang, vLLM). **Anthropic rejected** with 400 `unsupported_for_provider` in v11.1 |
| Env flag | `AGENTOS_CHAT_AUDIT_IMAGE_HASH=1` — log SHA-256 refs instead of URLs |

### Default model resolution (v11.2)

When a caller omits `"model"` in the request body, agentos supplies a default. Precedence at startup:

1. **`AGENTOS_MODEL_DEFAULT` env** — if set and non-empty, wins. Use this to pin a specific model in a deploy env.
2. **Backend `/v1/models` query** — for `AGENTOS_MODEL_PROVIDER=openai`, agentos queries `${AGENTOS_MODEL_BASE_URL}/models` at startup and uses the first id from the response. Retried with backoff for up to 30s if the backend is warming up.
3. **Fail-fast** — if neither source produces an answer, agentos exits non-zero (supervisor restarts).

The resolved choice is logged once at startup: `model default resolved source=env|backend model_id=<id>`. Anthropic provider keeps its own internal default and is unaffected by the backend query path.

## Input Schema (v11.0)

```json
{
  "model": "Qwen/Qwen3.6-35B-A3B",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "..."}
  ],
  "max_tokens": 256,
  "temperature": 0.2
}
```

### `messages[].content` — string or multipart array

**Text form (unchanged since v2.0):**
```json
{"role": "user", "content": "Explain the quicksort algorithm."}
```

**Multipart form (new in v11.0):**
```json
{
  "role": "user",
  "content": [
    {"type": "text", "text": "What does this screenshot show?"},
    {"type": "image_url", "image_url": {"url": "https://example.com/screenshot.png"}}
  ]
}
```

### Content part types

| Type | Fields | Notes |
|------|--------|-------|
| `text` | `text: string` | Arbitrary text. Multiple `text` parts in a message are concatenated by space for classifier extraction. |
| `image_url` | `image_url.url: string`, optional `image_url.detail: "auto"\|"low"\|"high"` | Fetched by the upstream inference backend, not by AgentOS. |

## Validation (v11.0)

AgentOS rejects a request with a structured error if:

- `image_url.url` uses `file://`, `data:`, or any non-`http(s)` scheme
- `image_url.url` is empty or the `image_url` struct is nil
- `type` field is missing or not in `{"text", "image_url"}`
- The message exceeds **8 image parts** (`chatMaxImageParts` in `mcp/tools/chat.go`)

The backend (SGLang) is responsible for actually fetching the URL and any network-level policy (private-range blocking, size limits). AgentOS only forwards URL strings.

## Governance

Multimodal requests require **ClearanceExecute (T2)** at runtime. The tool-level `MinClearance` remains `ClearanceInternal` (T1) so that text-only callers are unaffected.

This is a runtime clearance check layered on top of the standard AuthMiddleware — text-only calls pass at T1, multimodal calls need T2. See `chatMultimodalMinClearance` in `mcp/tools/chat.go`.

## Routing Behavior

| Content | Classifier | Intent | Notes |
|---------|-----------|--------|-------|
| Text-only | Called | As determined by classifier | `agentos_intent_classifications_total` increments |
| Multimodal | **Bypassed** | `general` (synthetic decision) | Classifier metric NOT incremented; `agentos_chat_messages_total{content_type="multimodal"}` increments |
| Multipart (text-only parts) | Called | As determined | Behaves like text |

## Metrics (v11.0)

| Metric | Labels | Semantics |
|--------|--------|-----------|
| `agentos_chat_messages_total` | `tenant_id`, `content_type` | Incremented per call. `content_type ∈ {text, multimodal}`. |
| `agentos_chat_image_parts_total` | `tenant_id` | Counter of image_url parts submitted (summed over all messages in the call). |

## Audit (v11.0)

Multimodal requests emit an audit entry when `ChatToolsConfig.AuditLogger` is wired:

```json
{
  "time": "2026-04-22T02:15:00Z",
  "tenant_id": "tenant-a",
  "action": "chat.multimodal",
  "resource": "tool:chat_completion",
  "outcome": "accepted",
  "meta": {
    "image_parts": 2,
    "image_refs": ["https://example.com/a.png", "https://example.com/b.png"],
    "image_hash": false
  }
}
```

Set `ChatToolsConfig.ChatAuditImageHash = true` to log `sha256:<hex>` of each URL instead of the URL itself (for deploys where URLs may carry tokens or PII).

Text-only calls emit no audit entry from this path.

## Examples

### Text-only (backward compatible)

```bash
curl https://agentos.example.com/mcp/tools/chat_completion \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3.6-35B-A3B",
    "messages": [{"role":"user","content":"Say hi."}],
    "max_tokens": 32
  }'
```

### Multimodal (MCP tool)

```bash
curl https://agentos.example.com/mcp/tools/chat_completion \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3.6-35B-A3B",
    "messages": [{
      "role":"user",
      "content":[
        {"type":"text","text":"What is happening in this screenshot?"},
        {"type":"image_url","image_url":{"url":"https://grafana.internal/panel.png"}}
      ]
    }],
    "max_tokens": 200
  }'
```

### Multimodal (HTTP `/v1/chat/completions`, v11.1)

Same payload, different endpoint. Caller must be authenticated with a
token that carries the `chat:multimodal` scope (lack thereof → 403
`scope_missing`).

```bash
curl https://agentos.example.com/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN_WITH_CHAT_MULTIMODAL" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3.6-35B-A3B",
    "messages": [{
      "role":"user",
      "content":[
        {"type":"text","text":"What animal is this?"},
        {"type":"image_url","image_url":{"url":"https://example.com/pet.png"}}
      ]
    }],
    "max_tokens": 120
  }'
```

### Rejection example (bad URL scheme)

```bash
# Request body with file:// URL is rejected 400 at AgentOS, never reaches upstream.
curl -d '{"messages":[{"role":"user","content":[
  {"type":"image_url","image_url":{"url":"file:///etc/passwd"}}
]}]}' ...
# → error: "messages[0]: content part 0: file:// URLs are rejected for safety; use a hosted http(s) URL"
```

## Limitations

- **No binary upload**: images must be hosted at an http(s) URL. Base64 `data:` URLs are explicitly rejected in v11.0 (planned for v11.1).
- **No audio/video parts**: rejected with structured error.
- **Per-message image cap**: 8 parts (constant `chatMaxImageParts`). Cap is per-message, not per-request — a request with multiple messages may contain more images in aggregate.
- **SSRF risk**: image URLs are fetched by the upstream backend from inside the cluster network. Any caller with T2 clearance can trigger fetches of internal URLs (e.g., Grafana, internal metrics). Scope the T2 grant accordingly.

## Related

- Upstream inference contract: `nexixai-infra/docs/intent/v5.0/prs.md` (Qwen3.6-35B-A3B deploy).
- Downstream agents (delegator + SRE vision wiring): `nexixai-agents/docs/intent/v9.3/prs.md` (to be drafted post-v11.0).
- AgentOS v11.0 PRS: `docs/intent/v11.0/prs.md`.
