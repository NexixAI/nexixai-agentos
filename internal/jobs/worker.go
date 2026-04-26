package jobs

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultPollInterval is the default time between queue polls.
const DefaultPollInterval = 5 * time.Second

// HandlerFunc processes a single job.
type HandlerFunc func(Job) error

// Worker polls a Queue and dispatches jobs to registered handlers.
// It processes one job at a time per instance.
type Worker struct {
	queue    Queue
	handlers map[JobType]HandlerFunc
	interval time.Duration
	done     chan struct{}
	wg       sync.WaitGroup
}

// NewWorker creates a Worker that polls the given queue.
func NewWorker(q Queue) *Worker {
	return &Worker{
		queue:    q,
		handlers: make(map[JobType]HandlerFunc),
		interval: DefaultPollInterval,
		done:     make(chan struct{}),
	}
}

// SetPollInterval overrides the default poll interval.
func (w *Worker) SetPollInterval(d time.Duration) {
	w.interval = d
}

// RegisterHandler registers a handler for the given job type.
func (w *Worker) RegisterHandler(jobType JobType, fn HandlerFunc) {
	w.handlers[jobType] = fn
}

// Start begins polling the queue in a background goroutine. It blocks until
// ctx is cancelled or Stop is called.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.loop(ctx)
}

// Stop signals the worker to stop and waits for the in-flight job (if any) to
// complete.
func (w *Worker) Stop() {
	close(w.done)
	w.wg.Wait()
}

func (w *Worker) loop(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Worker) poll(ctx context.Context) {
	job, err := w.queue.Dequeue(ctx)
	if err != nil {
		slog.Error("worker: dequeue error", "error", err)
		return
	}
	if job == nil {
		return
	}

	handler, ok := w.handlers[job.Type]
	if !ok {
		slog.Warn("worker: no handler registered", "job_type", job.Type, "job_id", job.ID)
		if fErr := w.queue.Fail(ctx, job.ID, "no handler registered for job type"); fErr != nil {
			slog.Error("worker: fail error after no handler", "error", fErr, "job_id", job.ID)
		}
		return
	}

	slog.Info("worker: processing job", "job_id", job.ID, "job_type", job.Type, "attempt", job.Attempts)

	if hErr := handler(*job); hErr != nil {
		slog.Error("worker: handler error", "error", hErr, "job_id", job.ID)
		if fErr := w.queue.Fail(ctx, job.ID, hErr.Error()); fErr != nil {
			slog.Error("worker: fail error", "error", fErr, "job_id", job.ID)
		}
		return
	}

	if cErr := w.queue.Complete(ctx, job.ID); cErr != nil {
		slog.Error("worker: complete error", "error", cErr, "job_id", job.ID)
	}
}
