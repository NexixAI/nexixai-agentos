package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// maxRunsInRunStore is the maximum number of runs to keep in the in-memory cache.
// Runs are persisted to disk, so evicted runs are not lost.
const maxRunsInRunStore = 5000

type fileRunStore struct {
	mu       sync.Mutex
	path     string
	runs     map[string]types.Run
	runOrder []string // access order for LRU eviction
}

// NewFileRunStore returns a file-backed FullRunStore persisted as JSON.
func NewFileRunStore(path string) (FullRunStore, error) {
	if path == "" {
		path = filepath.Join("data", "agent-orchestrator", "runs.json")
	}
	s := &fileRunStore{
		path: path,
		runs: make(map[string]types.Run),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *fileRunStore) Create(ctx context.Context, run types.Run) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := validateRun(run); err != nil {
		return err
	}
	key := storageKey(run.TenantID, run.RunID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.runs[key]; exists {
		return ErrRunExists
	}
	s.runs[key] = run
	s.touchOrderLocked(key)
	s.evictLocked()
	return s.persistLocked()
}

func (s *fileRunStore) Get(ctx context.Context, tenantID, runID string) (types.Run, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return types.Run{}, false, err
	}
	if tenantID == "" || runID == "" {
		return types.Run{}, false, nil
	}
	key := storageKey(tenantID, runID)

	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[key]
	if ok {
		s.touchOrderLocked(key)
	}
	return run, ok, nil
}

func (s *fileRunStore) GetByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (types.Run, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return types.Run{}, false, err
	}
	if tenantID == "" || idempotencyKey == "" {
		return types.Run{}, false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Iterate through runs to find matching idempotency key for tenant
	for _, run := range s.runs {
		if run.TenantID == tenantID && run.IdempotencyKey == idempotencyKey {
			return run, true, nil
		}
	}
	return types.Run{}, false, nil
}

func (s *fileRunStore) Save(ctx context.Context, run types.Run) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := validateRun(run); err != nil {
		return err
	}
	key := storageKey(run.TenantID, run.RunID)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[key] = run
	s.touchOrderLocked(key)
	s.evictLocked()
	return s.persistLocked()
}

func (s *fileRunStore) List(ctx context.Context, tenantID string) ([]types.Run, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if tenantID == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var runs []types.Run
	for _, run := range s.runs {
		if run.TenantID == tenantID {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt > runs[j].CreatedAt
	})
	return runs, nil
}

func (s *fileRunStore) ListByAgent(ctx context.Context, tenantID, agentID string, limit int, afterRunID string) ([]types.Run, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if tenantID == "" || agentID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var runs []types.Run
	for _, run := range s.runs {
		if run.TenantID == tenantID && run.AgentID == agentID {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt > runs[j].CreatedAt
	})

	if afterRunID != "" {
		idx := -1
		for i, run := range runs {
			if run.RunID == afterRunID {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, nil
		}
		runs = runs[idx+1:]
	}

	if len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func (s *fileRunStore) ListChildRuns(ctx context.Context, tenantID, parentRunID string) ([]types.Run, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if tenantID == "" || parentRunID == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var children []types.Run
	for _, run := range s.runs {
		if run.TenantID != tenantID {
			continue
		}
		if run.RetryOf == parentRunID || run.ParentRunID == parentRunID {
			children = append(children, run)
		}
	}
	return children, nil
}

func (s *fileRunStore) Close() error {
	return nil
}

// touchOrderLocked moves key to the end of runOrder (most recently accessed).
// Must be called with mu held.
func (s *fileRunStore) touchOrderLocked(key string) {
	for i, k := range s.runOrder {
		if k == key {
			s.runOrder = append(s.runOrder[:i], s.runOrder[i+1:]...)
			break
		}
	}
	s.runOrder = append(s.runOrder, key)
}

// evictLocked removes the least-recently-accessed runs when over cap.
// Runs remain on disk; only the in-memory cache is pruned.
// Must be called with mu held.
func (s *fileRunStore) evictLocked() {
	for len(s.runs) > maxRunsInRunStore && len(s.runOrder) > 0 {
		oldest := s.runOrder[0]
		s.runOrder = s.runOrder[1:]
		delete(s.runs, oldest)
	}
}

func (s *fileRunStore) load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var persisted map[string]types.Run
	if err := json.Unmarshal(b, &persisted); err != nil {
		return err
	}
	for k, v := range persisted {
		s.runs[k] = v
		s.runOrder = append(s.runOrder, k)
	}
	// If loaded data exceeds cap, evict oldest
	s.evictLocked()
	return nil
}

func (s *fileRunStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.runs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func validateRun(run types.Run) error {
	if run.TenantID == "" || run.RunID == "" {
		return ErrInvalidRun
	}
	return nil
}

func storageKey(tenantID, runID string) string {
	return filepath.ToSlash(filepath.Join("tenant", tenantID, "runs", runID))
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
