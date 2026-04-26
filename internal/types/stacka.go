package types

// Core API request/response types. Guided by the Schemas Appendix + OpenAPI.

type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
}

type RunCreateRequest struct {
	Input          RunInput   `json:"input"`
	Context        RunContext `json:"context"`
	Tooling        Tooling    `json:"tooling"`
	RunOptions     RunOptions `json:"run_options"`
	IdempotencyKey string     `json:"idempotency_key"`
}

type RunCreateResponse struct {
	Run           Run    `json:"run"`
	CorrelationID string `json:"correlation_id"`
}

type RunGetResponse struct {
	Run           Run    `json:"run"`
	CorrelationID string `json:"correlation_id"`
}

type RunCancelResponse struct {
	Run           Run    `json:"run"`
	CorrelationID string `json:"correlation_id"`
}

type RunListResponse struct {
	Runs          []Run  `json:"runs"`
	HasMore       bool   `json:"has_more"`
	CorrelationID string `json:"correlation_id"`
}

type Run struct {
	TenantID       string     `json:"tenant_id"`
	AgentID        string     `json:"agent_id"`
	RunID          string     `json:"run_id"`
	Status         string     `json:"status"`
	CreatedAt      string     `json:"created_at"`
	StartedAt      string     `json:"started_at,omitempty"`
	CompletedAt    string     `json:"completed_at,omitempty"`
	EventsURL      string     `json:"events_url"`
	Input          RunInput   `json:"input,omitempty"`
	RunOptions     RunOptions `json:"run_options,omitempty"`
	Output         *RunOutput `json:"output,omitempty"`
	Error          *RunError  `json:"error,omitempty"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
	RetryOf        string     `json:"retry_of,omitempty"`
	ParentRunID    string     `json:"parent_run_id,omitempty"`
}

type RunInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type RunContext struct {
	Locale    string         `json:"locale"`
	Timezone  string         `json:"timezone"`
	Channel   string         `json:"channel"`
	UserID    string         `json:"user_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Tooling struct {
	Tools []ToolDescriptor `json:"tools"`
}

type ToolDescriptor struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type RunOptions struct {
	Priority     string `json:"priority"`
	TimeoutMs    int    `json:"timeout_ms"`
	MaxSteps     int    `json:"max_steps"`
	StreamEvents bool   `json:"stream_events,omitempty"`
}

type RunOutput struct {
	Type      string           `json:"type,omitempty"`
	Text      string           `json:"text,omitempty"`
	Artifacts []map[string]any `json:"artifacts,omitempty"`
}

type RunError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type TraceContext struct {
	Traceparent string `json:"traceparent"`
	SpanID      string `json:"span_id,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}

type EventEnvelope struct {
	Event Event `json:"event"`
}

type Event struct {
	EventID  string         `json:"event_id"`
	Sequence int            `json:"sequence"`
	Time     string         `json:"time"`
	Type     string         `json:"type"`
	TenantID string         `json:"tenant_id"`
	AgentID  string         `json:"agent_id"`
	RunID    string         `json:"run_id"`
	StepID   string         `json:"step_id,omitempty"`
	Trace    TraceContext   `json:"trace"`
	Payload  map[string]any `json:"payload"`
}

// Agent represents agent metadata for the registry.
type Agent struct {
	AgentID     string       `json:"agent_id"`
	TenantID    string       `json:"tenant_id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Version     string       `json:"version"`
	Status      string       `json:"status"`
	Config      *AgentConfig `json:"config,omitempty"`
	CreatedAt   string       `json:"created_at"`
	UpdatedAt   string       `json:"updated_at"`
}

type AgentListResponse struct {
	Agents        []Agent `json:"agents"`
	HasMore       bool    `json:"has_more"`
	CorrelationID string  `json:"correlation_id"`
}

type AgentGetResponse struct {
	Agent         Agent  `json:"agent"`
	CorrelationID string `json:"correlation_id"`
}

// AgentCreateRequest is the JSON body for POST /v1/agents.
type AgentCreateRequest struct {
	AgentID     string       `json:"agent_id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Config      *AgentConfig `json:"config,omitempty"`
}

// AgentCreateResponse wraps the created agent.
type AgentCreateResponse struct {
	Agent         Agent  `json:"agent"`
	CorrelationID string `json:"correlation_id"`
}

// RunBatchRequest is the JSON body for POST /v1/agents/{id}/runs:batch.
type RunBatchRequest struct {
	Runs []RunCreateRequest `json:"runs"`
}

// RunBatchResponse wraps batch-created runs.
type RunBatchResponse struct {
	Runs          []Run  `json:"runs"`
	CorrelationID string `json:"correlation_id"`
}

// AgentUpdateRequest is the JSON body for PUT /v1/agents/{agent_id}.
type AgentUpdateRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Config      *AgentConfig `json:"config,omitempty"`
}
