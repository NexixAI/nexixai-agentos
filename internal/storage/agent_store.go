package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NexixAI/nexixai-agentos/internal/storage/storageerr"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// Re-export sentinel errors from storageerr so that existing callers
// (e.g. errors.Is(err, storage.ErrAgentNotFound)) continue to work.
var (
	ErrAgentExists   = storageerr.ErrAgentExists
	ErrAgentNotFound = storageerr.ErrAgentNotFound
	ErrInvalidAgent  = storageerr.ErrInvalidAgent
)

// AgentStore is a tenant-scoped persistence port for agent metadata.
type AgentStore interface {
	Create(ctx context.Context, agent types.Agent) error
	Get(ctx context.Context, tenantID, agentID string) (types.Agent, bool, error)
	List(ctx context.Context, tenantID string) ([]types.Agent, error)
	Save(ctx context.Context, agent types.Agent) error
	Delete(ctx context.Context, tenantID, agentID string) error
	Close() error
}

// fileAgentStore is a file-based implementation of AgentStore.
type fileAgentStore struct {
	mu     sync.Mutex
	dir    string
	agents map[string]types.Agent // key: tenant/{tenant_id}/agents/{agent_id}
}

// NewAgentStoreFromEnv constructs the default agent store adapter.
func NewAgentStoreFromEnv() (AgentStore, error) {
	dir := strings.TrimSpace(os.Getenv("AGENTOS_AGENT_STORE_DIR"))
	if dir == "" {
		dir = filepath.Join("data", "agents")
	}
	return NewFileAgentStore(dir)
}

// NewFileAgentStore returns a file-backed AgentStore.
func NewFileAgentStore(dir string) (AgentStore, error) {
	if dir == "" {
		dir = filepath.Join("data", "agents")
	}
	s := &fileAgentStore{
		dir:    dir,
		agents: make(map[string]types.Agent),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *fileAgentStore) Create(ctx context.Context, agent types.Agent) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := validateAgent(agent); err != nil {
		return err
	}
	key := agentStorageKey(agent.TenantID, agent.AgentID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.agents[key]; exists {
		return ErrAgentExists
	}
	s.agents[key] = agent
	return s.persistAgent(agent)
}

func (s *fileAgentStore) Get(ctx context.Context, tenantID, agentID string) (types.Agent, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return types.Agent{}, false, err
	}
	if tenantID == "" || agentID == "" {
		return types.Agent{}, false, nil
	}
	key := agentStorageKey(tenantID, agentID)

	s.mu.Lock()
	defer s.mu.Unlock()
	agent, ok := s.agents[key]
	return agent, ok, nil
}

func (s *fileAgentStore) List(ctx context.Context, tenantID string) ([]types.Agent, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if tenantID == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var agents []types.Agent
	for _, agent := range s.agents {
		if agent.TenantID == tenantID {
			agents = append(agents, agent)
		}
	}
	return agents, nil
}

func (s *fileAgentStore) Save(ctx context.Context, agent types.Agent) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := validateAgent(agent); err != nil {
		return err
	}
	key := agentStorageKey(agent.TenantID, agent.AgentID)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[key] = agent
	return s.persistAgent(agent)
}

func (s *fileAgentStore) Delete(ctx context.Context, tenantID, agentID string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if tenantID == "" || agentID == "" {
		return ErrAgentNotFound
	}
	key := agentStorageKey(tenantID, agentID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.agents[key]; !exists {
		return ErrAgentNotFound
	}
	delete(s.agents, key)

	// Remove JSON file.
	if s.dir != "" {
		agentPath := filepath.Join(s.dir, tenantID, agentID+".json")
		if err := os.Remove(agentPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *fileAgentStore) Close() error {
	return nil
}

func (s *fileAgentStore) load() error {
	if s.dir == "" {
		return nil
	}

	// Walk directory structure: data/agents/{tenant_id}/{agent_id}.json
	err := filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(b) == 0 {
			return nil
		}

		var agent types.Agent
		if err := json.Unmarshal(b, &agent); err != nil {
			return err
		}

		if agent.TenantID != "" && agent.AgentID != "" {
			key := agentStorageKey(agent.TenantID, agent.AgentID)
			s.agents[key] = agent
		}
		return nil
	})

	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *fileAgentStore) persistAgent(agent types.Agent) error {
	if s.dir == "" {
		return nil
	}

	// Create directory: data/agents/{tenant_id}/
	tenantDir := filepath.Join(s.dir, agent.TenantID)
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		return err
	}

	// Write file: data/agents/{tenant_id}/{agent_id}.json
	agentPath := filepath.Join(tenantDir, agent.AgentID+".json")
	b, err := json.MarshalIndent(agent, "", "  ")
	if err != nil {
		return err
	}

	tmp := agentPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, agentPath)
}

func validateAgent(agent types.Agent) error {
	if agent.TenantID == "" || agent.AgentID == "" {
		return ErrInvalidAgent
	}
	return nil
}

func agentStorageKey(tenantID, agentID string) string {
	return filepath.ToSlash(filepath.Join("tenant", tenantID, "agents", agentID))
}
