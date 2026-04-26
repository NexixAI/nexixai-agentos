package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// AgentVersion represents a point-in-time snapshot of an agent's configuration.
type AgentVersion struct {
	ID        int64           `json:"id"`
	TenantID  string          `json:"tenant_id"`
	AgentID   string          `json:"agent_id"`
	Version   string          `json:"version"`
	Config    json.RawMessage `json:"config"`
	CreatedBy string          `json:"created_by"`
	CreatedAt string          `json:"created_at"`
}

// AgentVersionStore manages agent version history snapshots.
type AgentVersionStore interface {
	CreateSnapshot(ctx context.Context, tenantID, agentID, version, createdBy string, config json.RawMessage) error
	ListVersions(ctx context.Context, tenantID, agentID string, limit int, afterID int64) ([]AgentVersion, bool, error)
	GetVersion(ctx context.Context, tenantID, agentID, version string) (*AgentVersion, error)
	CountVersions(ctx context.Context, tenantID, agentID string) (int, error)
}

// maxVersionsPerAgent is the upper bound on in-memory version snapshots per agent.
// This prevents unbounded growth (invariant: no unbounded in-memory collections).
const maxVersionsPerAgent = 1000

// memoryAgentVersionStore is a mutex-protected in-memory implementation of AgentVersionStore.
type memoryAgentVersionStore struct {
	mu       sync.Mutex
	versions map[string][]AgentVersion // key: tenantID + "/" + agentID
	nextID   int64
}

// NewMemoryAgentVersionStore returns an in-memory AgentVersionStore with bounded collections.
func NewMemoryAgentVersionStore() AgentVersionStore {
	return &memoryAgentVersionStore{
		versions: make(map[string][]AgentVersion),
		nextID:   1,
	}
}

func agentVersionKey(tenantID, agentID string) string {
	return tenantID + "/" + agentID
}

func (s *memoryAgentVersionStore) CreateSnapshot(ctx context.Context, tenantID, agentID, version, createdBy string, config json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenantID == "" || agentID == "" || version == "" {
		return fmt.Errorf("tenant_id, agent_id, and version are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := agentVersionKey(tenantID, agentID)
	av := AgentVersion{
		ID:        s.nextID,
		TenantID:  tenantID,
		AgentID:   agentID,
		Version:   version,
		Config:    config,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.nextID++

	versions := s.versions[key]
	versions = append(versions, av)

	// Enforce bounded collection: keep only the most recent maxVersionsPerAgent entries.
	if len(versions) > maxVersionsPerAgent {
		excess := len(versions) - maxVersionsPerAgent
		slog.Warn("agent version store: evicting old snapshots",
			"tenant_id", tenantID, "agent_id", agentID, "evicted", excess)
		versions = versions[excess:]
	}
	s.versions[key] = versions
	return nil
}

func (s *memoryAgentVersionStore) ListVersions(ctx context.Context, tenantID, agentID string, limit int, afterID int64) ([]AgentVersion, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if tenantID == "" || agentID == "" {
		return nil, false, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := agentVersionKey(tenantID, agentID)
	all := s.versions[key]

	// Filter by afterID cursor.
	var filtered []AgentVersion
	for _, v := range all {
		if v.ID > afterID {
			filtered = append(filtered, v)
		}
	}

	// Sort by ID ascending (oldest first within page).
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ID < filtered[j].ID
	})

	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	return filtered, hasMore, nil
}

func (s *memoryAgentVersionStore) GetVersion(ctx context.Context, tenantID, agentID, version string) (*AgentVersion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenantID == "" || agentID == "" || version == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := agentVersionKey(tenantID, agentID)
	for i := len(s.versions[key]) - 1; i >= 0; i-- {
		v := s.versions[key][i]
		if v.Version == version {
			return &v, nil
		}
	}
	return nil, nil
}

func (s *memoryAgentVersionStore) CountVersions(ctx context.Context, tenantID, agentID string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if tenantID == "" || agentID == "" {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := agentVersionKey(tenantID, agentID)
	return len(s.versions[key]), nil
}
