// Package governance provides the audit logging for governance decisions.
package governance

import (
	"log/slog"
	"time"
)

// AuditEntry records a governance decision for a tool call.
type AuditEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	AgentID     string    `json:"agent_id"`
	ToolName    string    `json:"tool_name"`
	Decision    string    `json:"decision"`    // "allow" or "deny"
	Reason      string    `json:"reason"`
	Domain      string    `json:"domain"`      // which domain triggered deny (empty if allow)
	DurationMs  float64   `json:"duration_ms"`
}

// KillSwitchAuditEntry records a freeze/unfreeze action.
type KillSwitchAuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agent_id"`
	Action    string    `json:"action"`    // "freeze" or "unfreeze"
	Reason    string    `json:"reason"`
	By        string    `json:"by"`
}

// CircuitBreakerAuditEntry records a circuit breaker trigger.
type CircuitBreakerAuditEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	AgentID      string    `json:"agent_id"`
	FailureCount int       `json:"failure_count"`
	AutoFreeze   bool      `json:"auto_freeze"`
}

// AuditLogger logs governance decisions. All methods are non-blocking.
type AuditLogger struct{}

// NewAuditLogger creates an audit logger.
func NewAuditLogger() *AuditLogger {
	return &AuditLogger{}
}

// LogDecision logs a governance check result.
func (a *AuditLogger) LogDecision(entry AuditEntry) {
	if entry.Decision == "deny" {
		slog.Warn("governance: denied",
			"agent", entry.AgentID,
			"tool", entry.ToolName,
			"reason", entry.Reason,
			"domain", entry.Domain,
			"duration_ms", entry.DurationMs,
		)
	} else {
		slog.Info("governance: allowed",
			"agent", entry.AgentID,
			"tool", entry.ToolName,
			"duration_ms", entry.DurationMs,
		)
	}
}

// LogKillSwitch logs a freeze or unfreeze action.
func (a *AuditLogger) LogKillSwitch(entry KillSwitchAuditEntry) {
	slog.Warn("governance: kill switch",
		"agent", entry.AgentID,
		"action", entry.Action,
		"reason", entry.Reason,
		"by", entry.By,
	)
}

// LogCircuitBreaker logs a circuit breaker trigger.
func (a *AuditLogger) LogCircuitBreaker(entry CircuitBreakerAuditEntry) {
	slog.Warn("governance: circuit breaker",
		"agent", entry.AgentID,
		"failures", entry.FailureCount,
		"auto_freeze", entry.AutoFreeze,
	)
}
