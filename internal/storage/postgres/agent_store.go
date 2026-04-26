package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/storage/storageerr"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// AgentStore is a PostgreSQL-backed implementation of storage.AgentStore.
type AgentStore struct {
	db *sql.DB
}

// NewAgentStore opens a connection pool and returns a ready-to-use AgentStore.
func NewAgentStore(cfg config.PostgresConfig) (*AgentStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &AgentStore{db: db}, nil
}

// NewAgentStoreFromDB wraps an existing *sql.DB as an AgentStore.
func NewAgentStoreFromDB(db *sql.DB) *AgentStore {
	return &AgentStore{db: db}
}

func (s *AgentStore) Create(ctx context.Context, agent types.Agent) error {
	const q = `
INSERT INTO agents (agent_id, tenant_id, name, description, version, status, config, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	configJSON, err := marshalConfig(agent.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, q,
		agent.AgentID, agent.TenantID, agent.Name, agent.Description,
		agent.Version, agent.Status, configJSON, agent.CreatedAt, agent.UpdatedAt,
	)
	return err
}

func (s *AgentStore) Get(ctx context.Context, tenantID, agentID string) (types.Agent, bool, error) {
	if tenantID == "" || agentID == "" {
		return types.Agent{}, false, nil
	}

	const q = `
SELECT agent_id, tenant_id, name, description, version, status, config, created_at, updated_at
FROM agents
WHERE tenant_id = $1 AND agent_id = $2`

	var a types.Agent
	var configJSON []byte
	err := s.db.QueryRowContext(ctx, q, tenantID, agentID).Scan(
		&a.AgentID, &a.TenantID, &a.Name, &a.Description,
		&a.Version, &a.Status, &configJSON, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return types.Agent{}, false, nil
	}
	if err != nil {
		return types.Agent{}, false, err
	}
	a.Config = unmarshalConfig(configJSON)
	return a, true, nil
}

func (s *AgentStore) List(ctx context.Context, tenantID string) ([]types.Agent, error) {
	if tenantID == "" {
		return nil, nil
	}

	const q = `
SELECT agent_id, tenant_id, name, description, version, status, config, created_at, updated_at
FROM agents
WHERE tenant_id = $1
ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []types.Agent
	for rows.Next() {
		var a types.Agent
		var configJSON []byte
		if err := rows.Scan(
			&a.AgentID, &a.TenantID, &a.Name, &a.Description,
			&a.Version, &a.Status, &configJSON, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}
		a.Config = unmarshalConfig(configJSON)
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *AgentStore) Save(ctx context.Context, agent types.Agent) error {
	const q = `
INSERT INTO agents (agent_id, tenant_id, name, description, version, status, config, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (tenant_id, agent_id) DO UPDATE SET
    name        = EXCLUDED.name,
    description = EXCLUDED.description,
    version     = EXCLUDED.version,
    status      = EXCLUDED.status,
    config      = EXCLUDED.config,
    created_at  = EXCLUDED.created_at,
    updated_at  = EXCLUDED.updated_at`

	configJSON, err := marshalConfig(agent.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, q,
		agent.AgentID, agent.TenantID, agent.Name, agent.Description,
		agent.Version, agent.Status, configJSON, agent.CreatedAt, agent.UpdatedAt,
	)
	return err
}

func marshalConfig(cfg *types.AgentConfig) ([]byte, error) {
	if cfg == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(cfg)
}

func unmarshalConfig(data []byte) *types.AgentConfig {
	if len(data) == 0 || string(data) == "{}" {
		return nil
	}
	var cfg types.AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

func (s *AgentStore) Delete(ctx context.Context, tenantID, agentID string) error {
	const q = `DELETE FROM agents WHERE tenant_id = $1 AND agent_id = $2`
	result, err := s.db.ExecContext(ctx, q, tenantID, agentID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return storageerr.ErrAgentNotFound
	}
	return nil
}

func (s *AgentStore) Close() error {
	return s.db.Close()
}
