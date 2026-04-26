package governance

import (
	"log/slog"
	"sync"
	"time"
)

// FreezeRecord describes a frozen agent: who froze it, why, and when.
type FreezeRecord struct {
	AgentID  string    `json:"agent_id"`
	Reason   string    `json:"reason"`
	FrozenBy string    `json:"frozen_by"`
	FrozenAt time.Time `json:"frozen_at"`
}

// KillSwitch tracks frozen agents and provides Freeze/Unfreeze/IsFrozen
// operations. Thread-safe via sync.RWMutex.
//
// The kill switch is checked at the very top of Engine.Check — a frozen
// agent is denied before any other policy evaluation.
type KillSwitch struct {
	mu      sync.RWMutex
	frozen  map[string]FreezeRecord
}

// NewKillSwitch creates a new, empty KillSwitch.
func NewKillSwitch() *KillSwitch {
	return &KillSwitch{
		frozen: make(map[string]FreezeRecord),
	}
}

// Freeze marks the given agent as frozen. If the agent is already frozen,
// the record is updated with the new reason, frozenBy, and timestamp.
// Freezing an agent that is not registered in governance.yml is allowed
// (preventive freeze).
func (ks *KillSwitch) Freeze(agentID, reason, frozenBy string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.frozen[agentID] = FreezeRecord{
		AgentID:  agentID,
		Reason:   reason,
		FrozenBy: frozenBy,
		FrozenAt: time.Now(),
	}
	slog.Warn("governance: kill switch activated",
		"agent", agentID,
		"reason", reason,
		"frozen_by", frozenBy,
	)
}

// Unfreeze removes the frozen state for the given agent.
// No-op if the agent is not frozen.
func (ks *KillSwitch) Unfreeze(agentID string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if _, ok := ks.frozen[agentID]; ok {
		delete(ks.frozen, agentID)
		slog.Info("governance: kill switch deactivated",
			"agent", agentID,
		)
	}
}

// IsFrozen returns whether the agent is frozen and, if so, the reason.
func (ks *KillSwitch) IsFrozen(agentID string) (bool, string) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	rec, ok := ks.frozen[agentID]
	if !ok {
		return false, ""
	}
	return true, rec.Reason
}

// ListFrozen returns a snapshot of all currently frozen agents.
func (ks *KillSwitch) ListFrozen() []FreezeRecord {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	out := make([]FreezeRecord, 0, len(ks.frozen))
	for _, rec := range ks.frozen {
		out = append(out, rec)
	}
	return out
}
