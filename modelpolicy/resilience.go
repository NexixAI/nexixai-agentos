package modelpolicy

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// RetryConfig configures the retry behavior for model provider calls.
type RetryConfig struct {
	MaxAttempts  int           // Maximum number of attempts (1 = no retry)
	BaseBackoff  time.Duration // Initial backoff duration
	MaxBackoff   time.Duration // Maximum backoff duration
	BackoffScale float64       // Multiplier per retry (default 2.0)
}

// DefaultRetryConfig returns a sensible default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		BaseBackoff:  500 * time.Millisecond,
		MaxBackoff:   30 * time.Second,
		BackoffScale: 2.0,
	}
}

// CircuitState represents the current state of the circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Blocking requests
	CircuitHalfOpen                     // Probing with single request
)

// CircuitBreakerConfig configures the circuit breaker behavior.
type CircuitBreakerConfig struct {
	FailureThreshold int           // Failures before opening (default 8, v11.5 retune)
	RecoveryTimeout  time.Duration // Time before half-open probe (default 60s)
	HalfOpenMax      int           // Max concurrent probes in half-open (default 2)
}

// DefaultCircuitBreakerConfig returns a sensible default configuration,
// overridable via environment variables:
//
//	AGENTOS_CB_FAILURE_THRESHOLD  — failures before opening (default 8)
//	AGENTOS_CB_RECOVERY_TIMEOUT   — seconds before half-open probe (default 60)
//	AGENTOS_CB_HALF_OPEN_MAX      — concurrent probes in half-open (default 2)
//
// The defaults target MoE backends (Qwen3.6-35B-A3B, Qwen3-Coder-Next AWQ)
// with sub-second p50 and ~1s p95 response times. 8 consecutive failures is
// roughly 8s of fully-failing traffic before fail-safe — a tradeoff between
// "trip on real outages" and "don't trip on a transient SGLang queue-full burst".
//
// Historical context: this default was 25 during the dense Qwen2.5-Coder-32B
// era (30-60s prefill), calibrated down in v11.5 after the MoE engine swap.
// If you're running a dense backend with multi-second prefill, set
// AGENTOS_CB_FAILURE_THRESHOLD=25 (or higher) in your deploy env.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	threshold := 8
	if v := os.Getenv("AGENTOS_CB_FAILURE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			threshold = n
		}
	}
	recovery := 60 * time.Second
	if v := os.Getenv("AGENTOS_CB_RECOVERY_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			recovery = time.Duration(n) * time.Second
		}
	}
	halfOpen := 2
	if v := os.Getenv("AGENTOS_CB_HALF_OPEN_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			halfOpen = n
		}
	}
	return CircuitBreakerConfig{
		FailureThreshold: threshold,
		RecoveryTimeout:  recovery,
		HalfOpenMax:      halfOpen,
	}
}

// circuitBreaker implements the circuit breaker pattern.
type circuitBreaker struct {
	mu             sync.Mutex
	state          CircuitState
	failures       int
	lastFailure    time.Time
	halfOpenActive int
	cfg            CircuitBreakerConfig
}

func newCircuitBreaker(cfg CircuitBreakerConfig) *circuitBreaker {
	return &circuitBreaker{cfg: cfg}
}

// allow checks if a request should be allowed through.
func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailure) >= cb.cfg.RecoveryTimeout {
			cb.state = CircuitHalfOpen
			cb.halfOpenActive = 1
			return true
		}
		return false
	case CircuitHalfOpen:
		if cb.halfOpenActive < cb.cfg.HalfOpenMax {
			cb.halfOpenActive++
			return true
		}
		return false
	}
	return false
}

// recordSuccess records a successful request.
func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.halfOpenActive = 0
	cb.state = CircuitClosed
}

// recordFailure records a failed request.
func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitOpen
		cb.halfOpenActive = 0
		return
	}
	if cb.failures >= cb.cfg.FailureThreshold {
		cb.state = CircuitOpen
	}
}

// State returns the current circuit state (for testing/monitoring).
func (cb *circuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// ResilientProvider wraps a provider with retry and circuit breaker logic.
type ResilientProvider struct {
	inner   provider
	retry   RetryConfig
	breaker *circuitBreaker
}

// NewResilientProvider wraps a provider with retry and circuit breaker.
func NewResilientProvider(inner provider, retryCfg RetryConfig, cbCfg CircuitBreakerConfig) *ResilientProvider {
	return &ResilientProvider{
		inner:   inner,
		retry:   retryCfg,
		breaker: newCircuitBreaker(cbCfg),
	}
}

// isRetryable determines if an error is worth retrying.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		switch {
		case pe.StatusCode >= 500:
			return true
		case pe.StatusCode == 429:
			return true
		}
		return false
	}
	// Treat connection/timeout errors as retryable.
	return true
}

// backoffDuration calculates backoff with jitter.
func backoffDuration(attempt int, cfg RetryConfig) time.Duration {
	base := float64(cfg.BaseBackoff) * math.Pow(cfg.BackoffScale, float64(attempt))
	if base > float64(cfg.MaxBackoff) {
		base = float64(cfg.MaxBackoff)
	}
	// Add ±25% jitter.
	jitter := base * 0.25 * (2*rand.Float64() - 1)
	d := time.Duration(base + jitter)
	if d < 0 {
		d = cfg.BaseBackoff
	}
	return d
}

// retryAfterDuration extracts a Retry-After duration from a ProviderError.
func retryAfterDuration(err error) time.Duration {
	var pe *ProviderError
	if errors.As(err, &pe) && pe.RetryAfter != "" {
		secs := retryAfterSeconds(pe)
		if secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

func (rp *ResilientProvider) ChatComplete(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var lastErr error
	for attempt := 0; attempt < rp.retry.MaxAttempts; attempt++ {
		if attempt > 0 {
			wait := backoffDuration(attempt-1, rp.retry)
			// Respect Retry-After if longer than computed backoff.
			if ra := retryAfterDuration(lastErr); ra > wait {
				wait = ra
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		if !rp.breaker.allow() {
			lastErr = ErrCircuitOpen
			continue
		}

		resp, err := rp.inner.ChatComplete(ctx, req)
		if err == nil {
			rp.breaker.recordSuccess()
			return resp, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
		rp.breaker.recordFailure()
	}
	return nil, lastErr
}

func (rp *ResilientProvider) ChatCompleteStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	var lastErr error
	for attempt := 0; attempt < rp.retry.MaxAttempts; attempt++ {
		if attempt > 0 {
			wait := backoffDuration(attempt-1, rp.retry)
			if ra := retryAfterDuration(lastErr); ra > wait {
				wait = ra
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		if !rp.breaker.allow() {
			lastErr = ErrCircuitOpen
			continue
		}

		ch, err := rp.inner.ChatCompleteStream(ctx, req)
		if err == nil {
			rp.breaker.recordSuccess()
			return ch, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
		rp.breaker.recordFailure()
	}
	return nil, lastErr
}

// Invoke delegates to the inner provider without retry (legacy path).
func (rp *ResilientProvider) Invoke(req types.ModelInvokeRequest) (map[string]any, map[string]any, error) {
	return rp.inner.Invoke(req)
}

// CircuitState returns the current circuit breaker state (for monitoring).
func (rp *ResilientProvider) CircuitState() CircuitState {
	return rp.breaker.State()
}

// CircuitBreakerStatusInfo contains the current status of the circuit breaker
// for external monitoring.
type CircuitBreakerStatusInfo struct {
	State           string        `json:"state"`
	Failures        int           `json:"failures"`
	LastFailure     time.Time     `json:"last_failure"`
	RecoveryTimeout time.Duration `json:"recovery_timeout"`
}

// CircuitBreakerStatus returns a snapshot of the circuit breaker's current status.
func (rp *ResilientProvider) CircuitBreakerStatus() CircuitBreakerStatusInfo {
	rp.breaker.mu.Lock()
	defer rp.breaker.mu.Unlock()

	var stateStr string
	switch rp.breaker.state {
	case CircuitClosed:
		stateStr = "closed"
	case CircuitOpen:
		stateStr = "open"
	case CircuitHalfOpen:
		stateStr = "half_open"
	default:
		stateStr = "unknown"
	}

	return CircuitBreakerStatusInfo{
		State:           stateStr,
		Failures:        rp.breaker.failures,
		LastFailure:     rp.breaker.lastFailure,
		RecoveryTimeout: rp.breaker.cfg.RecoveryTimeout,
	}
}
