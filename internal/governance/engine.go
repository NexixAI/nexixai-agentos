package governance

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Engine evaluates every tool call against the loaded governance policy.
// Evaluation order: KillSwitch -> Identity -> Segmentation -> Behavior -> Allow.
// Fail-closed: any internal error results in Deny.
type Engine struct {
	cfg            *GovernanceConfig
	killSwitch     *KillSwitch
	rateLimiter    RateLimiter
	loopDetector   LoopDetector
	circuitBreaker CircuitBreakerTracker
}

// NewEngine creates a governance engine from the config at the given path.
// Returns an error if the config cannot be loaded — fail-closed by design.
func NewEngine(configPath string) (*Engine, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return NewEngineFromConfig(cfg), nil
}

// NewEngineFromConfig creates a governance engine from an already-loaded config.
func NewEngineFromConfig(cfg *GovernanceConfig) *Engine {
	return &Engine{
		cfg:            cfg,
		killSwitch:     NewKillSwitch(),
		rateLimiter:    NewInMemoryRateLimiter(),
		loopDetector:   NewInMemoryLoopDetector(),
		circuitBreaker: NewInMemoryCircuitBreaker(),
	}
}

// Config returns the loaded governance configuration. Read-only.
func (e *Engine) Config() *GovernanceConfig {
	return e.cfg
}

// KillSwitch returns the engine's kill switch for freeze/unfreeze operations.
func (e *Engine) KillSwitch() *KillSwitch {
	return e.killSwitch
}

// Check evaluates whether the given agent is allowed to call the given tool
// with the given arguments. Returns Allow or Deny(reason).
//
// Evaluation order:
//  0. Kill switch — deny frozen agents immediately
//  1. Identity — resolve agent, deny unknown agents
//  2. Segmentation — check allowed/denied actions, scope
//  3. Behavior — rate limit, loop detection, circuit breaker
//
// Fail-closed: any error in evaluation returns Deny.
// CheckOpts holds optional parameters for governance Check.
type CheckOpts struct {
	Category ToolCategory // "observe" or "action"; defaults to "action"
}

func (e *Engine) Check(ctx context.Context, agentID string, toolName string, args map[string]any, opts ...CheckOpts) (*Decision, error) {
	// Kill switch — deny frozen agents before any other evaluation.
	if frozen, reason := e.killSwitch.IsFrozen(agentID); frozen {
		return &Decision{
			Verdict: Deny,
			Reason:  fmt.Sprintf("agent frozen: %s", reason),
			Domain:  "killswitch",
		}, nil
	}

	// Domain 1: Identity — resolve agent role, deny unknown agents.
	agent, ok := e.cfg.Agents[agentID]
	if !ok {
		return &Decision{
			Verdict: Deny,
			Reason:  fmt.Sprintf("unknown agent %q", agentID),
			Domain:  "identity",
		}, nil
	}

	_ = ctx // reserved for future context-based checks (e.g., tracing, deadlines)

	slog.Debug("governance: identity resolved",
		"agent", agentID,
		"role", agent.Role,
		"clearance", agent.Clearance,
	)

	// Domain 4: Segmentation — check allowed/denied actions and scope.
	if decision := e.checkSegmentation(agentID, toolName, args); decision != nil {
		return decision, nil
	}

	// Domain 2: Behavior — rate limit, loop detection, circuit breaker.
	category := CategoryAction
	if len(opts) > 0 && opts[0].Category != "" {
		category = opts[0].Category
	}

	if decision := e.checkBehavior(agentID, toolName, category, args); decision != nil {
		return decision, nil
	}

	return &Decision{Verdict: Allow}, nil
}

// checkSegmentation evaluates Domain 4 segmentation policy for the agent.
// Returns nil if the call is allowed by segmentation rules.
func (e *Engine) checkSegmentation(agentID string, toolName string, args map[string]any) *Decision {
	seg, ok := e.cfg.Segments[agentID]
	if !ok {
		// No segmentation rules for this agent — the agent is known (passed identity)
		// but has no restrictions. Fail-closed: deny.
		return &Decision{
			Verdict: Deny,
			Reason:  fmt.Sprintf("no segmentation policy defined for agent %q", agentID),
			Domain:  "segmentation",
		}
	}

	// Check denied actions first — explicit deny overrides allowed.
	for _, denied := range seg.DeniedActions {
		if denied == toolName {
			return &Decision{
				Verdict: Deny,
				Reason:  fmt.Sprintf("action %q is explicitly denied for agent %q", toolName, agentID),
				Domain:  "segmentation",
			}
		}
	}

	// Check allowed actions — must be in the allow list.
	allowed := false
	for _, a := range seg.AllowedActions {
		if a == toolName {
			allowed = true
			break
		}
	}
	if !allowed {
		return &Decision{
			Verdict: Deny,
			Reason:  fmt.Sprintf("action %q is not in allowed actions for agent %q", toolName, agentID),
			Domain:  "segmentation",
		}
	}

	// Check protected targets — if the tool targets a protected service,
	// the destructive_action_gate applies (noted but not enforced here;
	// gate enforcement is a middleware concern for Issue #2).
	if target, ok := args["target"].(string); ok && len(seg.ProtectedTargets) > 0 {
		for _, pt := range seg.ProtectedTargets {
			if pt == target {
				slog.Debug("governance: protected target hit",
					"agent", agentID,
					"tool", toolName,
					"target", target,
					"gate", seg.ProtectedGate,
				)
				// Protected targets are logged but allowed through segmentation.
				// The destructive_action_gate handles the actual double-confirmation.
				break
			}
		}
	}

	return nil // allowed
}

// checkBehavior evaluates Domain 2 behavioral limits.
// Returns nil if the call is within behavioral limits.
func (e *Engine) checkBehavior(agentID string, toolName string, category ToolCategory, args map[string]any) *Decision {
	now := time.Now()

	// Circuit breaker — check if agent is frozen.
	if e.circuitBreaker.IsFrozen(agentID) {
		return &Decision{
			Verdict: Deny,
			Reason:  fmt.Sprintf("agent %q is frozen by circuit breaker", agentID),
			Domain:  "behavior",
		}
	}

	// Rate limits.
	if limits, ok := e.cfg.Behavior.RateLimits[agentID]; ok {
		if reason := e.rateLimiter.Check(agentID, category, limits, now); reason != "" {
			return &Decision{
				Verdict: Deny,
				Reason:  reason,
				Domain:  "behavior",
			}
		}
	}

	// Loop detection.
	target := ""
	if t, ok := args["target"].(string); ok {
		target = t
	}
	if target != "" {
		windowDur := parseWindow(e.cfg.Behavior.LoopDetection.Window)
		maxAttempts := e.cfg.Behavior.LoopDetection.MaxAttemptsPerTarget
		if maxAttempts > 0 {
			if reason := e.loopDetector.Check(agentID, toolName, target, maxAttempts, windowDur, now); reason != "" {
				return &Decision{
					Verdict: Deny,
					Reason:  reason,
					Domain:  "behavior",
				}
			}
		}
	}

	// Record the action for future rate-limit and loop-detection checks.
	e.rateLimiter.RecordAction(agentID, category, now)
	if target != "" {
		e.loopDetector.RecordAttempt(agentID, toolName, target, now)
	}

	return nil // within behavioral limits
}

// RecordResult records the success/failure of a tool call for circuit breaker tracking.
// Should be called after the tool call completes.
func (e *Engine) RecordResult(agentID string, success bool) {
	if success {
		e.circuitBreaker.RecordSuccess(agentID)
	} else {
		count := e.circuitBreaker.RecordFailure(agentID)
		threshold := e.cfg.Behavior.CircuitBreaker.ConsecutiveFailures
		if threshold > 0 && count >= threshold {
			slog.Warn("governance: circuit breaker triggered — freezing agent",
				"agent", agentID,
				"failures", count,
				"threshold", threshold,
			)
			e.circuitBreaker.Freeze(agentID)
		}
	}
}

// parseWindow parses a duration string like "1h", "30m", etc.
// Returns 1 hour as default if parsing fails.
func parseWindow(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Hour
	}
	return d
}

// --- In-memory implementations ---

// InMemoryRateLimiter tracks action timestamps per agent with separate
// observe and action buckets for split rate limiting.
type InMemoryRateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time // "agentID:category" -> timestamps
}

// NewInMemoryRateLimiter creates a new in-memory rate limiter.
func NewInMemoryRateLimiter() *InMemoryRateLimiter {
	return &InMemoryRateLimiter{
		buckets: make(map[string][]time.Time),
	}
}

func rateBucketKey(agentID string, category ToolCategory) string {
	return agentID + ":" + string(category)
}

const maxRateBuckets = 10000 // cap to prevent unbounded memory growth (H-5)

// RecordAction records that an agent performed an action.
func (r *InMemoryRateLimiter) RecordAction(agentID string, category ToolCategory, t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := rateBucketKey(agentID, category)
	r.buckets[key] = append(r.buckets[key], t)

	// Evict oldest buckets if we exceed the cap.
	if len(r.buckets) > maxRateBuckets {
		r.evictOldestBucketsLocked()
	}
}

// evictOldestBucketsLocked removes the 10% oldest buckets. Caller must hold mu.
func (r *InMemoryRateLimiter) evictOldestBucketsLocked() {
	toRemove := len(r.buckets) / 10
	if toRemove < 1 {
		toRemove = 1
	}
	// Find keys with oldest last-access timestamps.
	type entry struct {
		key    string
		latest time.Time
	}
	entries := make([]entry, 0, len(r.buckets))
	for k, ts := range r.buckets {
		var latest time.Time
		for _, t := range ts {
			if t.After(latest) {
				latest = t
			}
		}
		entries = append(entries, entry{k, latest})
	}
	// Simple selection: delete the N oldest.
	for i := 0; i < toRemove && i < len(entries); i++ {
		oldest := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].latest.Before(entries[oldest].latest) {
				oldest = j
			}
		}
		entries[i], entries[oldest] = entries[oldest], entries[i]
		delete(r.buckets, entries[i].key)
	}
}

// Check checks whether the agent has exceeded rate limits.
func (r *InMemoryRateLimiter) Check(agentID string, category ToolCategory, limits RateLimit, now time.Time) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := rateBucketKey(agentID, category)
	timestamps := r.buckets[key]
	hourAgo := now.Add(-time.Hour)
	dayAgo := now.Add(-24 * time.Hour)

	var hourCount int
	alive := timestamps[:0]
	for _, t := range timestamps {
		if t.After(dayAgo) {
			alive = append(alive, t)
			if t.After(hourAgo) {
				hourCount++
			}
		}
	}
	r.buckets[key] = alive

	// Split limits: use category-specific limit if set, else combined PerHour.
	var hourLimit int
	if category == CategoryObserve && limits.ObservePerHour > 0 {
		hourLimit = limits.ObservePerHour
	} else if category == CategoryAction && limits.ActionPerHour > 0 {
		hourLimit = limits.ActionPerHour
	} else {
		hourLimit = limits.PerHour
	}

	if hourLimit > 0 && hourCount >= hourLimit {
		return fmt.Sprintf("agent %q exceeded %s hourly rate limit (%d/%d)", agentID, category, hourCount, hourLimit)
	}

	// Daily limit is combined across all categories.
	if limits.PerDay > 0 {
		dayCount := 0
		for _, cat := range []ToolCategory{CategoryObserve, CategoryAction} {
			for _, t := range r.buckets[rateBucketKey(agentID, cat)] {
				if t.After(dayAgo) {
					dayCount++
				}
			}
		}
		if dayCount >= limits.PerDay {
			return fmt.Sprintf("agent %q exceeded daily rate limit (%d/%d)", agentID, dayCount, limits.PerDay)
		}
	}

	return ""
}

// InMemoryLoopDetector tracks (agent, action, target) tuples in memory.
type InMemoryLoopDetector struct {
	mu       sync.Mutex
	attempts map[string][]time.Time // "agentID|action|target" -> timestamps
}

// NewInMemoryLoopDetector creates a new in-memory loop detector.
func NewInMemoryLoopDetector() *InMemoryLoopDetector {
	return &InMemoryLoopDetector{
		attempts: make(map[string][]time.Time),
	}
}

func loopKey(agentID, action, target string) string {
	return agentID + "|" + action + "|" + target
}

const maxLoopBuckets = 10000 // cap to prevent unbounded memory growth (H-6)

// RecordAttempt records an action attempt.
func (d *InMemoryLoopDetector) RecordAttempt(agentID, action, target string, t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := loopKey(agentID, action, target)
	d.attempts[key] = append(d.attempts[key], t)

	// Evict stale entries if we exceed the cap.
	if len(d.attempts) > maxLoopBuckets {
		cutoff := t.Add(-24 * time.Hour)
		for k, ts := range d.attempts {
			var alive []time.Time
			for _, tt := range ts {
				if tt.After(cutoff) {
					alive = append(alive, tt)
				}
			}
			if len(alive) == 0 {
				delete(d.attempts, k)
			} else {
				d.attempts[k] = alive
			}
		}
	}
}

// Check checks whether a loop has been detected.
func (d *InMemoryLoopDetector) Check(agentID, action, target string, maxAttempts int, window time.Duration, now time.Time) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := loopKey(agentID, action, target)
	timestamps := d.attempts[key]
	cutoff := now.Add(-window)

	var count int
	alive := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			alive = append(alive, t)
			count++
		}
	}
	d.attempts[key] = alive

	if count >= maxAttempts {
		return fmt.Sprintf("loop detected: agent %q attempted %q on %q %d times in %v (limit %d)",
			agentID, action, target, count, window, maxAttempts)
	}
	return ""
}

// InMemoryCircuitBreaker tracks consecutive failures per agent in memory.
type InMemoryCircuitBreaker struct {
	mu       sync.Mutex
	failures map[string]int  // agentID -> consecutive failure count
	frozen   map[string]bool // agentID -> frozen state
}

// NewInMemoryCircuitBreaker creates a new in-memory circuit breaker tracker.
func NewInMemoryCircuitBreaker() *InMemoryCircuitBreaker {
	return &InMemoryCircuitBreaker{
		failures: make(map[string]int),
		frozen:   make(map[string]bool),
	}
}

// RecordSuccess resets the failure counter.
func (cb *InMemoryCircuitBreaker) RecordSuccess(agentID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures[agentID] = 0
}

// RecordFailure increments and returns the failure count.
func (cb *InMemoryCircuitBreaker) RecordFailure(agentID string) int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures[agentID]++
	return cb.failures[agentID]
}

// ConsecutiveFailures returns the current failure count.
func (cb *InMemoryCircuitBreaker) ConsecutiveFailures(agentID string) int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failures[agentID]
}

// IsFrozen returns true if the agent is frozen.
func (cb *InMemoryCircuitBreaker) IsFrozen(agentID string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.frozen[agentID]
}

// Freeze marks the agent as frozen.
func (cb *InMemoryCircuitBreaker) Freeze(agentID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.frozen[agentID] = true
}
