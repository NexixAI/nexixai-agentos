package types

type Model struct {
	ModelID      string         `json:"model_id"`
	Provider     string         `json:"provider,omitempty"`
	DisplayName  string         `json:"display_name,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
}

type ModelsListResponse struct {
	Models []Model `json:"models"`
}

// The repo's Model Policy OpenAPI uses an invoke-style request/response.
type ModelInvokeRequest struct {
	Operation string         `json:"operation"`
	ModelID   string         `json:"model_id"`
	Input     map[string]any `json:"input"`
	Options   map[string]any `json:"options,omitempty"`
	Trace     map[string]any `json:"trace,omitempty"`
}

type ModelInvokeResponse struct {
	Output        map[string]any `json:"output"`
	Usage         map[string]any `json:"usage,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
}

type PolicyCheckRequest struct {
	Action   string         `json:"action"`
	Resource map[string]any `json:"resource"`
	Context  map[string]any `json:"context,omitempty"`
}

type PolicyCheckResponse struct {
	Decision      string           `json:"decision"`
	Reasons       []string         `json:"reasons,omitempty"`
	Obligations   []map[string]any `json:"obligations,omitempty"`
	CorrelationID string           `json:"correlation_id,omitempty"`
}
