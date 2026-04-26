package storage

import (
	"context"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// MemoryStore is a tenant-scoped persistence port for conversation memory.
type MemoryStore interface {
	// Append adds messages to conversation memory for an agent.
	Append(ctx context.Context, tenantID, agentID string, messages []types.ChatMessage) error

	// GetRecent returns the most recent messages within the configured window.
	GetRecent(ctx context.Context, tenantID, agentID string, maxMessages int, maxTokens int) ([]types.ChatMessage, error)

	// Clear removes all conversation memory for an agent.
	Clear(ctx context.Context, tenantID, agentID string) error

	Close() error
}
