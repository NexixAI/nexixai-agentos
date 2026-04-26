package federation

import (
	"encoding/json"
	"strconv"
	"sync"
)

// Eviction constants for the event store.
const (
	maxStoredRuns    = 500
	maxSeenEventIDs = 10000
)

type storeKey struct {
	TenantID string
	RunID    string
}

type storedEvents struct {
	seenEventIDs map[string]struct{}
	lastSequence int
	events       []map[string]any // raw event envelopes
}

type eventStore struct {
	mu       sync.Mutex
	m        map[storeKey]*storedEvents
	runOrder []storeKey // insertion order for LRU eviction
}

func newEventStore() *eventStore {
	return &eventStore{m: make(map[storeKey]*storedEvents)}
}

func (s *eventStore) Ingest(tenantID, runID string, envelopes []map[string]any) (accepted int, rejected int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := storeKey{TenantID: tenantID, RunID: runID}
	st := s.m[k]
	if st == nil {
		st = &storedEvents{seenEventIDs: make(map[string]struct{})}
		s.m[k] = st
		s.runOrder = append(s.runOrder, k)
	}

	for _, env := range envelopes {
		eventObj, ok := env["event"].(map[string]any)
		if !ok {
			rejected++
			continue
		}
		eid, _ := eventObj["event_id"].(string)
		if eid == "" {
			rejected++
			continue
		}

		seq := eventSequence(eventObj)
		if seq != 0 && seq <= st.lastSequence {
			// enforce monotonic ordering
			rejected++
			continue
		}
		if _, exists := st.seenEventIDs[eid]; exists {
			rejected++
			continue
		}

		// Cap seenEventIDs per run
		if len(st.seenEventIDs) >= maxSeenEventIDs {
			// Drop oldest entries (arbitrary eviction since map has no order)
			count := 0
			for old := range st.seenEventIDs {
				delete(st.seenEventIDs, old)
				count++
				if count >= maxSeenEventIDs/10 {
					break
				}
			}
		}

		st.seenEventIDs[eid] = struct{}{}
		if seq > st.lastSequence {
			st.lastSequence = seq
		}
		st.events = append(st.events, env)
		accepted++
	}

	// Evict oldest runs if we exceed the cap
	for len(s.m) > maxStoredRuns {
		oldest := s.runOrder[0]
		s.runOrder = s.runOrder[1:]
		delete(s.m, oldest)
	}

	return accepted, rejected
}

func (s *eventStore) List(tenantID, runID string) ([]map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.m[storeKey{TenantID: tenantID, RunID: runID}]
	if st == nil {
		return nil, false
	}
	// copy
	out := make([]map[string]any, 0, len(st.events))
	out = append(out, st.events...)
	return out, true
}

func (s *eventStore) ListFromSequence(tenantID, runID string, fromSequence int) ([]map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.m[storeKey{TenantID: tenantID, RunID: runID}]
	if st == nil {
		return nil, false
	}

	out := make([]map[string]any, 0, len(st.events))
	for _, env := range st.events {
		eventObj, _ := env["event"].(map[string]any)
		seq := eventSequence(eventObj)
		if seq == 0 || seq > fromSequence {
			out = append(out, env)
		}
	}
	return out, true
}

func eventSequence(eventObj map[string]any) int {
	if eventObj == nil {
		return 0
	}
	switch v := eventObj["sequence"].(type) {
	case json.Number:
		if i, err := strconv.Atoi(v.String()); err == nil {
			return i
		}
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return 0
}
