package quota

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockUsageRecorder is a test double for UsageRecorder.
type mockUsageRecorder struct {
	mu      sync.Mutex
	records []mockRecord
}

type mockRecord struct {
	tenantID  string
	tokens    int
	timestamp time.Time
}

func (m *mockUsageRecorder) Record(_ context.Context, tenantID string, tokens int, timestamp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, mockRecord{tenantID, tokens, timestamp})
	return nil
}

func (m *mockUsageRecorder) HourlyUsage(_ context.Context, tenantID string, hour time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hour.Truncate(time.Hour)
	total := 0
	for _, r := range m.records {
		if r.tenantID == tenantID && !r.timestamp.Before(h) && r.timestamp.Before(h.Add(time.Hour)) {
			total += r.tokens
		}
	}
	return total, nil
}

func TestTokenBudget_UnlimitedAlwaysAllows(t *testing.T) {
	tb := NewTokenBudget(0)
	if !tb.Check("tenant1") {
		t.Error("unlimited budget should always allow")
	}
	if !tb.Record("tenant1", 1000000) {
		t.Error("unlimited budget record should return true")
	}
	if tb.Remaining("tenant1") != -1 {
		t.Errorf("expected -1 for unlimited, got %d", tb.Remaining("tenant1"))
	}
}

func TestTokenBudget_EnforcesLimit(t *testing.T) {
	tb := NewTokenBudget(1000)

	if !tb.Check("t1") {
		t.Error("should allow with empty budget")
	}
	if tb.Remaining("t1") != 1000 {
		t.Errorf("expected 1000 remaining, got %d", tb.Remaining("t1"))
	}

	// Record 800 tokens.
	if !tb.Record("t1", 800) {
		t.Error("should be within budget after 800")
	}
	if tb.Remaining("t1") != 200 {
		t.Errorf("expected 200 remaining, got %d", tb.Remaining("t1"))
	}

	// Record 300 more — exceeds budget.
	if tb.Record("t1", 300) {
		t.Error("should report over budget after 1100")
	}
	if tb.Remaining("t1") != 0 {
		t.Errorf("expected 0 remaining, got %d", tb.Remaining("t1"))
	}

	// Check should return false.
	if tb.Check("t1") {
		t.Error("check should fail when over budget")
	}
}

func TestTokenBudget_TenantIsolation(t *testing.T) {
	tb := NewTokenBudget(500)

	tb.Record("a", 400)
	tb.Record("b", 100)

	if !tb.Check("a") {
		t.Error("tenant a should be within budget")
	}
	if !tb.Check("b") {
		t.Error("tenant b should be within budget")
	}

	tb.Record("a", 200) // a now at 600 — over
	if tb.Check("a") {
		t.Error("tenant a should be over budget")
	}
	if !tb.Check("b") {
		t.Error("tenant b should still be within budget")
	}
}

func TestTokenBudget_PersistentStore(t *testing.T) {
	store := &mockUsageRecorder{}

	// First budget instance records 600 tokens.
	tb1 := NewTokenBudget(1000).WithStore(store)
	tb1.Record("t1", 600)

	// Verify store received the record.
	store.mu.Lock()
	if len(store.records) != 1 || store.records[0].tokens != 600 {
		t.Fatalf("expected 1 record of 600, got %+v", store.records)
	}
	store.mu.Unlock()

	// Simulate restart: new budget instance with same store.
	tb2 := NewTokenBudget(1000).WithStore(store)

	// Should load existing usage from store (600), so remaining = 400.
	remaining := tb2.Remaining("t1")
	if remaining != 400 {
		t.Errorf("expected 400 remaining after reload, got %d", remaining)
	}

	// Record 500 more — should exceed.
	if tb2.Record("t1", 500) {
		t.Error("should be over budget after 1100 total")
	}
}

func TestTokenBudget_PersistentStore_UnlimitedStillPersists(t *testing.T) {
	store := &mockUsageRecorder{}
	tb := NewTokenBudget(0).WithStore(store)

	tb.Record("t1", 500)

	store.mu.Lock()
	if len(store.records) != 1 {
		t.Errorf("expected 1 persisted record even with unlimited budget, got %d", len(store.records))
	}
	store.mu.Unlock()
}

func TestTokenBudget_WindowResets(t *testing.T) {
	tb := NewTokenBudget(100)

	// Record up to limit.
	tb.Record("t1", 100)
	if tb.Check("t1") {
		t.Error("should be at limit")
	}

	// Manually expire the window.
	tb.mu.Lock()
	tb.windows["t1"].windowEnd = time.Now().Add(-1 * time.Second)
	tb.mu.Unlock()

	// Should reset and allow.
	if !tb.Check("t1") {
		t.Error("should allow after window expiry")
	}
	if tb.Remaining("t1") != 100 {
		t.Errorf("expected full budget after reset, got %d", tb.Remaining("t1"))
	}
}
