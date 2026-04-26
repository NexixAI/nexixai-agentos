package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// WebhookConfig holds webhook delivery settings.
type WebhookConfig struct {
	URL     string
	Timeout time.Duration
}

// RunCompletedEvent is the payload sent to the webhook on run completion.
type RunCompletedEvent struct {
	TenantID     string `json:"tenant_id"`
	RunID        string `json:"run_id"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	DurationMs   int64  `json:"duration_ms"`
	Timestamp    string `json:"timestamp"`
}

// WebhookURLFromEnv reads the webhook URL from the environment.
// An empty value means webhooks are disabled.
func WebhookURLFromEnv() string {
	return os.Getenv("AGENTOS_USAGE_WEBHOOK_URL")
}

// SendRunCompletedEvent delivers the event to the configured webhook URL.
// It retries up to 3 times with exponential backoff (1s, 5s, 25s).
// Failures are logged but never returned — this must not block run completion.
func SendRunCompletedEvent(ctx context.Context, cfg WebhookConfig, event RunCompletedEvent) {
	if cfg.URL == "" {
		return
	}

	body, err := json.Marshal(event)
	if err != nil {
		slog.Error("webhook: failed to marshal event", "error", err, "run_id", event.RunID)
		return
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	backoffs := []time.Duration{1 * time.Second, 5 * time.Second, 25 * time.Second}

	for attempt := 0; attempt <= len(backoffs); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				slog.Warn("webhook: context cancelled during retry",
					"run_id", event.RunID, "attempt", attempt)
				return
			case <-time.After(backoffs[attempt-1]):
			}
		}

		deliverErr := deliverWebhook(ctx, cfg.URL, timeout, body)
		if deliverErr == nil {
			slog.Info("webhook: delivered successfully",
				"run_id", event.RunID, "attempt", attempt+1)
			return
		}

		slog.Warn("webhook: delivery failed",
			"error", deliverErr, "run_id", event.RunID, "attempt", attempt+1)
	}

	slog.Error("webhook: all retries exhausted",
		"run_id", event.RunID, "url", cfg.URL)
}

func deliverWebhook(ctx context.Context, url string, timeout time.Duration, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("unexpected status: %d", resp.StatusCode)
}
