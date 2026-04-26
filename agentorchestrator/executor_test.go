package agentorchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/storage"
	"github.com/NexixAI/nexixai-agentos/internal/tools"
	"github.com/NexixAI/nexixai-agentos/internal/types"
	"github.com/NexixAI/nexixai-agentos/internal/webhook"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

// --- noop audit logger ---

type noopAuditLogger struct{}

func (noopAuditLogger) Log(_ audit.Entry) {}
func (noopAuditLogger) Close() error      { return nil }

// --- mock provider ---

type mockProvider struct {
	mu        sync.Mutex
	responses []*modelpolicy.ChatResponse
	errors    []error
	calls     int
	lastReq   *modelpolicy.ChatRequest
}

func newMockProvider(responses ...*modelpolicy.ChatResponse) *mockProvider {
	return &mockProvider{responses: responses}
}

func (m *mockProvider) ChatComplete(ctx context.Context, req modelpolicy.ChatRequest) (*modelpolicy.ChatResponse, error) {
	// Check context before proceeding.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	idx := m.calls
	m.calls++
	reqCopy := req
	m.lastReq = &reqCopy
	m.mu.Unlock()

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	// Default: return stop
	return &modelpolicy.ChatResponse{
		Model: req.Model,
		Choices: []modelpolicy.ChatChoice{{
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "default response"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
	}, nil
}

func (m *mockProvider) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// LastRequest returns a pointer to the most recently received ChatRequest,
// or nil if the mock has not been invoked. Used by v11.1 tests to assert
// that multipart content survived end-to-end without stringification.
func (m *mockProvider) LastRequest() *modelpolicy.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReq
}

// --- test helpers ---

func newTestExecConfig() config.ExecConfig {
	return config.ExecConfig{
		Workers:         2,
		DefaultMaxSteps: 10,
		DefaultTimeout:  30 * time.Second,
	}
}

func newTestStorageConfig() config.StorageConfig {
	return config.StorageConfig{
		MemoryMaxMessages: 50,
		MemoryMaxTokens:   8192,
		KVMaxValueSize:    65536,
		KVMaxKeysPerAgent: 1000,
	}
}

func newTestRunStore(t *testing.T) storage.FullRunStore {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewFileRunStore(filepath.Join(dir, "runs.json"))
	if err != nil {
		t.Fatalf("new run store: %v", err)
	}
	return store
}

func newTestMemoryStore(t *testing.T) storage.MemoryStore {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewFileMemoryStore(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatalf("new memory store: %v", err)
	}
	return store
}

func newTestExecutor(t *testing.T, provider ModelProvider) (*Executor, storage.FullRunStore) {
	t.Helper()
	runStore := newTestRunStore(t)
	memStore := newTestMemoryStore(t)
	toolRegistry := tools.NewRegistry()
	auditLogger := noopAuditLogger{}

	exec := NewExecutor(
		newTestExecConfig(),
		newTestStorageConfig(),
		provider,
		toolRegistry,
		memStore,
		runStore,
		auditLogger,
		WithDefaultModel("local-stub-llm"),
	)
	return exec, runStore
}

func makeRun(runID, tenantID, agentID, inputText string) types.Run {
	return types.Run{
		TenantID:  tenantID,
		AgentID:   agentID,
		RunID:     runID,
		Status:    "queued",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		EventsURL: "/v1/runs/" + runID + "/events",
		Input:     types.RunInput{Type: "text", Text: inputText},
	}
}

func makeAgent(agentID, tenantID string) types.Agent {
	return types.Agent{
		AgentID:  agentID,
		TenantID: tenantID,
		Name:     "Test Agent",
		Version:  "1.0",
		Status:   "active",
	}
}

func submitAndWait(t *testing.T, exec *Executor, runStore storage.RunStore, run types.Run, agent types.Agent) types.Run {
	t.Helper()
	if err := runStore.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	events := NewEventSink(run.TenantID, run.AgentID, run.RunID)
	job := &runJob{
		run:    run,
		agent:  agent,
		ctx:    ctx,
		cancel: cancel,
		events: events,
	}
	exec.Submit(job)

	// Poll for completion.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, ok, err := runStore.Get(context.Background(), run.TenantID, run.RunID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if ok && (got.Status == "completed" || got.Status == "failed" || got.Status == "canceled") {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach terminal state within deadline", run.RunID)
	return types.Run{}
}

// --- tests ---

func TestExecuteRun_FinalResponse(t *testing.T) {
	provider := newMockProvider(&modelpolicy.ChatResponse{
		Model: "test-model",
		Choices: []modelpolicy.ChatChoice{{
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "Hello, world!"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})

	exec, runStore := newTestExecutor(t, provider)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_final_1", "tnt_test", "agt_test", "Say hello")
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)

	if got.Status != "completed" {
		t.Fatalf("expected completed, got %s (error: %+v)", got.Status, got.Error)
	}
	if got.Output == nil || got.Output.Text != "Hello, world!" {
		t.Fatalf("expected output 'Hello, world!', got %+v", got.Output)
	}
	if provider.CallCount() != 1 {
		t.Fatalf("expected 1 provider call, got %d", provider.CallCount())
	}
}

func TestExecuteRun_ToolCallThenComplete(t *testing.T) {
	toolCallResp := &modelpolicy.ChatResponse{
		Model: "test-model",
		Choices: []modelpolicy.ChatChoice{{
			Message: modelpolicy.ChatMessage{
				Role: "assistant",
				ToolCalls: []modelpolicy.ToolCall{{
					ID:   "tc_1",
					Type: "function",
					Function: modelpolicy.FunctionCall{
						Name:      "http_fetch",
						Arguments: `{"url":"https://example.com"}`,
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	finalResp := &modelpolicy.ChatResponse{
		Model: "test-model",
		Choices: []modelpolicy.ChatChoice{{
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "Done fetching!"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
	}

	provider := newMockProvider(toolCallResp, finalResp)
	exec, runStore := newTestExecutor(t, provider)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_tool_1", "tnt_test", "agt_test", "Fetch example.com")
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)

	if got.Status != "completed" {
		t.Fatalf("expected completed, got %s (error: %+v)", got.Status, got.Error)
	}
	if got.Output == nil || got.Output.Text != "Done fetching!" {
		t.Fatalf("expected output 'Done fetching!', got %+v", got.Output)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("expected 2 provider calls, got %d", provider.CallCount())
	}
}

func TestExecuteRun_MaxStepsExceeded(t *testing.T) {
	// Provider always returns tool calls, never stops.
	alwaysToolCall := &modelpolicy.ChatResponse{
		Model: "test-model",
		Choices: []modelpolicy.ChatChoice{{
			Message: modelpolicy.ChatMessage{
				Role: "assistant",
				ToolCalls: []modelpolicy.ToolCall{{
					ID:   "tc_loop",
					Type: "function",
					Function: modelpolicy.FunctionCall{
						Name:      "http_fetch",
						Arguments: `{"url":"https://example.com"}`,
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
	}

	// Create many copies to satisfy the loop.
	var responses []*modelpolicy.ChatResponse
	for i := 0; i < 20; i++ {
		responses = append(responses, alwaysToolCall)
	}
	provider := newMockProvider(responses...)

	runStore := newTestRunStore(t)
	memStore := newTestMemoryStore(t)
	toolRegistry := tools.NewRegistry()

	// Use a small max steps.
	execCfg := config.ExecConfig{
		Workers:         2,
		DefaultMaxSteps: 3,
		DefaultTimeout:  30 * time.Second,
	}
	exec := NewExecutor(execCfg, newTestStorageConfig(), provider, toolRegistry, memStore, runStore, noopAuditLogger{}, WithDefaultModel("test-model"))
	defer exec.Shutdown(context.Background())

	run := makeRun("run_maxsteps_1", "tnt_test", "agt_test", "Loop forever")
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)

	if got.Status != "failed" {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == nil || got.Error.Code != "max_steps_exceeded" {
		t.Fatalf("expected max_steps_exceeded error, got %+v", got.Error)
	}
	if provider.CallCount() != 3 {
		t.Fatalf("expected 3 provider calls (max_steps=3), got %d", provider.CallCount())
	}
}

func TestExecuteRun_ContextCancellation(t *testing.T) {
	// Provider that blocks until context is canceled.
	slowProvider := &slowMockProvider{block: make(chan struct{})}

	exec, runStore := newTestExecutor(t, slowProvider)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_cancel_1", "tnt_test", "agt_test", "Cancel me")
	if err := runStore.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events := NewEventSink(run.TenantID, run.AgentID, run.RunID)
	job := &runJob{
		run:    run,
		agent:  makeAgent("agt_test", "tnt_test"),
		ctx:    ctx,
		cancel: cancel,
		events: events,
	}
	exec.Submit(job)

	// Wait a bit then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for run to reach terminal state.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, ok, _ := runStore.Get(context.Background(), run.TenantID, run.RunID)
		if ok && (got.Status == "failed" || got.Status == "canceled") {
			if got.Error == nil || got.Error.Code != "canceled" {
				t.Fatalf("expected canceled error, got %+v", got.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not reach terminal state after cancellation")
}

type slowMockProvider struct {
	block chan struct{}
}

func (s *slowMockProvider) ChatComplete(ctx context.Context, req modelpolicy.ChatRequest) (*modelpolicy.ChatResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.block:
		return &modelpolicy.ChatResponse{
			Model:   req.Model,
			Choices: []modelpolicy.ChatChoice{{Message: modelpolicy.ChatMessage{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
			Usage:   modelpolicy.ChatUsage{},
		}, nil
	}
}

func TestExecuteRun_WorkerPoolConcurrency(t *testing.T) {
	var concurrency int32
	var maxConcurrency int32

	provider := &concurrencyTrackingProvider{
		concurrency:    &concurrency,
		maxConcurrency: &maxConcurrency,
		delay:          50 * time.Millisecond,
	}

	runStore := newTestRunStore(t)
	memStore := newTestMemoryStore(t)
	toolRegistry := tools.NewRegistry()

	execCfg := config.ExecConfig{
		Workers:         4,
		DefaultMaxSteps: 10,
		DefaultTimeout:  30 * time.Second,
	}
	exec := NewExecutor(execCfg, newTestStorageConfig(), provider, toolRegistry, memStore, runStore, noopAuditLogger{}, WithDefaultModel("test-model"))
	defer exec.Shutdown(context.Background())

	// Submit 4 runs at once.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		runID := fmt.Sprintf("run_conc_%d", i)
		run := makeRun(runID, "tnt_test", "agt_test", "Hello")
		if err := runStore.Create(context.Background(), run); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		events := NewEventSink(run.TenantID, run.AgentID, run.RunID)
		job := &runJob{run: run, agent: makeAgent("agt_test", "tnt_test"), ctx: ctx, cancel: cancel, events: events}
		exec.Submit(job)

		go func(rid string) {
			defer wg.Done()
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				got, ok, _ := runStore.Get(context.Background(), "tnt_test", rid)
				if ok && got.Status == "completed" {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Errorf("run %s did not complete", rid)
		}(runID)
	}

	wg.Wait()

	maxC := atomic.LoadInt32(&maxConcurrency)
	if maxC < 2 {
		t.Logf("warning: max observed concurrency was %d (expected >= 2 with 4 workers)", maxC)
	}
}

type concurrencyTrackingProvider struct {
	concurrency    *int32
	maxConcurrency *int32
	delay          time.Duration
}

func (p *concurrencyTrackingProvider) ChatComplete(ctx context.Context, req modelpolicy.ChatRequest) (*modelpolicy.ChatResponse, error) {
	cur := atomic.AddInt32(p.concurrency, 1)
	for {
		max := atomic.LoadInt32(p.maxConcurrency)
		if cur > max {
			if atomic.CompareAndSwapInt32(p.maxConcurrency, max, cur) {
				break
			}
		} else {
			break
		}
	}
	defer atomic.AddInt32(p.concurrency, -1)

	time.Sleep(p.delay)

	return &modelpolicy.ChatResponse{
		Model: req.Model,
		Choices: []modelpolicy.ChatChoice{{
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "concurrent response"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
	}, nil
}

func TestExecuteRun_GracefulShutdown(t *testing.T) {
	// Provider that takes a bit of time.
	provider := &concurrencyTrackingProvider{
		concurrency:    new(int32),
		maxConcurrency: new(int32),
		delay:          200 * time.Millisecond,
	}

	runStore := newTestRunStore(t)
	memStore := newTestMemoryStore(t)
	toolRegistry := tools.NewRegistry()

	execCfg := config.ExecConfig{
		Workers:         2,
		DefaultMaxSteps: 10,
		DefaultTimeout:  30 * time.Second,
	}
	exec := NewExecutor(execCfg, newTestStorageConfig(), provider, toolRegistry, memStore, runStore, noopAuditLogger{}, WithDefaultModel("test-model"))

	// Submit a run.
	run := makeRun("run_shutdown_1", "tnt_test", "agt_test", "Shutdown test")
	if err := runStore.Create(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	events := NewEventSink(run.TenantID, run.AgentID, run.RunID)
	job := &runJob{run: run, agent: makeAgent("agt_test", "tnt_test"), ctx: ctx, cancel: cancel, events: events}
	exec.Submit(job)

	// Immediately initiate shutdown with enough time.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	exec.Shutdown(shutdownCtx)

	// Check that the run completed successfully.
	got, ok, err := runStore.Get(context.Background(), "tnt_test", "run_shutdown_1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok {
		t.Fatal("run not found")
	}
	if got.Status != "completed" {
		t.Fatalf("expected completed after graceful shutdown, got %s", got.Status)
	}
}

func TestExecuteRun_ProviderError(t *testing.T) {
	provider := &mockProvider{
		errors: []error{fmt.Errorf("connection refused")},
	}

	exec, runStore := newTestExecutor(t, provider)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_err_1", "tnt_test", "agt_test", "Fail me")
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)

	if got.Status != "failed" {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == nil || got.Error.Code != "provider_error" {
		t.Fatalf("expected provider_error, got %+v", got.Error)
	}
}

func TestBuildPrompt(t *testing.T) {
	agent := types.Agent{
		AgentID:  "agt_1",
		TenantID: "tnt_1",
		Config: &types.AgentConfig{
			SystemPrompt: "You are a helpful assistant.",
		},
	}
	run := types.Run{
		Input: types.RunInput{Type: "text", Text: "What is 2+2?"},
	}
	history := []types.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	msgs := buildPrompt(agent, run, history)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "You are a helpful assistant." {
		t.Fatalf("unexpected system message: %+v", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Content != "Hello" {
		t.Fatalf("unexpected history message 1: %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "Hi there!" {
		t.Fatalf("unexpected history message 2: %+v", msgs[2])
	}
	if msgs[3].Role != "user" || msgs[3].Content != "What is 2+2?" {
		t.Fatalf("unexpected user input message: %+v", msgs[3])
	}
}

func TestBuildPrompt_NoSystemPrompt(t *testing.T) {
	agent := types.Agent{AgentID: "agt_1", TenantID: "tnt_1"}
	run := types.Run{Input: types.RunInput{Type: "text", Text: "Hello"}}

	msgs := buildPrompt(agent, run, nil)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Hello" {
		t.Fatalf("unexpected message: %+v", msgs[0])
	}
}

// --- streaming tests ---

type mockStreamProvider struct {
	*mockProvider
	streamChunks []modelpolicy.StreamChunk
}

func (m *mockStreamProvider) ChatCompleteStream(ctx context.Context, req modelpolicy.ChatRequest) (<-chan modelpolicy.StreamChunk, error) {
	ch := make(chan modelpolicy.StreamChunk, len(m.streamChunks)+1)
	go func() {
		defer close(ch)
		for _, chunk := range m.streamChunks {
			select {
			case <-ctx.Done():
				ch <- modelpolicy.StreamChunk{Err: ctx.Err()}
				return
			case ch <- chunk:
			}
		}
	}()
	return ch, nil
}

func TestExecuteRun_StreamingEmitsTokenEvents(t *testing.T) {
	base := newMockProvider() // fallback for non-streaming
	sp := &mockStreamProvider{
		mockProvider: base,
		streamChunks: []modelpolicy.StreamChunk{
			{Delta: "Hello"},
			{Delta: " world"},
			{Delta: "!", FinishReason: "stop", Usage: &modelpolicy.ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		},
	}

	exec, runStore := newTestExecutor(t, sp)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_stream_1", "tnt_test", "agt_test", "Hello")
	run.RunOptions.StreamEvents = true
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)

	if got.Status != "completed" {
		t.Fatalf("expected completed, got %s (error: %+v)", got.Status, got.Error)
	}
	if got.Output == nil || got.Output.Text != "Hello world!" {
		t.Fatalf("expected output 'Hello world!', got %+v", got.Output)
	}

	// Check events contain step.token events.
	sink := exec.Events("run_stream_1")
	if sink == nil {
		t.Fatal("expected event sink")
	}
	events := sink.Events()
	tokenCount := 0
	for _, e := range events {
		if e.Event.Type == "step.token" {
			tokenCount++
		}
	}
	if tokenCount < 2 {
		t.Errorf("expected at least 2 step.token events, got %d", tokenCount)
	}
}

func TestExecuteRun_NonStreamingUnchanged(t *testing.T) {
	// Even when provider supports streaming, if StreamEvents=false,
	// should use buffered ChatComplete.
	base := newMockProvider(&modelpolicy.ChatResponse{
		Model: "test-model",
		Choices: []modelpolicy.ChatChoice{{
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "buffered"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
	})
	sp := &mockStreamProvider{
		mockProvider: base,
		streamChunks: []modelpolicy.StreamChunk{
			{Delta: "streamed", FinishReason: "stop"},
		},
	}

	exec, runStore := newTestExecutor(t, sp)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_nostream_1", "tnt_test", "agt_test", "Hello")
	run.RunOptions.StreamEvents = false
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)

	if got.Status != "completed" {
		t.Fatalf("expected completed, got %s", got.Status)
	}
	if got.Output == nil || got.Output.Text != "buffered" {
		t.Fatalf("expected 'buffered' from non-streaming path, got %+v", got.Output)
	}
}

func TestExecuteRun_StreamError(t *testing.T) {
	base := newMockProvider()
	sp := &mockStreamProvider{
		mockProvider: base,
		streamChunks: []modelpolicy.StreamChunk{
			{Delta: "partial"},
			{Err: fmt.Errorf("stream connection lost")},
		},
	}

	exec, runStore := newTestExecutor(t, sp)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_streamerr_1", "tnt_test", "agt_test", "Hello")
	run.RunOptions.StreamEvents = true
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)

	if got.Status != "failed" {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == nil || got.Error.Code != "provider_error" {
		t.Fatalf("expected provider_error, got %+v", got.Error)
	}
}

func TestExecuteRun_WebhookStepEventsEmitted(t *testing.T) {
	// Collect webhook events sent to our test server.
	var mu sync.Mutex
	var webhookEvents []webhook.Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var evt webhook.Event
		if err := json.Unmarshal(body, &evt); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		webhookEvents = append(webhookEvents, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Create a dispatcher with step events enabled.
	wh := webhook.New(srv.URL, "", true)

	// Provider: tool call then final response.
	toolCallResp := &modelpolicy.ChatResponse{
		Model: "test-model",
		Choices: []modelpolicy.ChatChoice{{
			Message: modelpolicy.ChatMessage{
				Role: "assistant",
				ToolCalls: []modelpolicy.ToolCall{{
					ID:   "tc_step1",
					Type: "function",
					Function: modelpolicy.FunctionCall{
						Name:      "http_fetch",
						Arguments: `{"url":"https://example.com"}`,
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	finalResp := &modelpolicy.ChatResponse{
		Model: "test-model",
		Choices: []modelpolicy.ChatChoice{{
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "Fetched!"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
	}
	provider := newMockProvider(toolCallResp, finalResp)

	runStore := newTestRunStore(t)
	memStore := newTestMemoryStore(t)
	toolRegistry := tools.NewRegistry()

	exec := NewExecutor(
		newTestExecConfig(),
		newTestStorageConfig(),
		provider,
		toolRegistry,
		memStore,
		runStore,
		noopAuditLogger{},
		WithDefaultModel("test-model"),
		WithWebhook(wh),
	)
	defer exec.Shutdown(context.Background())

	run := makeRun("run_stepevt_1", "tnt_test", "agt_test", "Fetch something")
	agent := makeAgent("agt_test", "tnt_test")

	got := submitAndWait(t, exec, runStore, run, agent)
	if got.Status != "completed" {
		t.Fatalf("expected completed, got %s (error: %+v)", got.Status, got.Error)
	}

	// Wait for async webhook deliveries.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Expect: run.started, run.step (for the tool call), run.completed.
	var stepEvents []webhook.Event
	for _, evt := range webhookEvents {
		if evt.Event == webhook.EventRunStep {
			stepEvents = append(stepEvents, evt)
		}
	}

	if len(stepEvents) != 1 {
		t.Fatalf("expected 1 run.step webhook event, got %d (total events: %d)", len(stepEvents), len(webhookEvents))
	}

	stepEvt := stepEvents[0]
	if stepEvt.RunID != "run_stepevt_1" {
		t.Fatalf("step event run_id mismatch: got %q", stepEvt.RunID)
	}
	if stepEvt.TenantID != "tnt_test" {
		t.Fatalf("step event tenant_id mismatch: got %q", stepEvt.TenantID)
	}
	if stepEvt.Data["tool_name"] != "http_fetch" {
		t.Fatalf("step event tool_name mismatch: got %v", stepEvt.Data["tool_name"])
	}
	if stepEvt.Data["step_id"] != "tc_step1" {
		t.Fatalf("step event step_id mismatch: got %v", stepEvt.Data["step_id"])
	}
	if _, ok := stepEvt.Data["duration_ms"]; !ok {
		t.Fatal("step event missing duration_ms")
	}
	if _, ok := stepEvt.Data["tool_input"]; !ok {
		t.Fatal("step event missing tool_input")
	}
	if _, ok := stepEvt.Data["tool_output"]; !ok {
		t.Fatal("step event missing tool_output")
	}
}
