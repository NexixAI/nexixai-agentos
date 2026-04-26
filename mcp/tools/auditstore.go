package tools

import (
	"context"
	"strings"
	"sync"
)

// InMemoryAuditStore implements both AuditLogger (write) and AuditReader (read).
// Shared instance allows log_decision writes to be read by get_audit_log.
type InMemoryAuditStore struct {
	mu      sync.RWMutex
	entries []AuditEntry
	maxSize int
}

// NewInMemoryAuditStore creates a shared audit store with a max entry cap.
func NewInMemoryAuditStore(maxSize int) *InMemoryAuditStore {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &InMemoryAuditStore{maxSize: maxSize}
}

// LogDecision implements AuditLogger — writes a decision as an audit entry.
func (s *InMemoryAuditStore) LogDecision(_ context.Context, entry DecisionEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, AuditEntry{
		ID:        entry.ID,
		AgentID:   entry.AgentID,
		TenantID:  entry.TenantID,
		Action:    "decision: " + entry.Decision,
		Detail:    entry.Reasoning,
		CreatedAt: entry.CreatedAt,
	})

	// Evict oldest if over cap
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize:]
	}
	return nil
}

// Read implements AuditReader — reads audit entries with optional filters.
func (s *InMemoryAuditStore) Read(_ context.Context, filter AuditFilter) ([]AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	var results []AuditEntry
	// Iterate backwards (newest first)
	for i := len(s.entries) - 1; i >= 0 && len(results) < limit; i-- {
		e := s.entries[i]
		if filter.AgentID != "" && !strings.EqualFold(e.AgentID, filter.AgentID) {
			continue
		}
		if filter.TenantID != "" && !strings.EqualFold(e.TenantID, filter.TenantID) {
			continue
		}
		results = append(results, e)
	}
	return results, nil
}

// Ensure compile-time interface compliance.
var _ AuditLogger = (*InMemoryAuditStore)(nil)
