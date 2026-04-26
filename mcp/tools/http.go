package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/NexixAI/nexixai-agentos/internal/httpfetch"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// fetchURLInput is the expected input for the fetch_url tool.
type fetchURLInput struct {
	URL string `json:"url"`
}

// fetchURLResponse is the structured result returned by the fetch_url tool.
type fetchURLResponse struct {
	URL          string `json:"url"`
	StatusCode   int    `json:"status_code"`
	ContentType  string `json:"content_type"`
	Content      string `json:"content"`
	BytesFetched int64  `json:"bytes_fetched"`
}

// httpRequestInput is the expected input for the http_request tool.
type httpRequestInput struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// pingURLInput is the expected input for the ping_url tool.
type pingURLInput struct {
	URL string `json:"url"`
}

// pingURLResponse is the structured result returned by the ping_url tool.
type pingURLResponse struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	LatencyMs  int64  `json:"latency_ms"`
}

// fetchURLInputSchema is the JSON Schema for the fetch_url tool.
var fetchURLInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"url": {
			"type": "string",
			"description": "The URL to fetch. Must be http or https."
		}
	},
	"required": ["url"],
	"additionalProperties": false
}`)

// httpRequestInputSchema is the JSON Schema for the http_request tool.
var httpRequestInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"url": {
			"type": "string",
			"description": "The URL to send the request to. Must be http or https."
		},
		"method": {
			"type": "string",
			"description": "The HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS)."
		},
		"headers": {
			"type": "object",
			"description": "Optional HTTP headers as key-value pairs.",
			"additionalProperties": { "type": "string" }
		},
		"body": {
			"type": "string",
			"description": "Optional request body. Only sent for POST, PUT, and PATCH methods."
		}
	},
	"required": ["url", "method"],
	"additionalProperties": false
}`)

// pingURLInputSchema is the JSON Schema for the ping_url tool.
var pingURLInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"url": {
			"type": "string",
			"description": "The URL to ping with a HEAD request. Must be http or https."
		}
	},
	"required": ["url"],
	"additionalProperties": false
}`)

// RegisterHTTPTools registers HTTP-related tools with the given registry.
// The fetcher is used for URL retrieval — callers may provide a mock for testing.
func RegisterHTTPTools(registry *mcp.ToolRegistry, fetcher httpfetch.Fetcher) error {
	if err := registry.Register(mcp.Tool{
		Name:         "fetch_url",
		Description:  "Fetches a URL and returns its content. HTML is converted to markdown. JSON is pretty-printed. Enforces SSRF protection, domain filtering, and size limits.",
		InputSchema:  fetchURLInputSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeFetchURLHandler(fetcher),
	}); err != nil {
		return err
	}

	if err := registry.Register(mcp.Tool{
		Name:         "http_request",
		Description:  "Sends an HTTP request with a configurable method, headers, and body. Supports GET, POST, PUT, DELETE, PATCH, HEAD, and OPTIONS. Enforces SSRF protection, domain filtering, and size limits.",
		InputSchema:  httpRequestInputSchema,
		MinClearance: mcp.ClearanceAdmin,
		Static:       true,
		Handler:      makeHTTPRequestHandler(fetcher),
	}); err != nil {
		return err
	}

	return registry.Register(mcp.Tool{
		Name:         "ping_url",
		Description:  "Sends a HEAD request to a URL and returns the status code and round-trip latency in milliseconds. Enforces SSRF protection and domain filtering.",
		InputSchema:  pingURLInputSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makePingURLHandler(fetcher),
	})
}

// makeFetchURLHandler returns a ToolHandler that fetches a URL.
func makeFetchURLHandler(fetcher httpfetch.Fetcher) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input fetchURLInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("fetch_url: invalid parameters: %w", err)
		}

		if input.URL == "" {
			return nil, fmt.Errorf("fetch_url: missing required parameter \"url\"")
		}

		slog.Info("fetch_url: fetching", "url", input.URL)

		result, err := fetcher.Fetch(ctx, input.URL)
		if err != nil {
			return nil, fmt.Errorf("fetch_url: %w", err)
		}

		return fetchURLResponse{
			URL:          result.URL,
			StatusCode:   result.StatusCode,
			ContentType:  result.ContentType,
			Content:      result.Body,
			BytesFetched: result.BytesFetched,
		}, nil
	}
}

// makeHTTPRequestHandler returns a ToolHandler that sends an HTTP request.
func makeHTTPRequestHandler(fetcher httpfetch.Fetcher) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input httpRequestInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("http_request: invalid parameters: %w", err)
		}

		if input.URL == "" {
			return nil, fmt.Errorf("http_request: missing required parameter \"url\"")
		}
		if input.Method == "" {
			return nil, fmt.Errorf("http_request: missing required parameter \"method\"")
		}

		slog.Info("http_request: sending", "method", input.Method, "url", input.URL)

		result, err := fetcher.Request(ctx, input.Method, input.URL, input.Headers, input.Body)
		if err != nil {
			return nil, fmt.Errorf("http_request: %w", err)
		}

		return fetchURLResponse{
			URL:          result.URL,
			StatusCode:   result.StatusCode,
			ContentType:  result.ContentType,
			Content:      result.Body,
			BytesFetched: result.BytesFetched,
		}, nil
	}
}

// makePingURLHandler returns a ToolHandler that pings a URL.
func makePingURLHandler(fetcher httpfetch.Fetcher) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input pingURLInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("ping_url: invalid parameters: %w", err)
		}

		if input.URL == "" {
			return nil, fmt.Errorf("ping_url: missing required parameter \"url\"")
		}

		slog.Info("ping_url: pinging", "url", input.URL)

		result, err := fetcher.Ping(ctx, input.URL)
		if err != nil {
			return nil, fmt.Errorf("ping_url: %w", err)
		}

		return pingURLResponse{
			URL:        result.URL,
			StatusCode: result.StatusCode,
			LatencyMs:  result.LatencyMs,
		}, nil
	}
}
