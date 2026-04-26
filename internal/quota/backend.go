package quota

// QuotaBackend abstracts the storage layer for quota enforcement.
// Implementations must be safe for concurrent use.
type QuotaBackend interface {
	// AllowQPS returns true if the tenant has at least 1 token available
	// in their rate-limit bucket (refilled at qps tokens/sec, capped at qps).
	AllowQPS(tenant string, qps int) bool

	// TryIncConcurrent atomically increments the tenant's concurrent counter
	// if it is below max. Returns true on success.
	TryIncConcurrent(tenant string, max int) bool

	// DecConcurrent decrements the tenant's concurrent counter (floor 0).
	DecConcurrent(tenant string)

	// WithInitialConcurrent sets starting concurrent counts (e.g. on restart).
	WithInitialConcurrent(counts map[string]int)
}
