package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/tokens"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

type fileMemoryStore struct {
	mu      sync.Mutex
	dataDir string
}

// NewFileMemoryStore returns a file-backed MemoryStore that persists messages
// as JSON files under dataDir/memory/{tenant_id}/{agent_id}.json.
func NewFileMemoryStore(dataDir string) (MemoryStore, error) {
	if dataDir == "" {
		dataDir = filepath.Join("data", "memory")
	}
	return &fileMemoryStore{dataDir: dataDir}, nil
}

func (s *fileMemoryStore) Append(ctx context.Context, tenantID, agentID string, messages []types.ChatMessage) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if tenantID == "" || agentID == "" {
		return errors.New("tenantID and agentID are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.loadLocked(tenantID, agentID)
	if err != nil {
		return err
	}
	existing = append(existing, messages...)
	if err := s.persistLocked(tenantID, agentID, existing); err != nil {
		metrics.IncMemoryOp("append", "error")
		return err
	}
	metrics.IncMemoryOp("append", "ok")
	return nil
}

func (s *fileMemoryStore) GetRecent(ctx context.Context, tenantID, agentID string, maxMessages int, maxTokens int) ([]types.ChatMessage, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if tenantID == "" || agentID == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.loadLocked(tenantID, agentID)
	if err != nil {
		metrics.IncMemoryOp("get_recent", "error")
		return nil, err
	}
	if len(all) == 0 {
		metrics.IncMemoryOp("get_recent", "ok")
		return nil, nil
	}

	// Take last maxMessages
	if maxMessages > 0 && len(all) > maxMessages {
		all = all[len(all)-maxMessages:]
	}

	// Trim oldest messages until total tokens <= maxTokens
	if maxTokens > 0 {
		all = trimByTokens(all, maxTokens)
	}

	metrics.IncMemoryOp("get_recent", "ok")
	return all, nil
}

func (s *fileMemoryStore) Clear(ctx context.Context, tenantID, agentID string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if tenantID == "" || agentID == "" {
		return errors.New("tenantID and agentID are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.filePath(tenantID, agentID)
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		metrics.IncMemoryOp("clear", "ok")
		return nil
	}
	if err != nil {
		metrics.IncMemoryOp("clear", "error")
		return err
	}
	metrics.IncMemoryOp("clear", "ok")
	return nil
}

func (s *fileMemoryStore) Close() error {
	return nil
}

func (s *fileMemoryStore) filePath(tenantID, agentID string) string {
	return filepath.Join(s.dataDir, tenantID, agentID+".json")
}

func (s *fileMemoryStore) loadLocked(tenantID, agentID string) ([]types.ChatMessage, error) {
	path := s.filePath(tenantID, agentID)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var msgs []types.ChatMessage
	if err := json.Unmarshal(b, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (s *fileMemoryStore) persistLocked(tenantID, agentID string, msgs []types.ChatMessage) error {
	path := s.filePath(tenantID, agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// trimByTokens removes the oldest messages until total estimated tokens <= maxTokens.
// Messages are assumed to be in chronological order.
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
