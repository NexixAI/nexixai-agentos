package types

// Tenant represents a tenant admin record.
type Tenant struct {
	TenantID     string         `json:"tenant_id"`
	Name         string         `json:"name,omitempty"`
	Status       string         `json:"status,omitempty"`
	PlanTier     string         `json:"plan_tier,omitempty"`
	Entitlements map[string]any `json:"entitlements,omitempty"`
	Quotas       map[string]any `json:"quotas,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Policy       *TenantPolicy  `json:"policy,omitempty"`
	CreatedAt    string         `json:"created_at,omitempty"`
	UpdatedAt    string         `json:"updated_at,omitempty"`
}

// TenantPolicy defines per-tenant model access and usage policies.
type TenantPolicy struct {
	AllowedModels []string     `json:"allowed_models,omitempty"`
	DeniedModels  []string     `json:"denied_models,omitempty"`
	TokenBudget   *TokenBudget `json:"token_budget,omitempty"`
	PIIPolicy     *PIIPolicy   `json:"pii_policy,omitempty"`
}

// PIIPolicy defines per-tenant PII detection and handling policy.
type PIIPolicy struct {
	Mode     string   `json:"mode"`     // "block", "redact", "warn"
	Patterns []string `json:"patterns"` // which patterns to enable; nil/empty = all
}

// TokenBudget defines token usage limits per tenant.
type TokenBudget struct {
	MaxTokensPerHour int `json:"max_tokens_per_hour,omitempty"`
	MaxTokensPerDay  int `json:"max_tokens_per_day,omitempty"`
}

// AgentConfig holds typed configuration for an agent's execution behaviour.
type AgentConfig struct {
	SystemPrompt string       `json:"system_prompt,omitempty"`
	ModelID      string       `json:"model_id,omitempty"`
	Tools        []string     `json:"tools,omitempty"`
	CustomTools  []CustomTool `json:"custom_tools,omitempty"`
	MaxSteps     int          `json:"max_steps,omitempty"`
	TimeoutMs    int          `json:"timeout_ms,omitempty"`
}

// CustomTool defines a tenant-scoped webhook tool registered via agent config.
type CustomTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]any    `json:"input_schema,omitempty"`
	WebhookURL  string            `json:"webhook_url"`
	Headers     map[string]string `json:"headers,omitempty"`
	TimeoutMs   int               `json:"timeout_ms,omitempty"`
}
