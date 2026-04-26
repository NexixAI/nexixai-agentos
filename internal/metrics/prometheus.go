package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"
)

// PrometheusClient defines read-only operations against a Prometheus instance.
type PrometheusClient interface {
	// Query executes an instant PromQL query and returns the result.
	Query(ctx context.Context, promql string) (*QueryResult, error)

	// GetAlerts returns all currently active alerts.
	GetAlerts(ctx context.Context) ([]Alert, error)
}

// QueryResult holds the response from a Prometheus instant query.
type QueryResult struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// Alert represents a single active Prometheus alert.
type Alert struct {
	Name     string            `json:"name"`
	State    string            `json:"state"`
	Labels   map[string]string `json:"labels"`
	Value    string            `json:"value"`
	ActiveAt time.Time         `json:"activeAt"`
}

// HTTPPrometheusClient implements PrometheusClient using the Prometheus HTTP API.
type HTTPPrometheusClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPPrometheusClient creates a PrometheusClient backed by the Prometheus
// HTTP API. If baseURL is empty, the value of AGENTOS_PROMETHEUS_URL is used,
// defaulting to http://localhost:9090.
func NewHTTPPrometheusClient(baseURL string) *HTTPPrometheusClient {
	if baseURL == "" {
		baseURL = os.Getenv("AGENTOS_PROMETHEUS_URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:9090"
	}
	return &HTTPPrometheusClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Query executes an instant PromQL query via GET /api/v1/query.
func (c *HTTPPrometheusClient) Query(ctx context.Context, promql string) (*QueryResult, error) {
	queryURL := c.baseURL + "/api/v1/query?query=" + url.QueryEscape(promql)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prometheus query: create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus query: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prometheus query: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Warn("prometheus query: non-200 response",
			"status", resp.StatusCode, "query", promql, "body", string(body))
		return nil, fmt.Errorf("prometheus query: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result QueryResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("prometheus query: unmarshal response: %w", err)
	}

	return &result, nil
}

// alertsAPIResponse mirrors the Prometheus /api/v1/alerts response structure.
type alertsAPIResponse struct {
	Status string `json:"status"`
	Data   struct {
		Alerts []alertEntry `json:"alerts"`
	} `json:"data"`
}

// alertEntry is a single alert in the Prometheus alerts API response.
type alertEntry struct {
	Labels   map[string]string `json:"labels"`
	State    string            `json:"state"`
	Value    string            `json:"value"`
	ActiveAt time.Time         `json:"activeAt"`
}

// GetAlerts retrieves active alerts via GET /api/v1/alerts.
func (c *HTTPPrometheusClient) GetAlerts(ctx context.Context) ([]Alert, error) {
	url := c.baseURL + "/api/v1/alerts"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("prometheus alerts: create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus alerts: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prometheus alerts: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Warn("prometheus alerts: non-200 response",
			"status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("prometheus alerts: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var apiResp alertsAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("prometheus alerts: unmarshal response: %w", err)
	}

	alerts := make([]Alert, 0, len(apiResp.Data.Alerts))
	for _, a := range apiResp.Data.Alerts {
		name := a.Labels["alertname"]
		alerts = append(alerts, Alert{
			Name:     name,
			State:    a.State,
			Labels:   a.Labels,
			Value:    a.Value,
			ActiveAt: a.ActiveAt,
		})
	}

	return alerts, nil
}
