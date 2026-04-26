package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// InMemoryClearanceStore implements ClearanceStore using an in-memory map.
// It supports a configurable default tier for agents not explicitly registered,
// making it suitable for development and lab environments where a full Postgres
// clearance table is not available.
//
// When DefaultTier is non-negative, unknown agents receive that tier instead
// of an error. Set DefaultTier to -1 to fail closed (match Postgres behavior).
type InMemoryClearanceStore struct {
	mu          sync.RWMutex
	agents      map[string]ClearanceTier
	defaultTier ClearanceTier
	failClosed  bool // when true, unknown agents return an error
}

// InMemoryClearanceOption configures an InMemoryClearanceStore.
type InMemoryClearanceOption func(*InMemoryClearanceStore)

// WithDefaultTier sets the clearance tier returned for agents not explicitly
// registered in the store. By default the store fails closed (returns an error
// for unknown agents).
func WithDefaultTier(tier ClearanceTier) InMemoryClearanceOption {
	return func(s *InMemoryClearanceStore) {
		s.defaultTier = tier
		s.failClosed = false
	}
}

// WithAgents seeds the store with a set of agent-to-tier mappings.
func WithAgents(agents map[string]ClearanceTier) InMemoryClearanceOption {
	return func(s *InMemoryClearanceStore) {
		for k, v := range agents {
			s.agents[k] = v
		}
	}
}

// NewInMemoryClearanceStore creates an in-memory ClearanceStore.
// Without options it fails closed for unknown agents (matching Postgres behavior).
func NewInMemoryClearanceStore(opts ...InMemoryClearanceOption) *InMemoryClearanceStore {
	s := &InMemoryClearanceStore{
		agents:     make(map[string]ClearanceTier),
		failClosed: true,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// GetClearance returns the clearance tier for the given agent.
//
// Resolution order:
//  1. If agentID is empty, return an error (invariant).
//  2. If the agent is explicitly registered, return its tier.
//  3. If a default tier is configured, return that.
//  4. Otherwise return an error (fail closed).
func (s *InMemoryClearanceStore) GetClearance(_ context.Context, agentID string) (ClearanceTier, error) {
	if agentID == "" {
		return 0, fmt.Errorf("clearance: agent_id must not be empty")
	}

	s.mu.RLock()
	tier, ok := s.agents[agentID]
	failClosed := s.failClosed
	defaultTier := s.defaultTier
	s.mu.RUnlock()

	if ok {
		return tier, nil
	}

	if failClosed {
		return 0, fmt.Errorf("clearance: agent %q not found", agentID)
	}

	slog.Debug("clearance: using default tier for unknown agent",
		"agent_id", agentID,
		"default_tier", defaultTier,
	)
	return defaultTier, nil
}

// SetClearance registers or updates the clearance tier for the given agent.
func (s *InMemoryClearanceStore) SetClearance(agentID string, tier ClearanceTier) error {
	if agentID == "" {
		return fmt.Errorf("clearance: agent_id must not be empty")
	}

	s.mu.Lock()
	s.agents[agentID] = tier
	s.mu.Unlock()

	return nil
}

// Compile-time interface check.
var _ ClearanceStore = (*InMemoryClearanceStore)(nil)
