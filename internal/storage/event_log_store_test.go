package storage

import (
	"context"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func newTestEventLogStore(t *testing.T) EventLogStore {
	t.Helper()
	store, err := NewFileEventLogStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLogStore: %v", err)
	}
	return store
}

func makeEvent(tenantID, runID string, seq int) types.EventEnvelope {
	return types.EventEnvelope{
		Event: types.Event{
			EventID:  runID + "_evt_" + time.Now().Format("150405"),
			Sequence: seq,
			Time:     time.Now().UTC().Format(time.RFC3339),
			Type:     "step.started",
			TenantID: tenantID,
			RunID:    runID,
		},
	}
}

// ---------------------------------------------------------------------------
// Append + QueryFromSequence round-trip
// ---------------------------------------------------------------------------

func TestFileEventLogStore_AppendAndQueryRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newTestEventLogStore(t)

	e1 := makeEvent("t1", "run1", 1)
	e2 := makeEvent("t1", "run1", 2)
	e3 := makeEvent("t1", "run1", 3)

	for _, e := range []types.EventEnvelope{e1, e2, e3} {
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("Append seq=%d: %v", e.Event.Sequence, err)
		}
	}

	got, err := store.QueryFromSequence(ctx, "t1", "run1", 0)
	if err != nil {
		t.Fatalf("QueryFromSequence(0): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	for i, e := range got {
		if e.Event.Sequence != i+1 {
			t.Fatalf("event[%d] expected sequence %d, got %d", i, i+1, e.Event.Sequence)
		}
	}
}

// ---------------------------------------------------------------------------
// afterSequence filtering
// ---------------------------------------------------------------------------

func TestFileEventLogStore_AfterSequenceFiltering(t *testing.T) {
	ctx := context.Background()
	store := newTestEventLogStore(t)

	for seq := 1; seq <= 5; seq++ {
		e := makeEvent("t1", "run1", seq)
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("Append seq=%d: %v", seq, err)
		}
	}

	// After sequence 3: should return events 4 and 5
	got, err := store.QueryFromSequence(ctx, "t1", "run1", 3)
	if err != nil {
		t.Fatalf("QueryFromSequence(3): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events after seq 3, got %d", len(got))
	}
	if got[0].Event.Sequence != 4 {
		t.Fatalf("expected first result seq=4, got %d", got[0].Event.Sequence)
	}
	if got[1].Event.Sequence != 5 {
		t.Fatalf("expected second result seq=5, got %d", got[1].Event.Sequence)
	}
}

func TestFileEventLogStore_AfterSequenceBeyondAll(t *testing.T) {
	ctx := context.Background()
	store := newTestEventLogStore(t)

	for seq := 1; seq <= 3; seq++ {
		e := makeEvent("t1", "run1", seq)
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("Append seq=%d: %v", seq, err)
		}
	}

	// After sequence 10: should return nothing
	got, err := store.QueryFromSequence(ctx, "t1", "run1", 10)
	if err != nil {
		t.Fatalf("QueryFromSequence(10): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events after seq 10, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Empty results
// ---------------------------------------------------------------------------

func TestFileEventLogStore_QueryEmptyStore(t *testing.T) {
	ctx := context.Background()
	store := newTestEventLogStore(t)

	got, err := store.QueryFromSequence(ctx, "t1", "run1", 0)
	if err != nil {
		t.Fatalf("QueryFromSequence on empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events from empty store, got %d", len(got))
	}
}

func TestFileEventLogStore_QueryNonexistentRun(t *testing.T) {
	ctx := context.Background()
	store := newTestEventLogStore(t)

	// Add events for one run, query a different run
	e := makeEvent("t1", "run1", 1)
	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := store.QueryFromSequence(ctx, "t1", "run_other", 0)
	if err != nil {
		t.Fatalf("QueryFromSequence(nonexistent run): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events for nonexistent run, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Tenant isolation
// ---------------------------------------------------------------------------

func TestFileEventLogStore_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	store := newTestEventLogStore(t)

	e1 := makeEvent("t1", "run1", 1)
	e2 := makeEvent("t2", "run1", 1)

	if err := store.Append(ctx, e1); err != nil {
		t.Fatalf("Append t1: %v", err)
	}
	if err := store.Append(ctx, e2); err != nil {
		t.Fatalf("Append t2: %v", err)
	}

	got1, err := store.QueryFromSequence(ctx, "t1", "run1", 0)
	if err != nil {
		t.Fatalf("Query t1: %v", err)
	}
	got2, err := store.QueryFromSequence(ctx, "t2", "run1", 0)
	if err != nil {
		t.Fatalf("Query t2: %v", err)
	}

	if len(got1) != 1 || got1[0].Event.TenantID != "t1" {
		t.Fatalf("tenant isolation violated for t1: got %+v", got1)
	}
	if len(got2) != 1 || got2[0].Event.TenantID != "t2" {
		t.Fatalf("tenant isolation violated for t2: got %+v", got2)
	}
}

// ---------------------------------------------------------------------------
// QueryFromSequence returns a copy (not a reference to internal state)
// ---------------------------------------------------------------------------

func TestFileEventLogStore_QueryReturnsCopy(t *testing.T) {
	ctx := context.Background()
	store := newTestEventLogStore(t)

	e := makeEvent("t1", "run1", 1)
	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := store.QueryFromSequence(ctx, "t1", "run1", 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	// Mutate the returned slice
	got[0].Event.Sequence = 999

	// Re-query should still return original data
	got2, err := store.QueryFromSequence(ctx, "t1", "run1", 0)
	if err != nil {
		t.Fatalf("Query2: %v", err)
	}
	if got2[0].Event.Sequence != 1 {
		t.Fatalf("internal state was mutated: expected seq=1, got %d", got2[0].Event.Sequence)
	}
}

// ---------------------------------------------------------------------------
// Close is idempotent
// ---------------------------------------------------------------------------

func TestFileEventLogStore_Close(t *testing.T) {
	store := newTestEventLogStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Double close should not error
	if err := store.Close(); err != nil {
		t.Fatalf("Close (second): %v", err)
	}
}
