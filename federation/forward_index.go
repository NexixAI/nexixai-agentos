package federation

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Eviction constants for the forward index.
const (
	maxForwardEntries = 10000
	forwardEntryTTL   = 24 * time.Hour
	forwardSweepEvery = 50 // sweep every N Set() calls
)

type forwardKey struct {
	TenantID string
	RunID    string
}

type forwardTarget struct {
	RemoteStackID   string
	RemoteEventsURL string
	createdAt       time.Time
}

type forwardIndex struct {
	mu       sync.Mutex
	m        map[forwardKey]forwardTarget
	path     string // if non-empty, persist on Set
	setCount int    // counts Set() calls for opportunistic sweep
}

type forwardIndexRecord struct {
	TenantID        string `json:"tenant_id"`
	RunID           string `json:"run_id"`
	RemoteStackID   string `json:"remote_stack_id"`
	RemoteEventsURL string `json:"remote_events_url"`
}

func newForwardIndex() *forwardIndex {
	return &forwardIndex{m: make(map[forwardKey]forwardTarget)}
}

// newForwardIndexPersistent creates a forward index that persists to a JSON file on Set().
// This is a minimal "production-ish" improvement to avoid losing forward mappings on restarts.
func newForwardIndexPersistent(path string) *forwardIndex {
	i := &forwardIndex{m: make(map[forwardKey]forwardTarget), path: path}
	if err := i.load(); err != nil {
		slog.Warn("failed to load forward index, starting empty", "path", path, "error", err)
	}
	return i
}

func (i *forwardIndex) load() error {
	if i.path == "" {
		return nil
	}
	b, err := os.ReadFile(i.path)
	if err != nil {
		return nil // file doesn't exist is fine
	}
	var recs []forwardIndexRecord
	if err := json.Unmarshal(b, &recs); err != nil {
		return err
	}
	now := time.Now()
	for _, r := range recs {
		i.m[forwardKey{TenantID: r.TenantID, RunID: r.RunID}] = forwardTarget{
			RemoteStackID:   r.RemoteStackID,
			RemoteEventsURL: r.RemoteEventsURL,
			createdAt:       now,
		}
	}
	return nil
}

func (i *forwardIndex) persistLocked() error {
	if i.path == "" {
		return nil
	}
	dir := filepath.Dir(i.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	recs := make([]forwardIndexRecord, 0, len(i.m))
	for k, v := range i.m {
		recs = append(recs, forwardIndexRecord{
			TenantID: k.TenantID, RunID: k.RunID, RemoteStackID: v.RemoteStackID, RemoteEventsURL: v.RemoteEventsURL,
		})
	}
	b, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, i.path)
}

func (i *forwardIndex) Set(tenantID, runID, remoteStackID, remoteEventsURL string) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.m[forwardKey{TenantID: tenantID, RunID: runID}] = forwardTarget{
		RemoteStackID:   remoteStackID,
		RemoteEventsURL: remoteEventsURL,
		createdAt:       time.Now(),
	}

	// Opportunistic sweep
	i.setCount++
	if i.setCount%forwardSweepEvery == 0 {
		i.sweepExpiredLocked()
	}

	// Size cap safety net
	if len(i.m) > maxForwardEntries {
		i.evictOldestLocked()
	}

	if err := i.persistLocked(); err != nil {
		slog.Error("forward index persist failed", "error", err)
	}
}

// sweepExpiredLocked removes entries older than forwardEntryTTL. Must be called with mu held.
func (i *forwardIndex) sweepExpiredLocked() {
	cutoff := time.Now().Add(-forwardEntryTTL)
	for k, v := range i.m {
		if !v.createdAt.IsZero() && v.createdAt.Before(cutoff) {
			delete(i.m, k)
		}
	}
}

// evictOldestLocked removes the oldest entry by createdAt. Must be called with mu held.
func (i *forwardIndex) evictOldestLocked() {
	var oldestKey forwardKey
	var oldestTime time.Time
	first := true
	for k, v := range i.m {
		if first || v.createdAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.createdAt
			first = false
		}
	}
	if !first {
		delete(i.m, oldestKey)
	}
}

func (i *forwardIndex) Get(tenantID, runID string) (forwardTarget, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	v, ok := i.m[forwardKey{TenantID: tenantID, RunID: runID}]
	return v, ok
}
