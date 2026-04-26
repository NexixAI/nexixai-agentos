// Package governance implements the platform governance engine that evaluates
// every MCP tool call against governance.yml policy. Aligned with the CSA
// Agentic Trust Framework (ATF) five-domain model.
package governance

import "time"

// Verdict is the outcome of a governance check.
type Verdict string

const (
	// Allow indicates the tool call is permitted.
	Allow Verdict = "allow"
	// Deny indicates the tool call is blocked.
	Deny Verdict = "deny"
)

// Decision is the result of evaluating a tool call against governance policy.
type Decision struct {
	// Verdict is Allow or Deny.
	Verdict Verdict `json:"verdict"`
	// Reason explains why the call was denied. Empty on Allow.
	Reason string `json:"reason,omitempty"`
	// Domain is the policy domain that triggered a Deny (e.g., "identity", "segmentation", "behavior").
	Domain string `json:"domain,omitempty"`
}

// --- governance.yml schema types ---

// GovernanceConfig is the top-level structure of governance.yml.
type GovernanceConfig struct {
	Version   string                       `yaml:"version"`
	Framework string                       `yaml:"framework"`
	Agents    map[string]AgentPolicy       `yaml:"agents"`
	Behavior  BehaviorConfig               `yaml:"behavior"`
	Data      DataConfig                   `yaml:"data"`
	Segments  map[string]SegmentationEntry `yaml:"segmentation"`
}

// AgentPolicy defines identity attributes for a single agent (Domain 1).
type AgentPolicy struct {
	Role        string `yaml:"role"`
	Description string `yaml:"description"`
	Clearance   string `yaml:"clearance"`
	Auth        string `yaml:"auth"`
}

// BehaviorConfig holds Domain 2 behavioral limits.
type BehaviorConfig struct {
	RateLimits    map[string]RateLimit  `yaml:"rate_limits"`
	LoopDetection LoopDetectionConfig   `yaml:"loop_detection"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker"`
	Confidence    ConfidenceConfig      `yaml:"confidence"`
}

// ToolCategory classifies tools for split rate limiting.
type ToolCategory string

const (
	CategoryObserve ToolCategory = "observe"
	CategoryAction  ToolCategory = "action"
)

// RateLimit defines per-agent rate limits.
// If ObservePerHour/ActionPerHour are set, they apply separately.
// If only PerHour is set, it applies to all tool calls (backward compat).
type RateLimit struct {
	PerHour        int `yaml:"per_hour"`
	PerDay         int `yaml:"per_day"`
	ObservePerHour int `yaml:"observe_per_hour,omitempty"`
	ActionPerHour  int `yaml:"action_per_hour,omitempty"`
}

// LoopDetectionConfig defines loop detection parameters.
type LoopDetectionConfig struct {
	MaxAttemptsPerTarget int    `yaml:"max_attempts_per_target"`
	Window               string `yaml:"window"`
	OnTrigger            string `yaml:"on_trigger"`
}

// CircuitBreakerConfig defines circuit breaker parameters.
type CircuitBreakerConfig struct {
	ConsecutiveFailures int    `yaml:"consecutive_failures"`
	Action              string `yaml:"action"`
	Notify              string `yaml:"notify"`
	AutoUnfreeze        bool   `yaml:"auto_unfreeze"`
}

// ConfidenceConfig defines confidence thresholds for action gating.
type ConfidenceConfig struct {
	ActThreshold   float64 `yaml:"act_threshold"`
	WatchThreshold float64 `yaml:"watch_threshold"`
	LogThreshold   float64 `yaml:"log_threshold"`
}

// SegmentationEntry defines what actions a role can take (Domain 4).
type SegmentationEntry struct {
	AllowedActions   []string `yaml:"allowed_actions"`
	DeniedActions    []string `yaml:"denied_actions"`
	ProtectedTargets []string `yaml:"protected_targets"`
	ProtectedGate    string   `yaml:"protected_gate"`
	Scope            string   `yaml:"scope"`
}

// DataConfig defines Domain 3 data governance rules.
type DataConfig struct {
	Readable   []string `yaml:"readable"`
	Writable   []string `yaml:"writable"`
	NeverTouch []string `yaml:"never_touch"`
}

// --- Behavior tracking interfaces ---

// RateLimiter tracks per-agent action rates.
type RateLimiter interface {
	// RecordAction records that an agent performed an action at the given time.
	// category is "observe" or "action" for split rate limiting.
	RecordAction(agentID string, category ToolCategory, t time.Time)
	// Check returns an error message if the agent has exceeded its rate limit.
	// Returns empty string if within limits.
	Check(agentID string, category ToolCategory, limits RateLimit, now time.Time) string
}

// LoopDetector tracks repeated (agent, action, target) tuples.
type LoopDetector interface {
	// RecordAttempt records that an agent attempted an action on a target.
	RecordAttempt(agentID, action, target string, t time.Time)
	// Check returns an error message if a loop is detected.
	// Returns empty string if no loop.
	Check(agentID, action, target string, maxAttempts int, window time.Duration, now time.Time) string
}

// CircuitBreakerTracker tracks consecutive failures per agent.
type CircuitBreakerTracker interface {
	// RecordSuccess resets the failure counter for an agent.
	RecordSuccess(agentID string)
	// RecordFailure increments the failure counter for an agent.
	// Returns the new count.
	RecordFailure(agentID string) int
	// ConsecutiveFailures returns the current failure count for an agent.
	ConsecutiveFailures(agentID string) int
	// IsFrozen returns true if the agent has been auto-frozen by the circuit breaker.
	IsFrozen(agentID string) bool
	// Freeze marks the agent as frozen.
	Freeze(agentID string)
}
