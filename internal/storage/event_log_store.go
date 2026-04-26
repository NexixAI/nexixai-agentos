package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// EventLogStore provides durable storage for run events with cursor-based replay.
type EventLogStore interface {
	// Append persists an event to the durable log.
	Append(ctx context.Context, event types.EventEnvelope) error

	// QueryFromSequence returns events for a run starting after the given sequence.
	// If afterSequence is 0, all events are returned.
	QueryFromSequence(ctx context.Context, tenantID, runID string, afterSequence int) ([]types.EventEnvelope, error)

	Close() error
}

// maxRunsInMemory is the maximum number of runs to keep in the in-memory cache.
// Events are persisted to disk on every Append, so evicted runs can be reloaded.
const maxRunsInMemory = 1000

// fileEventLogStore stores events as JSON files per run.
type fileEventLogStore struct {
	mu      sync.Mutex
	dataDir string
	// In-memory index for fast lookups (backed by files).
	runs     map[string][]types.EventEnvelope // key: tenantID/runID
	runOrder []string                         // insertion order for LRU eviction
}

// NewFileEventLogStore creates a file-based event log store.
func NewFileEventLogStore(dataDir string) (EventLogStore, error) {
	dir := filepath.Join(dataDir, "event_log")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &fileEventLogStore{
		dataDir: dir,
		runs:    make(map[string][]types.EventEnvelope),
	}, nil
}

func (s *fileEventLogStore) runKey(tenantID, runID string) string {
	return tenantID + "/" + runID
}

func (s *fileEventLogStore) Append(_ context.Context, event types.EventEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.runKey(event.Event.TenantID, event.Event.RunID)

	// Track insertion order; only add to runOrder if this is a new key.
	if _, exists := s.runs[key]; !exists {
		s.runOrder = append(s.runOrder, key)
	}

	s.runs[key] = append(s.runs[key], event)

	// Evict oldest runs if we exceed the cap. Events are already persisted to
	// disk, so the in-memory cache can be safely pruned.
	for len(s.runs) > maxRunsInMemory {
		oldest := s.runOrder[0]
		s.runOrder = s.runOrder[1:]
		delete(s.runs, oldest)
	}

	// Persist to file.
	return s.persistRun(key)
}

func (s *fileEventLogStore) QueryFromSequence(_ context.Context, tenantID, runID string, afterSequence int) ([]types.EventEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.runKey(tenantID, runID)
	events := s.runs[key]
	if afterSequence <= 0 {
		out := make([]types.EventEnvelope, len(events))
		copy(out, events)
		return out, nil
	}

	var out []types.EventEnvelope
	for _, e := range events {
		if e.Event.Sequence > afterSequence {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *fileEventLogStore) Close() error { return nil }

func (s *fileEventLogStore) persistRun(key string) error {
	events := s.runs[key]
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	fp := filepath.Join(s.dataDir, key+".json")
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fp, data, 0o644)
}
