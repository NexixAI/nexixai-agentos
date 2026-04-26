package modelpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

var ErrEmptyModelList = errors.New("upstream /v1/models returned empty data array")

type ErrBadStatus struct {
	Code int
}

func (e *ErrBadStatus) Error() string {
	return fmt.Sprintf("upstream /v1/models returned status %d", e.Code)
}

type modelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// QueryFirstModelID issues GET {baseURL}/models and returns the first id in
// the response. Caller owns retry policy; this function makes exactly one
// attempt and reports structured errors (ErrEmptyModelList, *ErrBadStatus,
// context cancellation, decode failures).
//
// baseURL must already include the /v1 prefix (e.g., "http://host:8000/v1")
// to match AGENTOS_MODEL_BASE_URL semantics everywhere else in the codebase.
func QueryFirstModelID(ctx context.Context, baseURL string, client *http.Client) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}

	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build models request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", &ErrBadStatus{Code: resp.StatusCode}
	}

	var parsed modelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode /v1/models response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return "", ErrEmptyModelList
	}
	id := strings.TrimSpace(parsed.Data[0].ID)
	if id == "" {
		return "", ErrEmptyModelList
	}
	return id, nil
}

// defaultResolveBackoffs is the retry schedule for ResolveDefaultModel.
// Exponential with ceiling; total wall-clock ~30s before exhaustion.
var defaultResolveBackoffs = []time.Duration{
	1 * time.Second,
	1 * time.Second,
	2 * time.Second,
	2 * time.Second,
	4 * time.Second,
	4 * time.Second,
	8 * time.Second,
	8 * time.Second,
}

// defaultResolveDeadline caps total wall clock spent across retries.
const defaultResolveDeadline = 30 * time.Second

// defaultResolveAttemptTimeout caps one HTTP attempt.
const defaultResolveAttemptTimeout = 3 * time.Second

// ResolveDefaultModel returns the effective default model id at startup.
// Precedence:
//  1. cfg.DefaultModel (env AGENTOS_MODEL_DEFAULT) — if non-empty, wins.
//  2. Backend /v1/models query against cfg.BaseURL, first id — only when
//     provider == "openai". Retried with backoff up to defaultResolveDeadline.
//  3. Empty string + error on exhaustion or empty-list.
//
// For provider != "openai" with an empty DefaultModel, returns ("", nil) —
// non-openai providers have their own default-model pathways (e.g., Anthropic
// hardcodes claude-sonnet-4).
func ResolveDefaultModel(ctx context.Context, cfg config.ModelConfig, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if env := strings.TrimSpace(cfg.DefaultModel); env != "" {
		logger.Info("model default resolved", "source", "env", "model_id", env)
		return env, nil
	}

	if cfg.Provider != "openai" {
		logger.Debug("model default: backend query skipped for non-openai provider",
			"provider", cfg.Provider)
		return "", nil
	}

	if strings.TrimSpace(cfg.BaseURL) == "" {
		return "", errors.New("AGENTOS_MODEL_BASE_URL is empty; cannot resolve default model")
	}

	client := &http.Client{Timeout: defaultResolveAttemptTimeout}
	return resolveWithBackoffs(ctx, cfg.BaseURL, client, defaultResolveBackoffs, defaultResolveDeadline, logger)
}

// resolveWithBackoffs is the testable core of ResolveDefaultModel.
func resolveWithBackoffs(ctx context.Context, baseURL string, client *http.Client, backoffs []time.Duration, deadline time.Duration, logger *slog.Logger) (string, error) {
	start := time.Now()
	var lastErr error
	for attempt := 0; attempt < len(backoffs); attempt++ {
		if deadline > 0 && time.Since(start) >= deadline {
			break
		}

		attemptCtx, cancel := context.WithTimeout(ctx, defaultResolveAttemptTimeout)
		id, err := QueryFirstModelID(attemptCtx, baseURL, client)
		cancel()

		if err == nil {
			logger.Info("model default resolved",
				"source", "backend",
				"model_id", id,
				"attempts", attempt+1)
			return id, nil
		}

		if errors.Is(err, ErrEmptyModelList) {
			// 200 with an empty list means the backend is up and intentionally
			// says it has no models — retrying won't help.
			return "", fmt.Errorf("resolve default model: %w", err)
		}

		lastErr = err

		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoffs[attempt]):
		}
	}

	return "", fmt.Errorf("resolve default model: exhausted retries (base=%s): %w", baseURLHost(baseURL), lastErr)
}

// baseURLHost returns the scheme://host portion of a URL, suitable for
// including in error messages. Never includes path, query, or userinfo —
// path/query can carry API keys in misconfigured deployments.
func baseURLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<malformed base URL>"
	}
	return u.Scheme + "://" + u.Host
}
