package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/jobs"
)

func TestSign_HMAC(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"event":"run.started"}`)

	sig := Sign(secret, body)

	// Verify manually.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if sig != expected {
		t.Fatalf("signature mismatch: got %q, want %q", sig, expected)
	}
}

func TestNilDispatcher_SendIsSafe(t *testing.T) {
	var d *Dispatcher
	// Should not panic.
	d.Send(Event{Event: EventRunStarted, RunID: "run_1"})
}

func TestEventJSON_Marshal(t *testing.T) {
	evt := Event{
		Event:     EventRunStarted,
		EventID:   "evt_123",
		TenantID:  "tnt_demo",
		RunID:     "run_456",
		AgentID:   "agt_789",
		Timestamp: "2026-03-09T12:00:00Z",
		Data:      map[string]any{"steps": 0},
	}

	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded["event"] != "run.started" {
		t.Fatalf("event type mismatch: got %v", decoded["event"])
	}
	if decoded["run_id"] != "run_456" {
		t.Fatalf("run_id mismatch: got %v", decoded["run_id"])
	}
	if decoded["tenant_id"] != "tnt_demo" {
		t.Fatalf("tenant_id mismatch: got %v", decoded["tenant_id"])
	}
}

func TestStepEvents_SkippedWhenDisabled(t *testing.T) {
	var called atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{
		url:        srv.URL,
		stepEvents: false,
		client:     &http.Client{Timeout: 2 * time.Second},
	}

	d.Send(Event{Event: EventRunStep, RunID: "run_1"})
	// Give goroutine time to fire (it shouldn't).
	time.Sleep(100 * time.Millisecond)

	if called.Load() != 0 {
		t.Fatal("step event should have been skipped")
	}

	// Non-step event should go through.
	d.Send(Event{Event: EventRunStarted, RunID: "run_1"})
	time.Sleep(200 * time.Millisecond)

	if called.Load() != 1 {
		t.Fatalf("expected 1 call for non-step event, got %d", called.Load())
	}
}

func TestStepEvents_SentWhenEnabled(t *testing.T) {
	var called atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{
		url:        srv.URL,
		stepEvents: true,
		client:     &http.Client{Timeout: 2 * time.Second},
	}

	d.Send(Event{Event: EventRunStep, RunID: "run_1"})
	time.Sleep(200 * time.Millisecond)

	if called.Load() != 1 {
		t.Fatalf("expected step event to be sent, got %d calls", called.Load())
	}
}

func TestNewFromEnv_NilWhenNoURL(t *testing.T) {
	t.Setenv("AGENTOS_WEBHOOK_URL", "")
	t.Setenv("AGENTOS_USAGE_WEBHOOK_URL", "")

	d := NewFromEnv()
	if d != nil {
		t.Fatal("expected nil dispatcher when no URL set")
	}
}

func TestNewFromEnv_UsesWebhookURL(t *testing.T) {
	t.Setenv("AGENTOS_WEBHOOK_URL", "http://example.com/hook")
	t.Setenv("AGENTOS_USAGE_WEBHOOK_URL", "")
	t.Setenv("AGENTOS_WEBHOOK_SECRET", "s3cret")
	t.Setenv("AGENTOS_WEBHOOK_STEP_EVENTS", "true")

	d := NewFromEnv()
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	if d.url != "http://example.com/hook" {
		t.Fatalf("url mismatch: %q", d.url)
	}
	if d.secret != "s3cret" {
		t.Fatalf("secret mismatch: %q", d.secret)
	}
	if !d.stepEvents {
		t.Fatal("expected stepEvents true")
	}
}

func TestNewFromEnv_FallbackToUsageURL(t *testing.T) {
	t.Setenv("AGENTOS_WEBHOOK_URL", "")
	t.Setenv("AGENTOS_USAGE_WEBHOOK_URL", "http://legacy.example.com/hook")
	t.Setenv("AGENTOS_WEBHOOK_SECRET", "")
	t.Setenv("AGENTOS_WEBHOOK_STEP_EVENTS", "")

	d := NewFromEnv()
	if d == nil {
		t.Fatal("expected non-nil dispatcher from fallback URL")
	}
	if d.url != "http://legacy.example.com/hook" {
		t.Fatalf("url mismatch: %q", d.url)
	}
}

func TestSend_EnqueuesWhenQueueSet(t *testing.T) {
	q := jobs.NewMemoryQueue()

	d := &Dispatcher{
		url:        "http://unused.example.com",
		stepEvents: true,
		client:     &http.Client{Timeout: 2 * time.Second},
	}
	d.WithQueue(q)

	evt := Event{
		Event:   EventRunStarted,
		EventID: "evt_q1",
		RunID:   "run_q1",
	}
	d.Send(evt)

	// Dequeue and verify.
	job, err := q.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job in the queue")
	}
	if job.Type != jobs.JobWebhook {
		t.Fatalf("expected job type %q, got %q", jobs.JobWebhook, job.Type)
	}

	var decoded Event
	if err := json.Unmarshal(job.Payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Event != EventRunStarted {
		t.Fatalf("expected event type %q, got %q", EventRunStarted, decoded.Event)
	}
	if decoded.RunID != "run_q1" {
		t.Fatalf("expected run_id %q, got %q", "run_q1", decoded.RunID)
	}
}

func TestSend_FallbackWhenNoQueue(t *testing.T) {
	var called atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{
		url:    srv.URL,
		client: &http.Client{Timeout: 2 * time.Second},
	}
	// No queue set — should use goroutine delivery.
	d.Send(Event{Event: EventRunCompleted, RunID: "run_fb1"})
	time.Sleep(200 * time.Millisecond)

	if called.Load() != 1 {
		t.Fatalf("expected 1 delivery call (goroutine path), got %d", called.Load())
	}
}

func TestDeliverJob_Roundtrip(t *testing.T) {
	var received atomic.Int32
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{
		url:    srv.URL,
		client: &http.Client{Timeout: 2 * time.Second},
	}

	// Simulate a job created by the queue.
	evt := Event{
		Event:   EventRunCompleted,
		EventID: "evt_dj1",
		RunID:   "run_dj1",
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{
		ID:      "job_dj1",
		Type:    jobs.JobWebhook,
		Payload: payload,
	}

	if err := d.DeliverJob(job); err != nil {
		t.Fatalf("DeliverJob: %v", err)
	}

	if received.Load() != 1 {
		t.Fatalf("expected 1 delivery, got %d", received.Load())
	}

	// Verify the body is the event JSON.
	var decoded Event
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("unmarshal received body: %v", err)
	}
	if decoded.RunID != "run_dj1" {
		t.Fatalf("expected run_id %q, got %q", "run_dj1", decoded.RunID)
	}
}

func TestDeliverJob_InvalidPayload(t *testing.T) {
	d := &Dispatcher{
		url:    "http://unused.example.com",
		client: &http.Client{Timeout: 2 * time.Second},
	}

	job := jobs.Job{
		ID:      "job_bad1",
		Type:    jobs.JobWebhook,
		Payload: json.RawMessage(`{invalid json`),
	}

	err := d.DeliverJob(job)
	if err == nil {
		t.Fatal("expected error for invalid payload")
	}
}

func TestWithQueue_NilDispatcher(t *testing.T) {
	var d *Dispatcher
	got := d.WithQueue(jobs.NewMemoryQueue())
	if got != nil {
		t.Fatal("expected nil from WithQueue on nil dispatcher")
	}
}

func TestDeliver_IncludesHMACSignature(t *testing.T) {
	secret := "my-secret"
	type result struct {
		sig  string
		body []byte
	}
	ch := make(chan result, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.Header.Get("X-Webhook-Signature")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
		ch <- result{sig: sig, body: body}
	}))
	defer srv.Close()

	d := &Dispatcher{
		url:    srv.URL,
		secret: secret,
		client: &http.Client{Timeout: 2 * time.Second},
	}

	d.Send(Event{Event: EventRunCompleted, RunID: "run_1"})

	select {
	case res := <-ch:
		if res.sig == "" {
			t.Fatal("expected X-Webhook-Signature header")
		}
		expected := Sign(secret, res.body)
		if res.sig != expected {
			t.Fatalf("signature mismatch: got %q, want %q", res.sig, expected)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
	}
}
