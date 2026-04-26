package agents

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/id"
)

// AgentStatus represents the lifecycle state of a managed agent.
type AgentStatus string

const (
	AgentStatusCreated   AgentStatus = "created"
	AgentStatusRunning   AgentStatus = "running"
	AgentStatusCompleted AgentStatus = "completed"
	AgentStatusFailed    AgentStatus = "failed"
)

// DefaultMaxConcurrentAgents is the default per-parent limit for spawned agents.
const DefaultMaxConcurrentAgents = 10

// Agent represents a managed agent spawned by a parent.
type Agent struct {
	ID           string      `json:"id"`
	ParentID     string      `json:"parent_id"`
	TenantID     string      `json:"tenant_id"`
	Directive    string      `json:"directive"`
	AllowedTools []string    `json:"allowed_tools"`
	Status       AgentStatus `json:"status"`
	Messages     []Message   `json:"messages"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// Message represents a message between agents.
type Message struct {
	ID        string    `json:"id"`
	FromID    string    `json:"from_id"`
	ToID      string    `json:"to_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentManager defines the interface for managing spawned agents.
type AgentManager interface {
	// SpawnAgent creates a new agent under the given parent.
	// The spawned agent inherits the parent's tenant_id.
	SpawnAgent(ctx context.Context, parentID, tenantID, directive string, tools []string) (*Agent, error)

	// SendMessage delivers a message from one agent to another.
	SendMessage(ctx context.Context, fromID, toID, message string) error

	// GetStatus returns the current state of an agent.
	GetStatus(ctx context.Context, agentID string) (*Agent, error)
}

// InMemoryAgentManager implements AgentManager using in-memory storage.
// It is safe for concurrent use.
type InMemoryAgentManager struct {
	mu                 sync.RWMutex
	agents             map[string]*Agent            // agentID -> Agent
	childrenByParent   map[string]map[string]bool   // parentID -> set of child agentIDs
	maxConcurrentPerParent int
}

// NewInMemoryAgentManager creates an InMemoryAgentManager with the given
// per-parent concurrency limit. Pass 0 to use DefaultMaxConcurrentAgents.
func NewInMemoryAgentManager(maxConcurrent int) *InMemoryAgentManager {
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrentAgents
	}
	return &InMemoryAgentManager{
		agents:                 make(map[string]*Agent),
		childrenByParent:       make(map[string]map[string]bool),
		maxConcurrentPerParent: maxConcurrent,
	}
}

// SeedAgent adds an agent to the manager without validation. This is used
// to register root/parent agents that were not spawned through SpawnAgent.
// It is intended for bootstrapping and testing.
func (m *InMemoryAgentManager) SeedAgent(a *Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[a.ID] = a
}

// SpawnAgent creates a new child agent under the given parent.
func (m *InMemoryAgentManager) SpawnAgent(_ context.Context, parentID, tenantID, directive string, tools []string) (*Agent, error) {
	if parentID == "" {
		return nil, fmt.Errorf("agents: parent_id must not be empty")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("agents: tenant_id must not be empty")
	}
	if directive == "" {
		return nil, fmt.Errorf("agents: directive must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate parent exists and is not in failed state.
	parent, ok := m.agents[parentID]
	if !ok {
		return nil, fmt.Errorf("agents: parent %q not found", parentID)
	}
	if parent.Status == AgentStatusFailed {
		return nil, fmt.Errorf("agents: cannot spawn from failed parent %q", parentID)
	}

	// Enforce tenant isolation: parent must belong to the same tenant.
	if parent.TenantID != tenantID {
		return nil, fmt.Errorf("agents: tenant mismatch — parent %q belongs to tenant %q, not %q", parentID, parent.TenantID, tenantID)
	}

	// Check concurrent child limit.
	children := m.childrenByParent[parentID]
	activeCount := 0
	for childID := range children {
		if child, exists := m.agents[childID]; exists {
			if child.Status == AgentStatusCreated || child.Status == AgentStatusRunning {
				activeCount++
			}
		}
	}
	if activeCount >= m.maxConcurrentPerParent {
		return nil, fmt.Errorf("agents: parent %q has reached max concurrent agents (%d)", parentID, m.maxConcurrentPerParent)
	}

	now := time.Now().UTC()
	agent := &Agent{
		ID:           id.New("agent"),
		ParentID:     parentID,
		TenantID:     tenantID,
		Directive:    directive,
		AllowedTools: tools,
		Status:       AgentStatusCreated,
		Messages:     []Message{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	m.agents[agent.ID] = agent

	if m.childrenByParent[parentID] == nil {
		m.childrenByParent[parentID] = make(map[string]bool)
	}
	m.childrenByParent[parentID][agent.ID] = true

	slog.Info("agents: spawned",
		"agent_id", agent.ID,
		"parent_id", parentID,
		"tenant_id", tenantID,
		"directive_len", len(directive),
	)

	return agent, nil
}

// SendMessage delivers a message from one agent to another.
func (m *InMemoryAgentManager) SendMessage(_ context.Context, fromID, toID, message string) error {
	if fromID == "" {
		return fmt.Errorf("agents: from_id must not be empty")
	}
	if toID == "" {
		return fmt.Errorf("agents: to_id must not be empty")
	}
	if message == "" {
		return fmt.Errorf("agents: message must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	from, ok := m.agents[fromID]
	if !ok {
		return fmt.Errorf("agents: sender %q not found", fromID)
	}

	to, ok := m.agents[toID]
	if !ok {
		return fmt.Errorf("agents: recipient %q not found", toID)
	}

	// Enforce tenant isolation.
	if from.TenantID != to.TenantID {
		return fmt.Errorf("agents: cross-tenant messaging denied (sender tenant %q, recipient tenant %q)", from.TenantID, to.TenantID)
	}

	now := time.Now().UTC()
	msg := Message{
		ID:        id.New("msg"),
		FromID:    fromID,
		ToID:      toID,
		Content:   message,
		CreatedAt: now,
	}

	to.Messages = append(to.Messages, msg)
	to.UpdatedAt = now

	slog.Info("agents: message delivered",
		"from_id", fromID,
		"to_id", toID,
		"msg_len", len(message),
	)

	return nil
}

// GetStatus returns the current state of the given agent.
func (m *InMemoryAgentManager) GetStatus(_ context.Context, agentID string) (*Agent, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agents: agent_id must not be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[agentID]
	if !ok {
		return nil, fmt.Errorf("agents: agent %q not found", agentID)
	}

	// Return a copy to avoid data races on the messages slice.
	cp := *agent
	cp.Messages = make([]Message, len(agent.Messages))
	copy(cp.Messages, agent.Messages)
	cp.AllowedTools = make([]string, len(agent.AllowedTools))
	copy(cp.AllowedTools, agent.AllowedTools)

	return &cp, nil
}
