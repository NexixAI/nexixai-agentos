package types

type PeerInfoResponse struct {
	PeerID  string         `json:"peer_id"`
	Name    string         `json:"name"`
	BaseURL string         `json:"base_url"`
	Meta    map[string]any `json:"meta,omitempty"`
}

type PeerCapabilitiesResponse struct {
	PeerID        string         `json:"peer_id"`
	Protocol      string         `json:"protocol"`
	Capabilities  []string       `json:"capabilities"`
	EventBackhaul map[string]any `json:"event_backhaul,omitempty"`
}

type AuthContext struct {
	TenantID    string         `json:"tenant_id"`
	PrincipalID string         `json:"principal_id"`
	Scopes      []string       `json:"scopes"`
	SubjectType string         `json:"subject_type,omitempty"`
	APIKeyID    string         `json:"api_key_id,omitempty"`
	JWTClaims   map[string]any `json:"jwt_claims,omitempty"`
}

type FederationForwardRunRequest struct {
	Forwarding map[string]any   `json:"forwarding"`
	Auth       AuthContext      `json:"auth"`
	RunCreate  RunCreateRequest `json:"run_create"`
	Trace      map[string]any   `json:"trace,omitempty"`
}

type FederationForwardRunResponse struct {
	ForwardedTo   map[string]any `json:"forwarded_to"`
	Run           Run            `json:"run"`
	CorrelationID string         `json:"correlation_id"`
}

type FederationEventIngestRequest struct {
	PeerID string          `json:"peer_id"`
	Auth   AuthContext     `json:"auth"`
	Events []EventEnvelope `json:"events"`
	Trace  map[string]any  `json:"trace,omitempty"`
}

type FederationEventIngestResponse struct {
	Accepted      int    `json:"accepted"`
	Rejected      int    `json:"rejected"`
	CorrelationID string `json:"correlation_id"`
}
