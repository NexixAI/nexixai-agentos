package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/id"
)

// JobType identifies the kind of background job.
type JobType string

const (
	JobWebhook    JobType = "webhook"
	JobDataExport JobType = "data_export"
	JobDataDelete JobType = "data_delete"
	JobPurge      JobType = "purge"
)

// JobStatus represents the lifecycle state of a job.
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
)

// Job is the unit of work in the queue.
type Job struct {
	ID          string          `json:"id"`
	Type        JobType         `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Status      JobStatus       `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	NextRunAt   time.Time       `json:"next_run_at"`
	CreatedAt   time.Time       `json:"created_at"`
	LastError   string          `json:"last_error"`
}

// Queue is the interface for enqueueing and processing background jobs.
type Queue interface {
	Enqueue(ctx context.Context, jobType JobType, payload any, opts ...EnqueueOption) (string, error)
	Dequeue(ctx context.Context) (*Job, error) // returns nil, nil when empty
	Complete(ctx context.Context, jobID string) error
	Fail(ctx context.Context, jobID string, errMsg string) error
}

// EnqueueOptions holds optional parameters for Enqueue.
type EnqueueOptions struct {
	MaxAttempts int
	Delay       time.Duration
}

// EnqueueOption is a functional option for Enqueue.
type EnqueueOption func(*EnqueueOptions)

// WithMaxAttempts sets the maximum number of attempts for a job.
func WithMaxAttempts(n int) EnqueueOption {
	return func(o *EnqueueOptions) {
		o.MaxAttempts = n
	}
}

// WithDelay sets an initial delay before the job becomes eligible.
func WithDelay(d time.Duration) EnqueueOption {
	return func(o *EnqueueOptions) {
		o.Delay = d
	}
}

func defaultEnqueueOptions() EnqueueOptions {
	return EnqueueOptions{MaxAttempts: 3}
}

// memoryQueueMaxJobs is the hard cap for the in-memory queue.
const memoryQueueMaxJobs = 10000

// MemoryQueue is an in-memory Queue implementation for testing and file-backend use.
type MemoryQueue struct {
	mu   sync.Mutex
	jobs []*Job
}

// NewMemoryQueue returns a new empty MemoryQueue.
func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{}
}

// Enqueue adds a job to the in-memory queue.
func (q *MemoryQueue) Enqueue(_ context.Context, jobType JobType, payload any, opts ...EnqueueOption) (string, error) {
	o := defaultEnqueueOptions()
	for _, fn := range opts {
		fn(&o)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	now := time.Now()
	j := &Job{
		ID:          id.New("job"),
		Type:        jobType,
		Payload:     json.RawMessage(raw),
		Status:      StatusPending,
		Attempts:    0,
		MaxAttempts: o.MaxAttempts,
		NextRunAt:   now.Add(o.Delay),
		CreatedAt:   now,
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	// Evict oldest completed jobs if at capacity.
	q.evictLocked()

	q.jobs = append(q.jobs, j)
	return j.ID, nil
}

// evictLocked removes oldest completed jobs to stay within the cap.
// Caller must hold q.mu.
func (q *MemoryQueue) evictLocked() {
	for len(q.jobs) >= memoryQueueMaxJobs {
		evicted := false
		for i, j := range q.jobs {
			if j.Status == StatusCompleted || j.Status == StatusFailed {
				q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			// All jobs are active; drop the oldest one to maintain the bound.
			q.jobs = q.jobs[1:]
		}
	}
}

// Dequeue returns the next eligible pending job, or nil, nil if the queue is empty.
func (q *MemoryQueue) Dequeue(_ context.Context) (*Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	for _, j := range q.jobs {
		if j.Status == StatusPending && !j.NextRunAt.After(now) {
			j.Status = StatusRunning
			j.Attempts++
			return j, nil
		}
	}
	return nil, nil
}

// Complete marks a job as completed.
func (q *MemoryQueue) Complete(_ context.Context, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, j := range q.jobs {
		if j.ID == jobID {
			j.Status = StatusCompleted
			return nil
		}
	}
	return ErrJobNotFound
}

// Fail marks a job as failed. If retries remain, it reschedules with exponential backoff.
func (q *MemoryQueue) Fail(_ context.Context, jobID string, errMsg string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, j := range q.jobs {
		if j.ID == jobID {
			j.LastError = errMsg
			if j.Attempts < j.MaxAttempts {
				j.Status = StatusPending
				j.NextRunAt = time.Now().Add(backoff(j.Attempts))
			} else {
				j.Status = StatusFailed
			}
			return nil
		}
	}
	return ErrJobNotFound
}

// backoff returns an exponential backoff duration: 5s * 3^(attempt-1).
func backoff(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 3
	}
	return d
}
