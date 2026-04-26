# AgentOS v1.03 Design — Real Execution

This document defines the **architecture decisions** for v1.03 real execution capabilities. It inherits the v1.02 and v1.025 designs unchanged and specifies how stubs are replaced with functional implementations.

---

## 1) Model provider architecture

### 1.1 Provider interface (extended)

The existing `provider` interface is extended to support streaming and chat completions:

```go
// modelpolicy/providers.go

type provider interface {
	// Invoke sends a non-streaming request and returns output + usage.
	// Retained for backward compatibility with stub provider.
	Invoke(req types.ModelInvokeRequest) (map[string]any, map[string]any, error)

	// ChatComplete sends a chat completions request (OpenAI format).
	ChatComplete(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// ChatCompleteStream sends a streaming chat completions request.
	// Tokens are delivered via the returned channel. Channel closes on completion.
	ChatCompleteStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
}
```

### 1.2 Chat request/response types

```go
// modelpolicy/chat.go (new)

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []ToolDef     `json:"tools,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type ChatMessage struct {
	Role       string     `json:"role"`       // system, user, assistant, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type ChatResponse struct {
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
	Model   string       `json:"model"`
}

type ChatChoice struct {
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"` // stop, tool_calls, length
}

type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type StreamChunk struct {
	Delta        string  `json:"delta,omitempty"`      // text content delta
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"` // partial tool calls
	FinishReason string  `json:"finish_reason,omitempty"`
	Usage        *ChatUsage `json:"usage,omitempty"`    // only in final chunk
	Err          error   `json:"-"`                     // stream error
}
```

### 1.3 OpenAI-compatible provider

```go
// modelpolicy/openai_provider.go (new)

type openaiProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	timeout    time.Duration
}

func newOpenAIProvider(cfg config.ModelConfig) *openaiProvider {
	return &openaiProvider{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		timeout: cfg.Timeout,
	}
}
```

The provider translates between AgentOS types and the OpenAI API format. It POSTs to `{baseURL}/chat/completions`.

### 1.4 Provider selection

```go
// modelpolicy/providers.go (updated)

func newProvider(cfg config.ModelConfig) (provider, error) {
	switch cfg.Provider {
	case "openai":
		return newOpenAIProvider(cfg), nil
	case "stub", "":
		return &stubProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown model provider: %s", cfg.Provider)
	}
}
```

---

## 2) Agent execution engine

### 2.1 Execution architecture

```
agentorchestrator/
  ├── server.go          (update — remove timer stubs, dispatch to executor)
  ├── executor.go        (new — worker pool + run executor)
  ├── agent_loop.go      (new — prompt → model → tool → observe loop)
  ├── prompt.go          (new — prompt construction from agent config + memory)
  └── events.go          (update — real event emission)
```

### 2.2 Worker pool

```go
// agentorchestrator/executor.go (new)

type Executor struct {
	workers    int
	queue      chan *runJob
	provider   modelpolicy.Client  // HTTP client to model policy service
	tools      *tools.Registry
	memory     storage.MemoryStore
	runs       storage.RunStore
	audit      audit.Logger
	wg         sync.WaitGroup
}

type runJob struct {
	run     types.Run
	agent   types.Agent
	ctx     context.Context
	cancel  context.CancelFunc
	events  *EventSink
}

func NewExecutor(cfg config.ExecConfig, ...) *Executor {
	e := &Executor{
		workers: cfg.Workers,
		queue:   make(chan *runJob, cfg.Workers*2),
		...
	}
	for i := 0; i < e.workers; i++ {
		e.wg.Add(1)
		go e.worker()
	}
	return e
}

func (e *Executor) Submit(job *runJob) {
	e.queue <- job
}

func (e *Executor) Shutdown(ctx context.Context) {
	close(e.queue)
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
```

### 2.3 Agent loop

```go
// agentorchestrator/agent_loop.go (new)

func (e *Executor) executeRun(job *runJob) {
	// 1. Load conversation memory
	history := e.memory.GetRecent(job.ctx, job.run.TenantID, job.run.AgentID)

	// 2. Build initial messages
	messages := buildPrompt(job.agent, job.run, history)

	// 3. Get tool definitions for this agent
	toolDefs := e.tools.GetToolDefs(job.agent.Config.Tools)

	step := 0
	for step < job.run.RunOptions.MaxSteps {
		step++

		// 4. Invoke model
		resp, err := e.provider.ChatComplete(job.ctx, ChatRequest{
			Model:    job.agent.Config.ModelID,
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			job.run.Status = "failed"
			job.run.Error = &types.RunError{Code: "provider_error", Message: err.Error()}
			return
		}

		choice := resp.Choices[0]

		// 5. Record token usage
		e.recordUsage(job.run.TenantID, resp.Usage)

		// 6. Check for tool calls
		if choice.FinishReason == "tool_calls" && len(choice.Message.ToolCalls) > 0 {
			messages = append(messages, choice.Message)
			for _, tc := range choice.Message.ToolCalls {
				result := e.tools.Execute(job.ctx, tc)
				messages = append(messages, ChatMessage{
					Role:       "tool",
					Content:    result,
					ToolCallID: tc.ID,
				})
			}
			continue
		}

		// 7. Final response — done
		job.run.Status = "completed"
		job.run.Output = &types.RunOutput{Type: "text", Text: choice.Message.Content}
		break
	}

	// 8. Save conversation to memory
	e.memory.Append(job.ctx, job.run.TenantID, job.run.AgentID, messages)
}
```

### 2.4 Prompt construction

```go
// agentorchestrator/prompt.go (new)

func buildPrompt(agent types.Agent, run types.Run, history []ChatMessage) []ChatMessage {
	var messages []ChatMessage

	// System prompt from agent config
	if agent.Config.SystemPrompt != "" {
		messages = append(messages, ChatMessage{
			Role:    "system",
			Content: agent.Config.SystemPrompt,
		})
	}

	// Conversation history (from memory)
	messages = append(messages, history...)

	// Current run input
	messages = append(messages, ChatMessage{
		Role:    "user",
		Content: run.Input.Text,
	})

	return messages
}
```

---

## 3) Tool execution framework

### 3.1 Package layout

```
internal/tools/
  ├── registry.go       (new — tool registry + dispatch)
  ├── types.go          (new — Tool interface, ToolResult)
  ├── http_fetch.go     (new — HTTP fetch tool)
  ├── json_extract.go   (new — JSONPath extraction tool)
  ├── text_summarize.go (new — text truncation tool)
  └── tools_test.go     (new — tests for all tools)
```

### 3.2 Tool interface

```go
// internal/tools/types.go

type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any  // JSON Schema
	Execute(ctx context.Context, input string) (string, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	// Register built-in tools
	r.Register(&HTTPFetchTool{})
	r.Register(&JSONExtractTool{})
	r.Register(&TextSummarizeTool{})
	return r
}

func (r *Registry) Execute(ctx context.Context, tc ToolCall) string {
	tool, ok := r.tools[tc.Function.Name]
	if !ok {
		return fmt.Sprintf(`{"error": "unknown_tool", "tool": %q}`, tc.Function.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, tc.Function.Arguments)
	if err != nil {
		return fmt.Sprintf(`{"error": "tool_error", "message": %q}`, err.Error())
	}
	return result
}
```

### 3.3 HTTP fetch tool (SSRF protection)

```go
// internal/tools/http_fetch.go

// SSRF protection: reject private IP ranges
var privateRanges = []net.IPNet{
	{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
	{IP: net.IPv4(172, 16, 0, 0), Mask: net.CIDRMask(12, 32)},
	{IP: net.IPv4(192, 168, 0, 0), Mask: net.CIDRMask(16, 32)},
	{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
	{IP: net.IPv4(169, 254, 0, 0), Mask: net.CIDRMask(16, 32)},
}

func isPrivateIP(ip net.IP) bool {
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}
```

The HTTP client uses a custom `DialContext` that resolves DNS first, checks the IP against private ranges, and rejects if private.

---

## 4) Memory / KV store

### 4.1 Package layout

```
internal/storage/
  ├── memory_store.go      (new — MemoryStore interface)
  ├── kv_store.go          (new — KVStore interface)
  ├── file_memory_store.go (new — file backend)
  ├── file_kv_store.go     (new — file backend)
  ├── postgres/
  │   ├── memory_store.go  (new — PostgreSQL backend)
  │   ├── kv_store.go      (new — PostgreSQL backend)
  │   └── migrations.go    (update — add memory + kv tables)
  └── factory.go           (update — add memory + kv factory functions)
```

### 4.2 Interfaces

```go
// internal/storage/memory_store.go

type MemoryStore interface {
	// Append adds messages to conversation memory for an agent.
	Append(ctx context.Context, tenantID, agentID string, messages []ChatMessage) error

	// GetRecent returns the most recent messages within the configured window.
	GetRecent(ctx context.Context, tenantID, agentID string, maxMessages int, maxTokens int) ([]ChatMessage, error)

	// Clear removes all conversation memory for an agent.
	Clear(ctx context.Context, tenantID, agentID string) error

	Close() error
}
```

```go
// internal/storage/kv_store.go

type KVStore interface {
	Get(ctx context.Context, tenantID, agentID, key string) (string, bool, error)
	Set(ctx context.Context, tenantID, agentID, key, value string) error
	Delete(ctx context.Context, tenantID, agentID, key string) error
	ListKeys(ctx context.Context, tenantID, agentID string) ([]string, error)
	Close() error
}
```

### 4.3 Schema (PostgreSQL)

```sql
CREATE TABLE IF NOT EXISTS conversation_memory (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    tool_call_id TEXT,
    tool_calls   JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_tenant_agent
    ON conversation_memory (tenant_id, agent_id, created_at DESC);

CREATE TABLE IF NOT EXISTS kv_store (
    tenant_id    TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    key          TEXT NOT NULL,
    value        TEXT NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, agent_id, key)
);
```

### 4.4 Memory window strategy

When loading conversation history, the memory store:
1. Queries the last `max_messages` rows for `(tenant_id, agent_id)` ordered by `created_at DESC`
2. Estimates token count using simple heuristic (chars / 4)
3. Trims oldest messages until total tokens <= `max_tokens`
4. Returns messages in chronological order (reversed from query)

This is a simple sliding window. Summary-based compression is deferred to a future version.

---

## 5) PII detection / redaction

### 5.1 Package layout

```
internal/pii/
  ├── detector.go      (new — pattern matching engine)
  ├── patterns.go      (new — built-in pattern definitions)
  ├── redactor.go      (new — redaction logic)
  ├── middleware.go     (new — HTTP middleware for pre/post model)
  └── pii_test.go      (new — tests)
```

### 5.2 Detector

```go
// internal/pii/detector.go

type Detection struct {
	Pattern  string `json:"pattern"`   // e.g., "email", "phone"
	Match    string `json:"match"`     // the matched text
	Start    int    `json:"start"`     // byte offset
	End      int    `json:"end"`       // byte offset
}

type Detector struct {
	patterns map[string]*regexp.Regexp
}

func NewDetector(enabled []string) *Detector {
	// Load only the requested patterns
}

func (d *Detector) Scan(text string) []Detection {
	// Run all enabled patterns against text
	// Return all matches sorted by position
}
```

### 5.3 Redactor

```go
// internal/pii/redactor.go

func Redact(text string, detections []Detection) string {
	// Replace each detection with its placeholder, working from end to start
	// to preserve byte offsets
}
```

### 5.4 Integration with execution loop

PII detection hooks into the agent execution loop at two points:

1. **Pre-model** (in `agent_loop.go`): Before each model invocation, scan the user message through the PII detector. Apply the tenant's PII policy (`block`/`redact`/`warn`).

2. **Post-model** (in `agent_loop.go`): After each model response, scan the output. Always `warn` mode (log detection but don't modify output).

This is implemented as a function call in the execution loop, NOT as HTTP middleware, because it needs access to tenant policy and must operate on individual messages, not HTTP request bodies.

---

## 6) Configuration extensions

### 6.1 Agent config (extended types)

```go
// internal/types/tenant.go (update)

type AgentConfig struct {
	SystemPrompt string   `json:"system_prompt,omitempty"`
	ModelID      string   `json:"model_id,omitempty"`
	Tools        []string `json:"tools,omitempty"`       // tool names this agent can use
	MaxSteps     int      `json:"max_steps,omitempty"`   // override default
	TimeoutMs    int      `json:"timeout_ms,omitempty"`  // override default
}
```

The `Agent` type's existing `Config map[string]any` field is replaced with a typed `AgentConfig` struct.

### 6.2 Tenant policy (extended)

```go
// internal/types/tenant.go (update)

type TenantPolicy struct {
	AllowedModels []string     `json:"allowed_models,omitempty"`
	DeniedModels  []string     `json:"denied_models,omitempty"`
	TokenBudget   *TokenBudget `json:"token_budget,omitempty"`
	PIIPolicy     *PIIPolicy   `json:"pii_policy,omitempty"`
}

type PIIPolicy struct {
	Mode     string   `json:"mode"`      // "block", "redact", "warn"
	Patterns []string `json:"patterns"`  // which patterns to enable
}
```

---

## 7) Testing strategy

### 7.1 Mock provider for tests

A mock HTTP server that implements the OpenAI chat completions API, returning configurable responses. This is used for:
- Unit tests of the agent execution loop
- Integration tests of the full run lifecycle
- CI tests (no real LLM dependency)

```go
// modelpolicy/mock_provider_test.go

func newMockOpenAIServer(t *testing.T, responses ...ChatResponse) *httptest.Server {
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := responses[i%len(responses)]
		i++
		json.NewEncoder(w).Encode(resp)
	}))
}
```

### 7.2 Test matrix

| Component | Test type | Backend | CI |
|-----------|-----------|---------|-----|
| OpenAI provider | Unit (mock server) | — | Always |
| Agent execution loop | Unit (mock provider + tools) | — | Always |
| Tool framework | Unit | — | Always |
| Memory store | Unit + integration | file + postgres | Always |
| KV store | Unit + integration | file + postgres | Always |
| PII detector | Unit | — | Always |
| End-to-end | Integration (mock provider) | postgres | Always |
