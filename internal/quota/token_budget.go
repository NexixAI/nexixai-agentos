package quota

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// UsageRecorder persists and queries token usage.
// Implemented by postgres.UsageStore.
type UsageRecorder interface {
	Record(ctx context.Context, tenantID string, tokens int, timestamp time.Time) error
	HourlyUsage(ctx context.Context, tenantID string, hour time.Time) (int, error)
}

// TokenBudget enforces per-tenant hourly token usage limits.
// It uses a sliding window approach with 1-hour windows.
// If a UsageRecorder is set, usage is persisted and restored on window creation.
type TokenBudget struct {
	mu      sync.Mutex
	windows map[string]*tokenWindow
	limit   int // max tokens per hour per tenant (0 = unlimited)
	store   UsageRecorder
}

type tokenWindow struct {
	tokens    int
	windowEnd time.Time
}

// NewTokenBudget creates a TokenBudget with the given per-tenant hourly limit.
// A limit of 0 means unlimited.
func NewTokenBudget(limit int) *TokenBudget {
	return &TokenBudget{
		windows: make(map[string]*tokenWindow),
		limit:   limit,
	}
}

// NewTokenBudgetFromEnv creates a TokenBudget from environment configuration.
func NewTokenBudgetFromEnv() *TokenBudget {
	limit := envInt("AGENTOS_QUOTA_HOURLY_TOKENS", 0)
	return NewTokenBudget(limit)
}

// WithStore attaches a persistent usage recorder to the budget.
// When set, Record persists usage and new windows load prior usage from the store.
func (tb *TokenBudget) WithStore(store UsageRecorder) *TokenBudget {
	tb.store = store
	return tb
}

// Check returns true if the tenant has budget remaining.
// It does NOT consume tokens — use Record for that.
func (tb *TokenBudget) Check(tenant string) bool {
	if tb.limit <= 0 {
		return true
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()

	w := tb.getOrCreateWindow(tenant)
	return w.tokens < tb.limit
}

// Remaining returns the number of tokens remaining in the current window.
// Returns -1 if unlimited.
func (tb *TokenBudget) Remaining(tenant string) int {
	if tb.limit <= 0 {
		return -1
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()

	w := tb.getOrCreateWindow(tenant)
	rem := tb.limit - w.tokens
	if rem < 0 {
		return 0
	}
	return rem
}

// Record adds token usage for a tenant. Returns true if within budget,
// false if the budget is now exceeded (tokens are still recorded).
// If a UsageRecorder is set, the usage is also persisted.
func (tb *TokenBudget) Record(tenant string, tokens int) bool {
	if tb.limit <= 0 {
		// Still persist even if unlimited, for usage reporting.
		if tb.store != nil {
			if err := tb.store.Record(context.Background(), tenant, tokens, time.Now()); err != nil {
				slog.Error("failed to persist token usage", "tenant_id", tenant, "error", err)
			}
		}
		return true
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()

	w := tb.getOrCreateWindow(tenant)
	w.tokens += tokens

	if tb.store != nil {
		if err := tb.store.Record(context.Background(), tenant, tokens, time.Now()); err != nil {
			slog.Error("failed to persist token usage", "tenant_id", tenant, "error", err)
		}
	}

	return w.tokens <= tb.limit
}

// getOrCreateWindow returns the current window for a tenant, resetting if expired.
// If a UsageRecorder is set, loads the current hour's usage on window creation.
// Must be called with tb.mu held.
func (tb *TokenBudget) getOrCreateWindow(tenant string) *tokenWindow {
	w := tb.windows[tenant]
	now := time.Now()
	if w == nil || now.After(w.windowEnd) {
		// Delete the expired entry before creating a new one to avoid leaking
		// stale map entries (the old key is the same, but be explicit).
		delete(tb.windows, tenant)

		// Sweep other expired windows while we're here.
		for id, tw := range tb.windows {
			if now.After(tw.windowEnd) {
				delete(tb.windows, id)
			}
		}

		initialTokens := 0
		if tb.store != nil {
			if usage, err := tb.store.HourlyUsage(context.Background(), tenant, now); err == nil {
				initialTokens = usage
			} else {
				slog.Error("failed to load hourly usage", "tenant_id", tenant, "error", err)
			}
		}
		w = &tokenWindow{
			tokens:    initialTokens,
			windowEnd: now.Add(1 * time.Hour),
		}
		tb.windows[tenant] = w
	}
	return w
}
