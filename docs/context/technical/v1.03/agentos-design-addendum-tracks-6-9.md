# AgentOS v1.03 Design Addendum — Tracks 6-9

This addendum extends the v1.03 design document (`agentos-design.md`) with
architecture decisions for tracks 6-9. These tracks were implemented after the
initial v1.03 design was written. This document is **retroactive authority**.

---

## 8) Agent CRUD API

### 8.1 Routing

Agent CRUD shares the existing `/v1/agents/` route handler in `server.go`. The
router dispatches based on path depth and HTTP method:

```
/v1/agents/           GET    → handleAgentList (existing)
/v1/agents/           POST   → handleAgentCreate (new)
/v1/agents/{id}       GET    → handleAgentGet (existing)
/v1/agents/{id}       PUT    → handleAgentUpdate (new)
/v1/agents/{id}       DELETE → handleAgentDelete (new)
/v1/agents/{id}/runs  ...    → (existing run handlers)
```

This was chosen over separate route registrations because Go's `http.ServeMux`
prefix matching makes it natural to handle all `/v1/agents/` paths in one handler
with internal dispatch. Adding separate `HandleFunc` calls per method would
require careful prefix ordering.

### 8.2 AgentStore interface extension

The `AgentStore` interface gains one method:

```go
Delete(ctx context.Context, tenantID, agentID string) error
```

**Why not soft-delete?** We considered a `status: "deleted"` soft-delete but
rejected it for v1.03 because:
- No audit requirement to retain deleted agent metadata (audit log already captures the delete event)
- Soft-deletes complicate List queries (need filter) and storage growth
- Hard-delete is simpler and matches the current storage model
- Soft-delete can be added in a future version if needed

### 8.3 File backend: Delete

```go
func (s *fileAgentStore) Delete(ctx, tenantID, agentID string) error {
    // 1. Check existence in map → ErrAgentNotFound if missing
    // 2. Remove from in-memory map
    // 3. Remove JSON file: data/agents/{tenant_id}/{agent_id}.json
    //    (ignore os.ErrNotExist to handle partial state)
}
```

### 8.4 PostgreSQL backend: Delete

```sql
DELETE FROM agents WHERE tenant_id = $1 AND agent_id = $2
```

Returns `ErrAgentNotFound` if `RowsAffected() == 0`.

### 8.5 Version incrementing

On update, the version field is parsed as an integer, incremented, and stored
back as a string. This gives a monotonically increasing version counter that
clients can use for optimistic concurrency (though we don't enforce ETags in
v1.03).

**Why string, not int?** The `Agent.Version` field was defined as a string in
v1.02 for flexibility (e.g., semantic versions). We keep it as a string but
treat it as a numeric counter internally. If the existing value is not a valid
integer, `strconv.Atoi` returns 0, so the next version becomes `"1"`.

### 8.6 Validation regex

```go
var validAgentID = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,64}$`)
```

This is compiled once at package init time and reused for all requests.

---

## 9) Custom tool registration

### 9.1 Architecture decision: config-embedded vs. API-managed

Custom tools are stored in `AgentConfig.CustomTools` rather than managed via
separate CRUD endpoints. This was chosen because:

1. **Simplicity** — no new storage table, no new API surface, no tool lifecycle management
2. **Atomicity** — tools are versioned with the agent (agent update = tool update)
3. **Tenant scoping** — inherits agent's tenant isolation automatically
4. **Trade-off** — tools cannot be shared across agents; this is acceptable for v1.03

A dedicated tool registry API is deferred to v1.04+.

### 9.2 Per-run registry cloning

```go
func (r *Registry) RegisterCustomTools(customTools []CustomTool) (*Registry, error) {
    // 1. Clone: create new Registry, copy all built-in tools
    // 2. Check for name collisions between custom and built-in
    // 3. If collision: return error (caller falls back to global registry)
    // 4. Add WebhookTool for each custom tool
    // 5. Return cloned registry
}
```

**Why clone instead of mutate?** The global registry is shared across all runs
and goroutines. Mutating it would:
- Create data races without additional locking
- Allow one agent's custom tools to leak into another agent's run
- Require cleanup logic to remove tools after a run completes

Cloning is O(n) where n = number of tools (small, typically < 20) and creates
a clear ownership boundary: each run owns its registry clone.

### 9.3 Agent loop integration

In `executeRun()`, custom tools are registered before the execution loop:

```go
runToolRegistry := e.tools                     // default: global registry
if len(agent.Config.CustomTools) > 0 {
    cloned, err := e.tools.RegisterCustomTools(agent.Config.CustomTools)
    if err != nil {
        // Log error, fall back to global registry
    } else {
        runToolRegistry = cloned
    }
}
toolDefs := e.toolDefsForModelRegistry(runToolRegistry, agentTools)
```

The `runToolRegistry` is passed through to all tool execution calls within this
run, ensuring custom tools are available for model-invoked tool calls.

### 9.4 WebhookTool

```
internal/tools/webhook_tool.go
```

WebhookTool implements the `Tool` interface. On `Execute`:
1. Builds an SSRF-safe HTTP client with a custom `DialContext` that resolves DNS
   first and checks IPs against private ranges
2. Parses the input arguments as JSON (falls back to raw string)
3. POSTs `{"input": <arguments>}` to the webhook URL with configured headers
4. Reads up to 32KB of response body
5. Returns the response as a string

**SSRF protection** reuses the same `isPrivateIP()` function as `http_fetch.go`.
The `AllowLoopback` field exists for testing (allows `127.0.0.1` in test
environments).

---

## 10) Anthropic provider

### 10.1 Architecture decision: separate provider vs. adapter

We implemented `anthropicProvider` as a direct `provider` interface implementation
rather than an adapter that translates to OpenAI format and delegates to the
OpenAI provider. Reasons:

1. **Fidelity** — Anthropic's API has structural differences (system message handling,
   content blocks, tool_use/tool_result format) that would require complex
   bidirectional mapping if wrapped around the OpenAI provider
2. **Streaming** — Anthropic's SSE format is significantly different from OpenAI's
   (event types like `content_block_start`, `content_block_delta` vs. OpenAI's
   simpler delta format)
3. **Error model** — HTTP error codes and response bodies differ
4. **Maintenance** — a direct implementation is easier to reason about and debug

### 10.2 Message format translation

The most complex part of the provider is the request/response mapping. Key
decisions:

**System message extraction**: Anthropic requires the system message as a
top-level field, not in the messages array. The provider iterates messages once
and extracts any `role: "system"` message to `anthropicRequest.System`.

**Tool result mapping**: In the OpenAI format, tool results are messages with
`role: "tool"`. In Anthropic's format, they are `role: "user"` messages with
`type: "tool_result"` content blocks. The provider wraps each tool result in
a user message with a content block array.

**Assistant + tool_calls**: When an assistant message has tool calls, it must
be sent as content blocks (text block + tool_use blocks), not as a simple
string content field.

### 10.3 Provider factory integration

```go
func newProvider(cfg config.ModelConfig) (provider, error) {
    switch cfg.Provider {
    case "openai":
        p = newOpenAIProvider(cfg)
    case "anthropic":
        p = newAnthropicProvider(cfg)
    case "stub":
        return &stubProvider{}, nil
    ...
    }
    return NewResilientProvider(p, ...), nil
}
```

Both OpenAI and Anthropic providers are wrapped with the same resilient provider
(retry + circuit breaker). The stub provider is NOT wrapped (no network calls).

### 10.4 Streaming SSE parser

The Anthropic streaming parser runs in a goroutine that reads SSE lines from the
response body. It tracks state:

- **pendingToolUses**: a slice of in-progress tool_use blocks, each with an
  `argsBuilder` (strings.Builder) that accumulates partial JSON from
  `input_json_delta` events
- On `message_delta` with `stop_reason: "tool_use"`, all pending tool uses are
  emitted as a single `StreamChunk` with the complete `ToolCalls` array

This design handles Anthropic's multi-step streaming where tool call arguments
arrive as incremental JSON fragments.

### 10.5 Error mapping

```go
func (p *anthropicProvider) mapHTTPError(resp *http.Response) error {
    // Read up to 4KB of error body
    // Map status codes:
    //   429 → rate_limited (with Retry-After)
    //   400 → invalid_request
    //   5xx → provider_error
    // Return *ProviderError
}
```

The `ProviderError` type is shared with the OpenAI provider, ensuring consistent
error handling upstream.

---

## 11) Streaming token output

### 11.1 Architecture decision: type assertion vs. separate interface

The executor uses a runtime type assertion to check if the provider supports
streaming:

```go
streamProvider, canStream := e.provider.(ModelStreamProvider)
if useStream && canStream {
    resp, err = e.streamModelCall(job, streamProvider, ...)
} else {
    resp, err = e.provider.ChatComplete(...)
}
```

**Why type assertion?** The `ModelProvider` interface is the minimal contract
needed by the executor. Not all providers support streaming (e.g., a future
batch-only provider). Using a type assertion allows:
- Backward compatibility: existing providers that only implement `ModelProvider`
  continue to work
- Optional capability: streaming is opt-in at both the provider level (implements
  `ModelStreamProvider`) and the request level (`stream_events: true`)
- No forced implementation: providers are not required to implement streaming

### 11.2 Streaming model call flow

```go
func (e *Executor) streamModelCall(...) (*ChatResponse, error) {
    // 1. Call ChatCompleteStream → get channel
    // 2. Iterate channel:
    //    a. chunk.Err → return error
    //    b. chunk.Delta → emit step.token event, accumulate text
    //    c. chunk.ToolCalls → accumulate
    //    d. chunk.FinishReason → record
    //    e. chunk.Usage → record
    // 3. Build synthetic ChatResponse from accumulated data
    // 4. Return response (caller continues normal loop)
}
```

The synthetic `ChatResponse` returned by `streamModelCall` is identical in
structure to what `ChatComplete` returns. This means the rest of the agent loop
(tool execution, completion handling, memory persistence) does not need to know
whether streaming was used.

### 11.3 Event emission

`step.token` events are emitted synchronously during stream iteration. This
means tokens flow to the event sink in real-time as they arrive from the
provider. The `EventSink` is goroutine-safe and buffers events for later
retrieval by the SSE endpoint.

### 11.4 Tool call handling during streaming

Tool calls arrive as fragments during streaming:
1. `content_block_start` → new tool_use block (Anthropic) or initial delta (OpenAI)
2. `content_block_delta` → partial arguments JSON
3. Final chunk → complete tool calls

The `streamModelCall` function accumulates all tool calls and emits them as
`step.tool_call` events after the stream ends. This is simpler than emitting
partial tool call events during streaming and matches the non-streaming behavior.

### 11.5 Graceful degradation

If `stream_events: true` but the provider does not implement `ModelStreamProvider`
(e.g., stub provider in tests), the executor silently falls back to buffered
`ChatComplete`. No error is raised. This ensures that switching providers does
not break existing run configurations.

---

## 12) Configuration extensions (tracks 6-9)

### 12.1 Types added

```go
// internal/types/stacka.go

type AgentCreateRequest struct {
    AgentID     string       `json:"agent_id"`
    Name        string       `json:"name"`
    Description string       `json:"description,omitempty"`
    Config      *AgentConfig `json:"config,omitempty"`
}

type AgentCreateResponse struct {
    Agent         Agent  `json:"agent"`
    CorrelationID string `json:"correlation_id"`
}

type AgentUpdateRequest struct {
    Name        string       `json:"name"`
    Description string       `json:"description,omitempty"`
    Config      *AgentConfig `json:"config,omitempty"`
}
```

```go
// internal/types/tenant.go

type CustomTool struct {
    Name        string            `json:"name"`
    Description string            `json:"description"`
    InputSchema map[string]any    `json:"input_schema,omitempty"`
    WebhookURL  string            `json:"webhook_url"`
    Headers     map[string]string `json:"headers,omitempty"`
    TimeoutMs   int               `json:"timeout_ms,omitempty"`
}

// Added to AgentConfig:
CustomTools []CustomTool `json:"custom_tools,omitempty"`
```

### 12.2 No new environment variables

Tracks 6-9 do not introduce new environment variables. They reuse:
- `AGENTOS_MODEL_PROVIDER` (extended to accept `"anthropic"`)
- `AGENTOS_MODEL_BASE_URL`, `AGENTOS_MODEL_API_KEY`, `AGENTOS_MODEL_DEFAULT` (reused for Anthropic)
- `AGENTOS_TOOL_TIMEOUT` (used by WebhookTool default timeout)

---

## 13) Testing strategy (tracks 6-9)

### 13.1 Agent CRUD tests

Agent CRUD tests run against the `Server` test harness with an in-memory file
agent store. Tests cover the full HTTP lifecycle: create, read, update, delete,
conflict detection, and tenant isolation.

### 13.2 Custom tool tests

WebhookTool tests use `httptest.NewServer` as a mock webhook target. SSRF tests
verify that connections to private IPs are rejected. Registry clone tests verify
isolation between the global and per-run registries.

### 13.3 Anthropic provider tests

Seven tests cover the Anthropic provider using `httptest.NewServer`:
1. Basic chat completion (text response)
2. Tool use response mapping
3. Tool result request mapping
4. Rate limiting (429 + Retry-After)
5. Server error (500)
6. Streaming with content_block_delta events
7. System message extraction

### 13.4 Streaming tests

Streaming tests use a mock provider that implements `ModelStreamProvider` and
returns chunks via a channel. Tests verify that `step.token` events are emitted,
tool calls are accumulated, and errors are handled correctly.
