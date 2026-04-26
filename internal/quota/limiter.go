package quota

import (
	"database/sql"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// Eviction constants for MemoryBackend maps.
const (
	bucketIdleTTL     = 1 * time.Hour
	limiterSweepEvery = 100 // sweep every N AllowQPS() calls
)

// Limiter is a per-tenant quota gate that delegates to a QuotaBackend.
type Limiter struct {
	backend       QuotaBackend
	qps           int
	maxConcurrent int
}

// NewFromEnv creates a Limiter backed by MemoryBackend (default).
// Environment variables control QPS and max-concurrent limits.
func NewFromEnv(qpsEnv, concurrentEnv string, defaultQPS, defaultConcurrent int) *Limiter {
	qps := envInt(qpsEnv, defaultQPS)
	conc := envInt(concurrentEnv, defaultConcurrent)
	return &Limiter{
		backend:       NewMemoryBackend(),
		qps:           qps,
		maxConcurrent: conc,
	}
}

// NewFromEnvWithDB creates a Limiter whose backend is selected by AGENTOS_QUOTA_BACKEND.
//   - "memory" or "" → MemoryBackend
//   - "postgres"     → PostgresBackend using the supplied *sql.DB
func NewFromEnvWithDB(qpsEnv, concurrentEnv string, defaultQPS, defaultConcurrent int, db *sql.DB) *Limiter {
	qps := envInt(qpsEnv, defaultQPS)
	conc := envInt(concurrentEnv, defaultConcurrent)

	var backend QuotaBackend
	switch os.Getenv("AGENTOS_QUOTA_BACKEND") {
	case "postgres":
		if db == nil {
			slog.Error("AGENTOS_QUOTA_BACKEND=postgres but no *sql.DB provided, falling back to memory")
			backend = NewMemoryBackend()
		} else {
			backend = NewPostgresBackend(db)
		}
	default:
		backend = NewMemoryBackend()
	}

	return &Limiter{
		backend:       backend,
		qps:           qps,
		maxConcurrent: conc,
	}
}

// EnvInt reads an integer from an environment variable with a default fallback.
func EnvInt(key string, def int) int {
	return envInt(key, def)
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil || i <= 0 {
		return def
	}
	return i
}

// AllowQPS returns true if a tenant is within its QPS budget.
func (l *Limiter) AllowQPS(tenant string) bool {
	return l.backend.AllowQPS(tenant, l.qps)
}

// TryIncConcurrent increments concurrent count if under max.
func (l *Limiter) TryIncConcurrent(tenant string) bool {
	return l.backend.TryIncConcurrent(tenant, l.maxConcurrent)
}

// DecConcurrent decrements concurrent count (floor at 0).
func (l *Limiter) DecConcurrent(tenant string) {
	l.backend.DecConcurrent(tenant)
}

// WithInitialConcurrent sets initial concurrent counts from existing data.
// This is used on startup to restore concurrent counters from in-progress runs.
func (l *Limiter) WithInitialConcurrent(counts map[string]int) {
	l.backend.WithInitialConcurrent(counts)
}

// ---- MemoryBackend ----

// MemoryBackend is an in-memory QuotaBackend using token-bucket rate limiting
// with opportunistic eviction of idle tenants.
type MemoryBackend struct {
	mu sync.Mutex

	buckets    map[string]*bucket // per-tenant token bucket
	concurrent map[string]int     // per-tenant concurrent counter
	allowCount int                // counts AllowQPS() calls for opportunistic sweep
}

type bucket struct {
	tokens     float64
	last       time.Time
	lastAccess time.Time
}

// NewMemoryBackend creates a MemoryBackend ready for use.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		buckets:    make(map[string]*bucket),
		concurrent: make(map[string]int),
	}
}

func (m *MemoryBackend) AllowQPS(tenant string, qps int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// Opportunistic sweep
	m.allowCount++
	if m.allowCount%limiterSweepEvery == 0 {
		m.sweepLocked(now)
	}

	b := m.buckets[tenant]
	if b == nil {
		b = &bucket{tokens: float64(qps), last: now, lastAccess: now}
		m.buckets[tenant] = b
	}
	dt := now.Sub(b.last).Seconds()
	b.last = now
	b.lastAccess = now

	// refill
	b.tokens += dt * float64(qps)
	if b.tokens > float64(qps) {
		b.tokens = float64(qps)
	}

	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

// sweepLocked removes buckets and concurrent entries not accessed within bucketIdleTTL.
// Must be called with mu held.
func (m *MemoryBackend) sweepLocked(now time.Time) {
	cutoff := now.Add(-bucketIdleTTL)
	for tenant, b := range m.buckets {
		if b.lastAccess.Before(cutoff) {
			delete(m.buckets, tenant)
		}
	}
	// Clean up concurrent entries that are at zero and have no corresponding bucket
	for tenant, count := range m.concurrent {
		if count <= 0 {
			if _, hasBucket := m.buckets[tenant]; !hasBucket {
				delete(m.concurrent, tenant)
			}
		}
	}
}

func (m *MemoryBackend) TryIncConcurrent(tenant string, max int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.concurrent[tenant]
	if cur >= max {
		return false
	}
	m.concurrent[tenant] = cur + 1
	return true
}

func (m *MemoryBackend) DecConcurrent(tenant string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.concurrent[tenant]
	cur--
	if cur < 0 {
		cur = 0
	}
	m.concurrent[tenant] = cur
}

func (m *MemoryBackend) WithInitialConcurrent(counts map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tenant, count := range counts {
		m.concurrent[tenant] = count
	}
}
