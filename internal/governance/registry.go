package governance

import (
	"fmt"
	"sync"
	"time"
)

// AgentStats holds runtime statistics for a registered agent.
type AgentStats struct {
	// Role is the governance role assigned to this agent.
	Role string
	// ConnectedAt is the time the agent registered.
	ConnectedAt time.Time
	// LastActionAt is the time of the most recent recorded action.
	LastActionAt time.Time
	// ActionCount is the total number of recorded actions.
	ActionCount int64
}

// AgentRegistry tracks agents that have registered with the governance system.
// It is safe for concurrent use.
type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*agentEntry
	// validRoles is the set of roles present in the governance config.
	validRoles map[string]struct{}
}

// agentEntry is the internal representation of a registered agent.
type agentEntry struct {
	role         string
	connectedAt  time.Time
	lastActionAt time.Time
	actionCount  int64
}

// NewAgentRegistry creates a registry that validates roles against the
// provided GovernanceConfig. Only roles that appear in at least one
// agent policy are accepted during registration.
func NewAgentRegistry(cfg *GovernanceConfig) *AgentRegistry {
	roles := make(map[string]struct{})
	for _, policy := range cfg.Agents {
		if policy.Role != "" {
			roles[policy.Role] = struct{}{}
		}
	}
	return &AgentRegistry{
		agents:     make(map[string]*agentEntry),
		validRoles: roles,
	}
}

// Register adds an agent with the given role. It returns an error if the role
// is not defined in the governance config. If the agent is already registered,
// the existing registration is replaced.
func (r *AgentRegistry) Register(agentID, role string) error {
	// Validate the role before acquiring the write lock.
	r.mu.RLock()
	_, ok := r.validRoles[role]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("unknown role %q: not defined in governance config", role)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[agentID] = &agentEntry{
		role:        role,
		connectedAt: time.Now(),
	}
	return nil
}

// GetRole returns the role for a registered agent.
// The second return value is false if the agent is not registered.
func (r *AgentRegistry) GetRole(agentID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.agents[agentID]
	if !ok {
		return "", false
	}
	return entry.role, true
}

// RecordAction increments the action count and updates the last action time
// for the given agent. It is a no-op if the agent is not registered.
func (r *AgentRegistry) RecordAction(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.agents[agentID]
	if !ok {
		return
	}
	entry.actionCount++
	entry.lastActionAt = time.Now()
}

// GetStats returns the runtime statistics for a registered agent.
// If the agent is not registered, a zero-value AgentStats is returned.
func (r *AgentRegistry) GetStats(agentID string) AgentStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.agents[agentID]
	if !ok {
		return AgentStats{}
	}
	return AgentStats{
		Role:         entry.role,
		ConnectedAt:  entry.connectedAt,
		LastActionAt: entry.lastActionAt,
		ActionCount:  entry.actionCount,
	}
}
