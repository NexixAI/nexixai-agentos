package postgres

import (
	"context"
	"database/sql"
	"errors"

	_ "github.com/lib/pq"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/tokens"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// MemoryStore is a PostgreSQL-backed implementation of storage.MemoryStore.
type MemoryStore struct {
	db *sql.DB
}

// NewMemoryStore opens a connection pool and returns a ready-to-use MemoryStore.
func NewMemoryStore(cfg config.PostgresConfig) (*MemoryStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &MemoryStore{db: db}, nil
}

// NewMemoryStoreFromDB wraps an existing *sql.DB as a MemoryStore.
func NewMemoryStoreFromDB(db *sql.DB) *MemoryStore {
	return &MemoryStore{db: db}
}

func (s *MemoryStore) Append(ctx context.Context, tenantID, agentID string, messages []types.ChatMessage) error {
	if tenantID == "" || agentID == "" {
		return errors.New("tenantID and agentID are required")
	}
	if len(messages) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const q = `
INSERT INTO conversation_memory (tenant_id, agent_id, role, content, tool_call_id, tool_calls)
VALUES ($1, $2, $3, $4, $5, $6)`

	for _, msg := range messages {
		var toolCalls []byte
		if len(msg.ToolCalls) > 0 {
			toolCalls = msg.ToolCalls
		}
		_, err := tx.ExecContext(ctx, q,
			tenantID, agentID, msg.Role, msg.Content, nilIfEmpty(msg.ToolCallID), toolCalls,
		)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		metrics.IncMemoryOp("append", "error")
		return err
	}
	metrics.IncMemoryOp("append", "ok")
	return nil
}

func (s *MemoryStore) GetRecent(ctx context.Context, tenantID, agentID string, maxMessages int, maxTokens int) ([]types.ChatMessage, error) {
	if tenantID == "" || agentID == "" {
		return nil, nil
	}

	const q = `
SELECT role, content, COALESCE(tool_call_id, ''), tool_calls
FROM conversation_memory
WHERE tenant_id = $1 AND agent_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3`

	limit := maxMessages
	if limit <= 0 {
		limit = 1000 // sensible upper bound
	}

	rows, err := s.db.QueryContext(ctx, q, tenantID, agentID, limit)
	if err != nil {
		metrics.IncMemoryOp("get_recent", "error")
		return nil, err
	}
	defer rows.Close()

	var msgs []types.ChatMessage
	for rows.Next() {
		var m types.ChatMessage
		var toolCalls []byte
		if err := rows.Scan(&m.Role, &m.Content, &m.ToolCallID, &toolCalls); err != nil {
			metrics.IncMemoryOp("get_recent", "error")
			return nil, err
		}
		if len(toolCalls) > 0 {
			m.ToolCalls = toolCalls
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		metrics.IncMemoryOp("get_recent", "error")
		return nil, err
	}

	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	// Trim by tokens (oldest first)
	if maxTokens > 0 {
		msgs = trimByTokens(msgs, maxTokens)
	}

	metrics.IncMemoryOp("get_recent", "ok")
	return msgs, nil
}

func (s *MemoryStore) Clear(ctx context.Context, tenantID, agentID string) error {
	if tenantID == "" || agentID == "" {
		return errors.New("tenantID and agentID are required")
	}

	const q = `DELETE FROM conversation_memory WHERE tenant_id = $1 AND agent_id = $2`
	_, err := s.db.ExecContext(ctx, q, tenantID, agentID)
	if err != nil {
		metrics.IncMemoryOp("clear", "error")
		return err
	}
	metrics.IncMemoryOp("clear", "ok")
	return nil
}

func (s *MemoryStore) Close() error {
	return s.db.Close()
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// trimByTokens removes the oldest messages until total estimated tokens <= maxTokens.
func trimByTokens(msgs []types.ChatMessage, maxTokens int) []types.ChatMessage {
	total := 0
	for _, m := range msgs {
		total += tokens.EstimateTokens(m.Content)
	}
	for len(msgs) > 0 && total > maxTokens {
		total -= tokens.EstimateTokens(msgs[0].Content)
		msgs = msgs[1:]
	}
	return msgs
}
