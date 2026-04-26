package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/jobs"
)

// EventType represents a webhook event type.
type EventType string

const (
	EventRunStarted   EventType = "run.started"
	EventRunCompleted EventType = "run.completed"
	EventRunFailed    EventType = "run.failed"
	EventRunStep      EventType = "run.step"
)

// Event is the webhook event envelope.
type Event struct {
	Event     EventType      `json:"event"`
	EventID   string         `json:"event_id"`
	TenantID  string         `json:"tenant_id"`
	RunID     string         `json:"run_id"`
	AgentID   string         `json:"agent_id"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

// Dispatcher sends webhook events to a configured URL.
type Dispatcher struct {
	url        string
	secret     string
	stepEvents bool
	client     *http.Client
	queue      jobs.Queue
}

// validateWebhookURL checks that the URL uses http or https scheme to prevent SSRF.
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL scheme %q not allowed; only http and https are permitted", u.Scheme)
	}
	return nil
}

// New creates a Dispatcher with explicit parameters.
// Returns nil if url is empty. Returns nil and logs an error if the URL scheme is not http/https.
func New(rawURL, secret string, stepEvents bool) *Dispatcher {
	if rawURL == "" {
		return nil
	}
	if err := validateWebhookURL(rawURL); err != nil {
		slog.Error("webhook: rejected URL", "url", rawURL, "error", err)
		return nil
	}
	return &Dispatcher{
		url:        rawURL,
		secret:     secret,
		stepEvents: stepEvents,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

// NewFromEnv creates a Dispatcher from environment variables.
// Returns nil if no webhook URL is configured.
func NewFromEnv() *Dispatcher {
	url := os.Getenv("AGENTOS_WEBHOOK_URL")
	if url == "" {
		// Backward compatibility: fall back to usage webhook URL.
		url = os.Getenv("AGENTOS_USAGE_WEBHOOK_URL")
	}
	if url == "" {
		return nil
	}
	if err := validateWebhookURL(url); err != nil {
		slog.Error("webhook: rejected URL from environment", "url", url, "error", err)
		return nil
	}
	return &Dispatcher{
		url:        url,
		secret:     os.Getenv("AGENTOS_WEBHOOK_SECRET"),
		stepEvents: os.Getenv("AGENTOS_WEBHOOK_STEP_EVENTS") == "true",
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

// WithQueue returns the Dispatcher with a job queue configured.
// When a queue is set, Send enqueues events as jobs instead of
// delivering them in a goroutine.
func (d *Dispatcher) WithQueue(q jobs.Queue) *Dispatcher {
	if d == nil {
		return nil
	}
	d.queue = q
	return d
}

// Send dispatches a webhook event asynchronously. Non-blocking.
// Safe to call on a nil Dispatcher.
// If a job queue is configured, the event is enqueued as a JobWebhook job.
// Otherwise, delivery happens in a background goroutine.
func (d *Dispatcher) Send(evt Event) {
	if d == nil {
		return
	}
	// Skip step events unless explicitly enabled.
	if evt.Event == EventRunStep && !d.stepEvents {
		return
	}
	if d.queue != nil {
		if _, err := d.queue.Enqueue(context.Background(), jobs.JobWebhook, evt); err != nil {
			slog.Error("webhook enqueue failed", "event", evt.Event, "error", err)
		}
		return
	}
	go d.deliver(evt)
}

// DeliverJob synchronously delivers a webhook event from a job queue job.
// The worker calls this after dequeuing a JobWebhook job. It unmarshals
// the payload and calls deliver(). Returns an error if unmarshalling or
// delivery fails.
func (d *Dispatcher) DeliverJob(job jobs.Job) error {
	var evt Event
	if err := json.Unmarshal(job.Payload, &evt); err != nil {
		return err
	}
	d.deliver(evt)
	return nil
}

func (d *Dispatcher) deliver(evt Event) {
	body, err := json.Marshal(evt)
	if err != nil {
		slog.Error("webhook marshal failed", "event", evt.Event, "error", err)
		return
	}

	backoffs := []time.Duration{0, 1 * time.Second, 5 * time.Second, 25 * time.Second}
	for attempt, backoff := range backoffs {
		if backoff > 0 {
			time.Sleep(backoff)
		}

		req, err := http.NewRequest(http.MethodPost, d.url, bytes.NewReader(body))
		if err != nil {
			slog.Error("webhook request creation failed", "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		// HMAC signing.
		if d.secret != "" {
			sig := Sign(d.secret, body)
			req.Header.Set("X-Webhook-Signature", sig)
		}

		resp, err := d.client.Do(req)
		if err != nil {
			slog.Warn("webhook delivery failed", "attempt", attempt+1, "event", evt.Event, "error", err)
			continue
		}
		resp.Body.Close() //nolint:errcheck // fire-and-forget HTTP response body
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // success
		}
		slog.Warn("webhook non-2xx response", "attempt", attempt+1, "status", resp.StatusCode, "event", evt.Event)
	}
	slog.Error("webhook delivery exhausted retries", "event", evt.Event, "run_id", evt.RunID)
}

// Sign computes HMAC-SHA256 of the body using the given secret.
// Exported for testing.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
