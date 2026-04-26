package quota

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

// VERIFIED: modelpolicy/usage.go already uses time.Now().UTC() for all
// time-bucketed usage keys (Record on line 61, CheckBudget on line 82,
// GetUsage on line 110). No changes needed.

// ---------------------------------------------------------------------------
// Helper: create a Limiter without relying on environment variables.
// ---------------------------------------------------------------------------

func newTestLimiter(qps, maxConcurrent int) *Limiter {
	return &Limiter{
		backend:       NewMemoryBackend(),
		qps:           qps,
		maxConcurrent: maxConcurrent,
	}
}

// memBackend extracts the *MemoryBackend from a test Limiter for internal assertions.
func memBackend(l *Limiter) *MemoryBackend {
	return l.backend.(*MemoryBackend)
}

// ===========================================================================
// Functional tests – AllowQPS
// ===========================================================================

func TestAllowQPS_UnderBudget(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(10, 1)

	// A fresh limiter starts with a full bucket, so the first call must succeed.
	if !lim.AllowQPS("tenant-a") {
		t.Fatal("expected AllowQPS to return true when under budget")
	}
}

func TestAllowQPS_ExceedsBudget(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(5, 1)

	// Drain the bucket: 5 tokens available at start.
	for i := 0; i < 5; i++ {
		if !lim.AllowQPS("tenant-b") {
			t.Fatalf("expected AllowQPS to return true on call %d", i+1)
		}
	}

	// Next call should be rejected – no tokens left, no time to refill.
	if lim.AllowQPS("tenant-b") {
		t.Fatal("expected AllowQPS to return false when budget exceeded")
	}
}

func TestAllowQPS_TenantIsolation(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(1, 1)

	// Drain tenant-1's bucket.
	if !lim.AllowQPS("tenant-1") {
		t.Fatal("first call for tenant-1 should succeed")
	}
	if lim.AllowQPS("tenant-1") {
		t.Fatal("second call for tenant-1 should fail (budget=1)")
	}

	// tenant-2 must still have its own full bucket.
	if !lim.AllowQPS("tenant-2") {
		t.Fatal("first call for tenant-2 should succeed (independent bucket)")
	}
}

// ===========================================================================
// Functional tests – TryIncConcurrent / DecConcurrent
// ===========================================================================

func TestTryIncConcurrent_AtMax(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(10, 2)

	if !lim.TryIncConcurrent("t") {
		t.Fatal("first inc should succeed")
	}
	if !lim.TryIncConcurrent("t") {
		t.Fatal("second inc should succeed (max=2)")
	}
	if lim.TryIncConcurrent("t") {
		t.Fatal("third inc should fail (at max)")
	}
}

func TestDecConcurrent_FloorsAtZero(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(10, 2)

	// Decrement without any prior increment – must not panic and must floor at 0.
	lim.DecConcurrent("t")
	lim.DecConcurrent("t")

	// After flooring, a new increment should still succeed.
	if !lim.TryIncConcurrent("t") {
		t.Fatal("TryIncConcurrent should succeed after DecConcurrent floored at 0")
	}
}

func TestConcurrent_IncDecSymmetry(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(10, 100)
	const tenant = "sym"

	for i := 0; i < 50; i++ {
		if !lim.TryIncConcurrent(tenant) {
			t.Fatalf("inc %d should succeed (max=100)", i)
		}
	}
	for i := 0; i < 50; i++ {
		lim.DecConcurrent(tenant)
	}

	// All 100 slots should now be available again.
	for i := 0; i < 100; i++ {
		if !lim.TryIncConcurrent(tenant) {
			t.Fatalf("inc %d should succeed after full release", i)
		}
	}
}

// ===========================================================================
// Concurrency stress tests – designed for go test -race
// ===========================================================================

func TestStress_AllowQPS_100Goroutines(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(1000, 1)
	const goroutines = 100
	const callsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	var allowed atomic.Int64
	var denied atomic.Int64

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			tenant := fmt.Sprintf("stress-tenant-%d", id%5) // 5 tenants
			for c := 0; c < callsPerGoroutine; c++ {
				if lim.AllowQPS(tenant) {
					allowed.Add(1)
				} else {
					denied.Add(1)
				}
			}
		}(g)
	}

	wg.Wait()

	total := allowed.Load() + denied.Load()
	if total != goroutines*callsPerGoroutine {
		t.Fatalf("expected %d total calls, got %d", goroutines*callsPerGoroutine, total)
	}
	t.Logf("AllowQPS stress: %d allowed, %d denied out of %d total", allowed.Load(), denied.Load(), total)
}

func TestStress_TryIncDecConcurrent_100Goroutines(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(10, 1000) // high max so most increments succeed
	const tenant = "stress-conc"

	var wg sync.WaitGroup
	wg.Add(goroutines100)

	for g := 0; g < goroutines100; g++ {
		go func() {
			defer wg.Done()
			// Each goroutine increments then decrements, so net effect is zero.
			if lim.TryIncConcurrent(tenant) {
				lim.DecConcurrent(tenant)
			}
		}()
	}

	wg.Wait()

	// After all goroutines complete, the concurrent count must be exactly 0.
	mb := memBackend(lim)
	mb.mu.Lock()
	final := mb.concurrent[tenant]
	mb.mu.Unlock()

	if final != 0 {
		t.Fatalf("expected concurrent count to be 0 after balanced inc/dec, got %d", final)
	}
}

const goroutines100 = 100

func TestStress_MixedOps_100Goroutines(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(500, 50)
	const tenant = "stress-mixed"

	var wg sync.WaitGroup
	wg.Add(goroutines100)

	for g := 0; g < goroutines100; g++ {
		go func(id int) {
			defer wg.Done()
			// Mix QPS checks and concurrent slot acquire/release.
			for i := 0; i < 20; i++ {
				_ = lim.AllowQPS(tenant)
				if lim.TryIncConcurrent(tenant) {
					lim.DecConcurrent(tenant)
				}
			}
		}(g)
	}

	wg.Wait()

	mb := memBackend(lim)
	mb.mu.Lock()
	final := mb.concurrent[tenant]
	mb.mu.Unlock()

	if final != 0 {
		t.Fatalf("expected concurrent count 0 after mixed stress, got %d", final)
	}
}

// ===========================================================================
// NewFromEnv – basic smoke test (does not depend on real env vars)
// ===========================================================================

func TestNewFromEnv_Defaults(t *testing.T) {
	t.Parallel()
	// Use env var names that are extremely unlikely to be set.
	lim := NewFromEnv(
		"AGENTOS_TEST_QPS_UNLIKELY_SET_9999",
		"AGENTOS_TEST_CONC_UNLIKELY_SET_9999",
		7, 3,
	)

	if lim.qps != 7 {
		t.Fatalf("expected default qps=7, got %d", lim.qps)
	}
	if lim.maxConcurrent != 3 {
		t.Fatalf("expected default maxConcurrent=3, got %d", lim.maxConcurrent)
	}
}

// ===========================================================================
// Backend selection tests
// ===========================================================================

func TestBackendSelection_Default(t *testing.T) {
	t.Setenv("AGENTOS_QUOTA_BACKEND", "")
	l := NewFromEnvWithDB("TEST_QPS_BS_1", "TEST_CONC_BS_1", 10, 5, nil)
	if _, ok := l.backend.(*MemoryBackend); !ok {
		t.Fatalf("expected MemoryBackend, got %T", l.backend)
	}
}

func TestBackendSelection_Memory(t *testing.T) {
	t.Setenv("AGENTOS_QUOTA_BACKEND", "memory")
	l := NewFromEnvWithDB("TEST_QPS_BS_2", "TEST_CONC_BS_2", 10, 5, nil)
	if _, ok := l.backend.(*MemoryBackend); !ok {
		t.Fatalf("expected MemoryBackend, got %T", l.backend)
	}
}

func TestBackendSelection_PostgresFallbackNilDB(t *testing.T) {
	t.Setenv("AGENTOS_QUOTA_BACKEND", "postgres")
	// nil db → should fall back to MemoryBackend with a logged error.
	l := NewFromEnvWithDB("TEST_QPS_BS_3", "TEST_CONC_BS_3", 10, 5, nil)
	if _, ok := l.backend.(*MemoryBackend); !ok {
		t.Fatalf("expected MemoryBackend fallback, got %T", l.backend)
	}
}

func TestNewFromEnv_UsesMemoryBackend(t *testing.T) {
	t.Parallel()
	// Original constructor always uses MemoryBackend.
	lim := NewFromEnv("TEST_QPS_BS_4", "TEST_CONC_BS_4", 10, 5)
	if _, ok := lim.backend.(*MemoryBackend); !ok {
		t.Fatalf("expected MemoryBackend, got %T", lim.backend)
	}
}

func TestMemoryBackend_ImplementsInterface(t *testing.T) {
	var _ QuotaBackend = (*MemoryBackend)(nil)
}

func TestPostgresBackend_ImplementsInterface(t *testing.T) {
	var _ QuotaBackend = (*PostgresBackend)(nil)
}

// ===========================================================================
// WithInitialConcurrent
// ===========================================================================

func TestWithInitialConcurrent(t *testing.T) {
	t.Parallel()
	lim := newTestLimiter(10, 3)
	lim.WithInitialConcurrent(map[string]int{"t1": 2})

	// Only 1 more should fit (max=3, initial=2).
	if !lim.TryIncConcurrent("t1") {
		t.Fatal("should allow 3rd")
	}
	if lim.TryIncConcurrent("t1") {
		t.Fatal("should deny 4th")
	}
}

// ===========================================================================
// envInt
// ===========================================================================

func TestEnvInt(t *testing.T) {
	t.Parallel()

	// Unset → default
	os.Unsetenv("AGENTOS_TEST_ENVINT_1")
	if v := envInt("AGENTOS_TEST_ENVINT_1", 42); v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
}
