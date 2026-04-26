package agentorchestrator

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/pii"
	"github.com/NexixAI/nexixai-agentos/internal/quota"
	"github.com/NexixAI/nexixai-agentos/internal/storage"
	"github.com/NexixAI/nexixai-agentos/internal/tools"
	"github.com/NexixAI/nexixai-agentos/internal/types"
	"github.com/NexixAI/nexixai-agentos/internal/webhook"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

// json helpers used by prompt.go conversion functions (avoids direct
// encoding/json import in prompt.go keeping it focused on logic).
var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
)

// ModelProvider is the subset of the model policy provider interface needed
// by the executor. This allows injection of mock providers in tests.
type ModelProvider interface {
	ChatComplete(ctx context.Context, req modelpolicy.ChatRequest) (*modelpolicy.ChatResponse, error)
}

// ModelStreamProvider extends ModelProvider with streaming support.
// If the provider implements this interface AND stream_events is true,
// the executor uses streaming instead of buffered completions.
type ModelStreamProvider interface {
	ModelProvider
	ChatCompleteStream(ctx context.Context, req modelpolicy.ChatRequest) (<-chan modelpolicy.StreamChunk, error)
}

// runJob represents a single run to be executed by a worker.
type runJob struct {
	run    types.Run
	agent  types.Agent
	ctx    context.Context
	cancel context.CancelFunc
	events *EventSink
}

// ExecutorOption configures optional Executor parameters.
type ExecutorOption func(*Executor)

// WithTokenBudget sets the token budget for the executor.
func WithTokenBudget(tb *quota.TokenBudget) ExecutorOption {
	return func(e *Executor) {
		e.tokenBudget = tb
	}
}

// WithDefaultModel sets the fallback model ID when agents don't specify one.
func WithDefaultModel(model string) ExecutorOption {
	return func(e *Executor) {
		e.defaultModel = model
	}
}

// WithEventLog sets a durable event log store for the executor.
func WithEventLog(el storage.EventLogStore) ExecutorOption {
	return func(e *Executor) {
		e.eventLog = el
	}
}

// WithWebhook sets the webhook dispatcher for run lifecycle events.
func WithWebhook(wh *webhook.Dispatcher) ExecutorOption {
	return func(e *Executor) {
		e.webhook = wh
	}
}

// Executor manages a pool of worker goroutines that execute agent runs.
type Executor struct {
	workers        int
	defaultMaxSteps int
	queue          chan *runJob
	provider       ModelProvider
	tools          *tools.Registry
	memory         storage.MemoryStore
	runs           storage.FullRunStore
	audit          audit.Logger
	piiDetector    *pii.Detector
	piiMode        string // "block", "redact", "warn"
	tokenBudget    *quota.TokenBudget
	eventLog       storage.EventLogStore
	defaultModel   string
	webhook        *webhook.Dispatcher
	memMaxMessages int
	memMaxTokens   int
	toolMaxOutput  int
	wg             sync.WaitGroup
	shutdownOnce   sync.Once

	// shutdownCtx is cancelled when Shutdown is called.  Detached goroutines
	// (e.g. auto-retry) derive their context from this so they are cancelled
	// on shutdown and tracked via wg (v9.0 #13 / M-6).
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc

	// cancels tracks active run cancel functions for external cancellation.
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	// eventSinks tracks event sinks for active and recently-completed runs.
	// Completed sinks are kept for sinkTTL to allow SSE clients to drain events.
	eventSinks    map[string]*EventSink
	sinkCompleted map[string]time.Time // runID → completion time
}

// NewExecutor creates an Executor and starts its worker goroutines.
func NewExecutor(
	execCfg config.ExecConfig,
	storageCfg config.StorageConfig,
	provider ModelProvider,
	toolRegistry *tools.Registry,
	memoryStore storage.MemoryStore,
	runStore storage.FullRunStore,
	auditLogger audit.Logger,
	opts ...ExecutorOption,
) *Executor {
	// Create PII detector if enabled.
	var piiDetector *pii.Detector
	piiMode := storageCfg.PII.DefaultMode
	if storageCfg.PII.Enabled {
		piiDetector = pii.NewDetector(nil) // all patterns enabled
	}
	if piiMode == "" {
		piiMode = "warn"
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	e := &Executor{
		workers:         execCfg.Workers,
		defaultMaxSteps: execCfg.DefaultMaxSteps,
		queue:           make(chan *runJob, execCfg.Workers*2),
		provider:        provider,
		tools:           toolRegistry,
		memory:          memoryStore,
		runs:            runStore,
		audit:           auditLogger,
		piiDetector:     piiDetector,
		piiMode:         piiMode,
		memMaxMessages:  storageCfg.MemoryMaxMessages,
		memMaxTokens:    storageCfg.MemoryMaxTokens,
		cancels:         make(map[string]context.CancelFunc),
		eventSinks:      make(map[string]*EventSink),
		sinkCompleted:   make(map[string]time.Time),
		shutdownCtx:     shutdownCtx,
		shutdownCancel:  shutdownCancel,
	}
	for _, opt := range opts {
		opt(e)
	}

	e.toolMaxOutput = execCfg.ToolMaxOutputSize
	if e.toolMaxOutput <= 0 {
		e.toolMaxOutput = 1024 * 1024
	}

	for i := 0; i < e.workers; i++ {
		e.wg.Add(1)
		go e.worker()
	}

	return e
}

// Submit enqueues a run job for execution.
func (e *Executor) Submit(job *runJob) {
	e.mu.Lock()
	e.cancels[job.run.RunID] = job.cancel
	e.eventSinks[job.run.RunID] = job.events
	e.mu.Unlock()
	e.queue <- job
}

// Cancel requests cancellation of a running job by run ID.
func (e *Executor) Cancel(runID string) {
	e.mu.Lock()
	cancel, ok := e.cancels[runID]
	e.mu.Unlock()
	if ok {
		cancel()
	}
}

// Events returns the event sink for a run, if available.
func (e *Executor) Events(runID string) *EventSink {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.eventSinks[runID]
}

// EventSinkForRun returns the event sink for a run, if available.
// This is the preferred accessor for streaming subscribers.
func (e *Executor) EventSinkForRun(runID string) *EventSink {
	return e.Events(runID)
}

// Shutdown stops the executor by closing the job queue, cancelling the
// shutdown context (which signals detached goroutines like auto-retry),
// and waiting for in-flight work to complete (v9.0 #13).
func (e *Executor) Shutdown(ctx context.Context) {
	e.shutdownOnce.Do(func() {
		close(e.queue)
		e.shutdownCancel() // signal detached goroutines
	})
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// worker is a long-lived goroutine that dequeues and executes run jobs.
func (e *Executor) worker() {
	defer e.wg.Done()
	for job := range e.queue {
		e.executeRun(job)
		// Notify streaming subscribers that the run is done.
		job.events.Done()
		// Clean up cancel func; mark event sink for deferred eviction.
		e.mu.Lock()
		delete(e.cancels, job.run.RunID)
		e.sinkCompleted[job.run.RunID] = time.Now()
		e.evictStaleSinks()
		e.mu.Unlock()
	}
}

const sinkTTL = 5 * time.Minute

// evictStaleSinks removes event sinks that completed more than sinkTTL ago.
// Must be called with e.mu held.
func (e *Executor) evictStaleSinks() {
	cutoff := time.Now().Add(-sinkTTL)
	for runID, completedAt := range e.sinkCompleted {
		if completedAt.Before(cutoff) {
			delete(e.eventSinks, runID)
			delete(e.sinkCompleted, runID)
		}
	}
}

// truncateToolOutput limits tool output to the configured maximum size.
func (e *Executor) truncateToolOutput(result string) string {
	if e.toolMaxOutput > 0 && len(result) > e.toolMaxOutput {
		return result[:e.toolMaxOutput] + "\n... [output truncated at " + strconv.Itoa(e.toolMaxOutput) + " bytes]"
	}
	return result
}
