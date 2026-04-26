package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// healthResponse is the structured result returned by the get_health tool.
type healthResponse struct {
	AgentOS    componentStatus `json:"agentos"`
	VLLM       componentStatus `json:"vllm"`
	Classifier componentStatus `json:"classifier"`
	Timestamp  string          `json:"timestamp"`
}

// componentStatus describes the health of a single component.
type componentStatus struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// healthInputSchema is the JSON Schema for get_health (no required parameters).
var healthInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

// RegisterHealthTools registers the get_health tool with the given registry.
// modelBaseURL is the base URL for the vLLM provider (e.g. "http://localhost:8000").
// classifierURL is the URL for the classifier service (e.g. "http://localhost:8001").
func RegisterHealthTools(registry *mcp.ToolRegistry, modelBaseURL, classifierURL string) error {
	return registry.Register(mcp.Tool{
		Name:         "get_health",
		Description:  "Returns health status of AgentOS and its upstream dependencies (vLLM, classifier).",
		InputSchema:  healthInputSchema,
		MinClearance: mcp.ClearancePublic,
		Static:       true,
		Category:     "observe",
		Handler:      makeHealthHandler(modelBaseURL, classifierURL),
	})
}

// makeHealthHandler returns a ToolHandler that probes vLLM and classifier endpoints.
func makeHealthHandler(modelBaseURL, classifierURL string) mcp.ToolHandler {
	client := &http.Client{Timeout: 5 * time.Second}

	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		resp := healthResponse{
			AgentOS: componentStatus{Status: "healthy"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		// Probe vLLM upstream.
		resp.VLLM = probeHTTP(ctx, client, modelBaseURL+"/models")

		// Probe classifier upstream — use /v1/models (classifierURL points
		// to /v1/chat/completions which only accepts POST).
		classifierHealthURL := classifierURL
		if idx := len(classifierHealthURL) - len("/v1/chat/completions"); idx > 0 && classifierHealthURL[idx:] == "/v1/chat/completions" {
			classifierHealthURL = classifierHealthURL[:idx] + "/v1/models"
		}
		resp.Classifier = probeHTTP(ctx, client, classifierHealthURL)

		return resp, nil
	}
}

// probeHTTP performs a GET request to the given URL and returns a componentStatus.
func probeHTTP(ctx context.Context, client *http.Client, url string) componentStatus {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.Warn("health probe: failed to create request", "url", url, "error", err)
		return componentStatus{
			Status: "unhealthy",
			Error:  fmt.Sprintf("request creation failed: %v", err),
		}
	}

	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		slog.Warn("health probe: request failed", "url", url, "error", err)
		return componentStatus{
			Status:    "unhealthy",
			LatencyMs: latency,
			Error:     fmt.Sprintf("request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		slog.Warn("health probe: server error", "url", url, "status", resp.StatusCode)
		return componentStatus{
			Status:    "unhealthy",
			LatencyMs: latency,
			Error:     fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode),
		}
	}

	return componentStatus{
		Status:    "healthy",
		LatencyMs: latency,
	}
}
