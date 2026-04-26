package modelpolicy

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

type failingProvider struct {
	callCount  atomic.Int32
	failUntil  int32 // fail for this many calls, then succeed
	statusCode int   // HTTP status code to simulate
	retryAfter string
}

func (p *failingProvider) Invoke(_ types.ModelInvokeRequest) (map[string]any, map[string]any, error) {
	return nil, nil, errors.New("not implemented")
}

func (p *failingProvider) ChatComplete(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	n := p.callCount.Add(1)
	if n <= p.failUntil {
		return nil, &ProviderError{
			StatusCode: p.statusCode,
			Code:       "test_error",
			Message:    "simulated failure",
			RetryAfter: p.retryAfter,
		}
	}
	return &ChatResponse{
		Model:   req.Model,
		Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
		Usage:   ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func (p *failingProvider) ChatCompleteStream(_ context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	n := p.callCount.Add(1)
	if n <= p.failUntil {
		return nil, &ProviderError{
			StatusCode: p.statusCode,
			Code:       "test_error",
			Message:    "simulated failure",
		}
	}
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Delta: "ok", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestRetry_SucceedsAfterTransientFailure(t *testing.T) {
	fp := &failingProvider{failUntil: 2, statusCode: 500}
	rp := NewResilientProvider(fp, RetryConfig{
		MaxAttempts:  3,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   10 * time.Millisecond,
		BackoffScale: 2.0,
	}, DefaultCircuitBreakerConfig())

	resp, err := rp.ChatComplete(context.Background(), ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if fp.callCount.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", fp.callCount.Load())
	}
}

func TestRetry_ExhaustsAttempts(t *testing.T) {
	fp := &failingProvider{failUntil: 10, statusCode: 500}
	rp := NewResilientProvider(fp, RetryConfig{
		MaxAttempts:  3,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   10 * time.Millisecond,
		BackoffScale: 2.0,
	}, DefaultCircuitBreakerConfig())

	_, err := rp.ChatComplete(context.Background(), ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ProviderError, got %T", err)
	}
}

func TestRetry_NonRetryableError(t *testing.T) {
	fp := &failingProvider{failUntil: 10, statusCode: 400} // 400 is not retryable
	rp := NewResilientProvider(fp, RetryConfig{
		MaxAttempts:  3,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   10 * time.Millisecond,
		BackoffScale: 2.0,
	}, DefaultCircuitBreakerConfig())

	_, err := rp.ChatComplete(context.Background(), ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error for non-retryable")
	}
	if fp.callCount.Load() != 1 {
		t.Errorf("expected 1 call (no retry for 400), got %d", fp.callCount.Load())
	}
}

func TestRetry_StreamSucceedsAfterFailure(t *testing.T) {
	fp := &failingProvider{failUntil: 1, statusCode: 503}
	rp := NewResilientProvider(fp, RetryConfig{
		MaxAttempts:  3,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   10 * time.Millisecond,
		BackoffScale: 2.0,
	}, DefaultCircuitBreakerConfig())

	ch, err := rp.ChatCompleteStream(context.Background(), ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	var text string
	for c := range ch {
		text += c.Delta
	}
	if text != "ok" {
		t.Errorf("unexpected stream content: %s", text)
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	fp := &failingProvider{failUntil: 100, statusCode: 500}
	rp := NewResilientProvider(fp, RetryConfig{
		MaxAttempts:  1, // No retry — isolate circuit breaker behavior.
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   10 * time.Millisecond,
		BackoffScale: 2.0,
	}, CircuitBreakerConfig{
		FailureThreshold: 3,
		RecoveryTimeout:  1 * time.Hour, // Won't recover in this test.
		HalfOpenMax:      1,
	})

	// Fail 3 times to trigger open.
	for i := 0; i < 3; i++ {
		_, _ = rp.ChatComplete(context.Background(), ChatRequest{Model: "test"})
	}

	if rp.CircuitState() != CircuitOpen {
		t.Errorf("expected CircuitOpen, got %v", rp.CircuitState())
	}

	// Next call should get ErrCircuitOpen.
	_, err := rp.ChatComplete(context.Background(), ChatRequest{Model: "test"})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got: %v", err)
	}
}

func TestCircuitBreaker_RecoversThroughHalfOpen(t *testing.T) {
	fp := &failingProvider{failUntil: 3, statusCode: 500}
	rp := NewResilientProvider(fp, RetryConfig{
		MaxAttempts:  1,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   10 * time.Millisecond,
		BackoffScale: 2.0,
	}, CircuitBreakerConfig{
		FailureThreshold: 3,
		RecoveryTimeout:  10 * time.Millisecond, // Short for testing.
		HalfOpenMax:      1,
	})

	// Trigger open.
	for i := 0; i < 3; i++ {
		_, _ = rp.ChatComplete(context.Background(), ChatRequest{Model: "test"})
	}
	if rp.CircuitState() != CircuitOpen {
		t.Fatalf("expected CircuitOpen, got %v", rp.CircuitState())
	}

	// Wait for recovery timeout.
	time.Sleep(15 * time.Millisecond)

	// Next call should probe (half-open) and succeed (fp.failUntil=3, already called 3 times).
	resp, err := rp.ChatComplete(context.Background(), ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("expected success in half-open probe, got: %v", err)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if rp.CircuitState() != CircuitClosed {
		t.Errorf("expected CircuitClosed after successful probe, got %v", rp.CircuitState())
	}
}

func TestBackoffDuration_Scales(t *testing.T) {
	cfg := RetryConfig{
		BaseBackoff:  100 * time.Millisecond,
		MaxBackoff:   10 * time.Second,
		BackoffScale: 2.0,
	}
	d0 := backoffDuration(0, cfg)
	d1 := backoffDuration(1, cfg)
	d2 := backoffDuration(2, cfg)

	// With jitter, values should be roughly in the right range.
	if d0 < 50*time.Millisecond || d0 > 150*time.Millisecond {
		t.Errorf("d0 out of expected range: %v", d0)
	}
	if d1 < 100*time.Millisecond || d1 > 300*time.Millisecond {
		t.Errorf("d1 out of expected range: %v", d1)
	}
	if d2 < 200*time.Millisecond || d2 > 600*time.Millisecond {
		t.Errorf("d2 out of expected range: %v", d2)
	}
}

func TestRetry_RespectsContextCancellation(t *testing.T) {
	fp := &failingProvider{failUntil: 100, statusCode: 500}
	rp := NewResilientProvider(fp, RetryConfig{
		MaxAttempts:  10,
		BaseBackoff:  1 * time.Second, // Long backoff to test cancellation.
		MaxBackoff:   10 * time.Second,
		BackoffScale: 2.0,
	}, DefaultCircuitBreakerConfig())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := rp.ChatComplete(ctx, ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}
