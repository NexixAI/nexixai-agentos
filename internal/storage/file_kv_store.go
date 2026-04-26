package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

const (
	defaultMaxValueSize    = 65536
	defaultMaxKeysPerAgent = 1000
)

type fileKVStore struct {
	mu              sync.Mutex
	dataDir         string
	maxValueSize    int
	maxKeysPerAgent int
}

// NewFileKVStore returns a file-backed KVStore that persists key-value pairs
// as JSON files under dataDir/kv/{tenant_id}/{agent_id}.json.
func NewFileKVStore(dataDir string, maxValueSize, maxKeysPerAgent int) (KVStore, error) {
	if dataDir == "" {
		dataDir = filepath.Join("data", "kv")
	}
	if maxValueSize <= 0 {
		maxValueSize = defaultMaxValueSize
	}
	if maxKeysPerAgent <= 0 {
		maxKeysPerAgent = defaultMaxKeysPerAgent
	}
	return &fileKVStore{
		dataDir:         dataDir,
		maxValueSize:    maxValueSize,
		maxKeysPerAgent: maxKeysPerAgent,
	}, nil
}

func (s *fileKVStore) Get(ctx context.Context, tenantID, agentID, key string) (string, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return "", false, err
	}
	if err := types.ValidateKey(key); err != nil {
		return "", false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.loadLocked(tenantID, agentID)
	if err != nil {
		metrics.IncKVOp("get", "error")
		return "", false, err
	}
	v, ok := data[key]
	metrics.IncKVOp("get", "ok")
	return v, ok, nil
}

func (s *fileKVStore) Set(ctx context.Context, tenantID, agentID, key, value string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if tenantID == "" || agentID == "" {
		return errors.New("tenantID and agentID are required")
	}
	if err := types.ValidateKey(key); err != nil {
		return err
	}
	if err := types.ValidateValueSize(value, s.maxValueSize); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.loadLocked(tenantID, agentID)
	if err != nil {
		return err
	}

	// Check key limit (only count if this is a new key)
	if _, exists := data[key]; !exists && len(data) >= s.maxKeysPerAgent {
		return types.ErrMaxKeysExceeded
	}

	data[key] = value
	if err := s.persistLocked(tenantID, agentID, data); err != nil {
		metrics.IncKVOp("set", "error")
		return err
	}
	metrics.IncKVOp("set", "ok")
	return nil
}

func (s *fileKVStore) Delete(ctx context.Context, tenantID, agentID, key string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := types.ValidateKey(key); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.loadLocked(tenantID, agentID)
	if err != nil {
		return err
	}
	delete(data, key)
	if err := s.persistLocked(tenantID, agentID, data); err != nil {
		metrics.IncKVOp("delete", "error")
		return err
	}
	metrics.IncKVOp("delete", "ok")
	return nil
}

func (s *fileKVStore) ListKeys(ctx context.Context, tenantID, agentID string) ([]string, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if tenantID == "" || agentID == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.loadLocked(tenantID, agentID)
	if err != nil {
		metrics.IncKVOp("list", "error")
		return nil, err
	}

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	metrics.IncKVOp("list", "ok")
	return keys, nil
}

func (s *fileKVStore) Close() error {
	return nil
}

func (s *fileKVStore) filePath(tenantID, agentID string) string {
	return filepath.Join(s.dataDir, tenantID, agentID+".json")
}

func (s *fileKVStore) loadLocked(tenantID, agentID string) (map[string]string, error) {
	path := s.filePath(tenantID, agentID)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]string), nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return make(map[string]string), nil
	}
	var data map[string]string
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *fileKVStore) persistLocked(tenantID, agentID string, data map[string]string) error {
	path := s.filePath(tenantID, agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
