package jobs

import (
	"context"
	"testing"
	"time"
)

func TestMemoryQueue_EnqueueDequeueRoundTrip(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	payload := map[string]string{"url": "https://example.com/hook"}
	jobID, err := q.Enqueue(ctx, JobWebhook, payload)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected non-empty job ID")
	}

	job, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job, got nil")
	}
	if job.ID != jobID {
		t.Fatalf("got job ID %q, want %q", job.ID, jobID)
	}
	if job.Type != JobWebhook {
		t.Fatalf("got type %q, want %q", job.Type, JobWebhook)
	}
	if job.Status != StatusRunning {
		t.Fatalf("got status %q, want %q", job.Status, StatusRunning)
	}
	if job.Attempts != 1 {
		t.Fatalf("got attempts %d, want 1", job.Attempts)
	}
}

func TestMemoryQueue_DequeueEmpty(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	job, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job != nil {
		t.Fatalf("expected nil job from empty queue, got %+v", job)
	}
}

func TestMemoryQueue_Complete(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	jobID, err := q.Enqueue(ctx, JobDataExport, "test")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	job, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job")
	}

	if err := q.Complete(ctx, jobID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Verify it's no longer dequeued.
	job2, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue after complete: %v", err)
	}
	if job2 != nil {
		t.Fatal("expected nil after completing the only job")
	}
}

func TestMemoryQueue_CompleteNotFound(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	err := q.Complete(ctx, "nonexistent")
	if err != ErrJobNotFound {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}

func TestMemoryQueue_FailWithRetry(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	jobID, err := q.Enqueue(ctx, JobPurge, nil, WithMaxAttempts(3))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Dequeue attempt 1.
	job, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job")
	}

	// Fail it — should reschedule since attempts(1) < maxAttempts(3).
	if err := q.Fail(ctx, jobID, "temporary error"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// The job should be back in pending state.
	q.mu.Lock()
	var found *Job
	for _, j := range q.jobs {
		if j.ID == jobID {
			found = j
			break
		}
	}
	q.mu.Unlock()

	if found == nil {
		t.Fatal("job not found after fail")
	}
	if found.Status != StatusPending {
		t.Fatalf("expected status pending after fail with retries, got %q", found.Status)
	}
	if found.LastError != "temporary error" {
		t.Fatalf("expected last_error 'temporary error', got %q", found.LastError)
	}
}

func TestMemoryQueue_FailExhausted(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	jobID, err := q.Enqueue(ctx, JobPurge, nil, WithMaxAttempts(1))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Dequeue (attempt 1 of 1).
	job, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job")
	}

	// Fail — attempts(1) >= maxAttempts(1), should be permanently failed.
	if err := q.Fail(ctx, jobID, "permanent error"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	q.mu.Lock()
	var found *Job
	for _, j := range q.jobs {
		if j.ID == jobID {
			found = j
			break
		}
	}
	q.mu.Unlock()

	if found == nil {
		t.Fatal("job not found")
	}
	if found.Status != StatusFailed {
		t.Fatalf("expected status failed, got %q", found.Status)
	}
}

func TestMemoryQueue_FailNotFound(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	err := q.Fail(ctx, "nonexistent", "oops")
	if err != ErrJobNotFound {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}

func TestMemoryQueue_BoundedAt10000(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	// Fill to capacity.
	for i := 0; i < memoryQueueMaxJobs; i++ {
		if _, err := q.Enqueue(ctx, JobWebhook, i); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	if len(q.jobs) != memoryQueueMaxJobs {
		t.Fatalf("expected %d jobs, got %d", memoryQueueMaxJobs, len(q.jobs))
	}

	// One more should evict an old one (since none are completed, it drops oldest).
	if _, err := q.Enqueue(ctx, JobWebhook, "overflow"); err != nil {
		t.Fatalf("Enqueue overflow: %v", err)
	}

	if len(q.jobs) != memoryQueueMaxJobs {
		t.Fatalf("expected %d jobs after overflow, got %d", memoryQueueMaxJobs, len(q.jobs))
	}
}

func TestMemoryQueue_WithDelay(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()

	_, err := q.Enqueue(ctx, JobWebhook, nil, WithDelay(1*time.Hour))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Should not be dequeued because of the delay.
	job, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job != nil {
		t.Fatal("expected nil — job has a future delay")
	}
}

func TestBackoff(t *testing.T) {
	// attempt 1 → 5s, attempt 2 → 15s, attempt 3 → 45s
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 5 * time.Second},
		{2, 15 * time.Second},
		{3, 45 * time.Second},
	}
	for _, tc := range cases {
		got := backoff(tc.attempt)
		if got != tc.want {
			t.Errorf("backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// Verify MemoryQueue implements Queue interface at compile time.
var _ Queue = (*MemoryQueue)(nil)
