package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/httpfetch"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// mockFetcher implements httpfetch.Fetcher for testing.
type mockFetcher struct {
	result     *httpfetch.FetchResult
	pingResult *httpfetch.PingResult
	err        error

	// Capture fields for assertions.
	lastMethod  string
	lastHeaders map[string]string
	lastBody    string
}

func (m *mockFetcher) Fetch(_ context.Context, rawURL string) (*httpfetch.FetchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	r := *m.result
	r.URL = rawURL
	return &r, nil
}

func (m *mockFetcher) Request(_ context.Context, method, rawURL string, headers map[string]string, body string) (*httpfetch.FetchResult, error) {
	m.lastMethod = method
	m.lastHeaders = headers
	m.lastBody = body
	if m.err != nil {
		return nil, m.err
	}
	r := *m.result
	r.URL = rawURL
	return &r, nil
}

func (m *mockFetcher) Ping(_ context.Context, rawURL string) (*httpfetch.PingResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	r := *m.pingResult
	r.URL = rawURL
	return &r, nil
}

func TestFetchURL_Handler(t *testing.T) {
	tests := []struct {
		name       string
		params     string
		fetcher    *mockFetcher
		wantErr    bool
		errContain string
		checkResp  func(t *testing.T, resp fetchURLResponse)
	}{
		{
			name:   "successful fetch",
			params: `{"url":"https://example.com"}`,
			fetcher: &mockFetcher{
				result: &httpfetch.FetchResult{
					URL:          "https://example.com",
					StatusCode:   200,
					ContentType:  "text/plain",
					Body:         "hello world",
					BytesFetched: 11,
				},
			},
			checkResp: func(t *testing.T, resp fetchURLResponse) {
				t.Helper()
				if resp.URL != "https://example.com" {
					t.Errorf("expected URL %q, got %q", "https://example.com", resp.URL)
				}
				if resp.StatusCode != 200 {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
				}
				if resp.Content != "hello world" {
					t.Errorf("expected content %q, got %q", "hello world", resp.Content)
				}
				if resp.ContentType != "text/plain" {
					t.Errorf("expected content type %q, got %q", "text/plain", resp.ContentType)
				}
				if resp.BytesFetched != 11 {
					t.Errorf("expected 11 bytes, got %d", resp.BytesFetched)
				}
			},
		},
		{
			name:       "missing url parameter",
			params:     `{}`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "missing required parameter",
		},
		{
			name:       "empty url parameter",
			params:     `{"url":""}`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "missing required parameter",
		},
		{
			name:       "invalid JSON params",
			params:     `{invalid`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "invalid parameters",
		},
		{
			name:   "fetcher returns error",
			params: `{"url":"https://example.com"}`,
			fetcher: &mockFetcher{
				err: fmt.Errorf("httpfetch: SSRF protection — blocked"),
			},
			wantErr:    true,
			errContain: "SSRF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := mcp.NewToolRegistry()
			if err := RegisterHTTPTools(registry, tt.fetcher); err != nil {
				t.Fatalf("RegisterHTTPTools failed: %v", err)
			}

			tool := registry.Get("fetch_url")
			if tool == nil {
				t.Fatal("fetch_url tool not found in registry")
			}

			result, err := tool.Handler(context.Background(), json.RawMessage(tt.params))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" {
					if got := err.Error(); !contains(got, tt.errContain) {
						t.Errorf("expected error containing %q, got %q", tt.errContain, got)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			resp, ok := result.(fetchURLResponse)
			if !ok {
				t.Fatalf("expected fetchURLResponse, got %T", result)
			}

			if tt.checkResp != nil {
				tt.checkResp(t, resp)
			}
		})
	}
}

func TestFetchURL_ToolRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	fetcher := &mockFetcher{}
	if err := RegisterHTTPTools(registry, fetcher); err != nil {
		t.Fatalf("RegisterHTTPTools failed: %v", err)
	}

	tool := registry.Get("fetch_url")
	if tool == nil {
		t.Fatal("fetch_url tool not found in registry")
	}

	if tool.Name != "fetch_url" {
		t.Errorf("expected tool name %q, got %q", "fetch_url", tool.Name)
	}
	if tool.MinClearance != mcp.ClearanceInternal {
		t.Errorf("expected ClearanceInternal (%d), got %d", mcp.ClearanceInternal, tool.MinClearance)
	}
	if !tool.Static {
		t.Error("expected tool to be static")
	}
	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	// Verify InputSchema is valid JSON with required url field.
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("invalid InputSchema JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected InputSchema type %q, got %v", "object", schema["type"])
	}

	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("expected required to be an array")
	}
	if len(required) != 1 || required[0] != "url" {
		t.Errorf("expected required=[\"url\"], got %v", required)
	}
}

func TestFetchURL_DuplicateRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	fetcher := &mockFetcher{}
	if err := RegisterHTTPTools(registry, fetcher); err != nil {
		t.Fatalf("first RegisterHTTPTools failed: %v", err)
	}
	if err := RegisterHTTPTools(registry, fetcher); err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}
}

func TestFetchURL_ResponseSerializesToJSON(t *testing.T) {
	registry := mcp.NewToolRegistry()
	fetcher := &mockFetcher{
		result: &httpfetch.FetchResult{
			URL:          "https://example.com",
			StatusCode:   200,
			ContentType:  "text/plain",
			Body:         "test body",
			BytesFetched: 9,
		},
	}
	if err := RegisterHTTPTools(registry, fetcher); err != nil {
		t.Fatalf("RegisterHTTPTools failed: %v", err)
	}

	tool := registry.Get("fetch_url")
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result JSON: %v", err)
	}

	for _, key := range []string{"url", "status_code", "content_type", "content", "bytes_fetched"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing key %q in serialized response", key)
		}
	}
}

func TestHTTPRequest_Handler(t *testing.T) {
	tests := []struct {
		name       string
		params     string
		fetcher    *mockFetcher
		wantErr    bool
		errContain string
		checkResp  func(t *testing.T, resp fetchURLResponse, m *mockFetcher)
	}{
		{
			name:   "successful POST request",
			params: `{"url":"https://example.com/api","method":"POST","headers":{"Content-Type":"application/json"},"body":"{\"key\":\"val\"}"}`,
			fetcher: &mockFetcher{
				result: &httpfetch.FetchResult{
					StatusCode:   201,
					ContentType:  "application/json",
					Body:         `{"created":true}`,
					BytesFetched: 16,
				},
			},
			checkResp: func(t *testing.T, resp fetchURLResponse, m *mockFetcher) {
				t.Helper()
				if resp.URL != "https://example.com/api" {
					t.Errorf("expected URL %q, got %q", "https://example.com/api", resp.URL)
				}
				if resp.StatusCode != 201 {
					t.Errorf("expected status 201, got %d", resp.StatusCode)
				}
				if m.lastMethod != "POST" {
					t.Errorf("expected method POST, got %q", m.lastMethod)
				}
				if m.lastBody != `{"key":"val"}` {
					t.Errorf("expected body %q, got %q", `{"key":"val"}`, m.lastBody)
				}
				if m.lastHeaders["Content-Type"] != "application/json" {
					t.Errorf("expected Content-Type header, got %v", m.lastHeaders)
				}
			},
		},
		{
			name:       "missing method parameter",
			params:     `{"url":"https://example.com"}`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "missing required parameter \"method\"",
		},
		{
			name:       "missing url parameter",
			params:     `{"method":"GET"}`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "missing required parameter \"url\"",
		},
		{
			name:       "invalid JSON params",
			params:     `{invalid`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "invalid parameters",
		},
		{
			name:   "fetcher returns error",
			params: `{"url":"https://example.com","method":"GET"}`,
			fetcher: &mockFetcher{
				err: fmt.Errorf("httpfetch: SSRF protection — blocked"),
			},
			wantErr:    true,
			errContain: "SSRF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := mcp.NewToolRegistry()
			if err := RegisterHTTPTools(registry, tt.fetcher); err != nil {
				t.Fatalf("RegisterHTTPTools failed: %v", err)
			}

			tool := registry.Get("http_request")
			if tool == nil {
				t.Fatal("http_request tool not found in registry")
			}

			result, err := tool.Handler(context.Background(), json.RawMessage(tt.params))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" {
					if got := err.Error(); !contains(got, tt.errContain) {
						t.Errorf("expected error containing %q, got %q", tt.errContain, got)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			resp, ok := result.(fetchURLResponse)
			if !ok {
				t.Fatalf("expected fetchURLResponse, got %T", result)
			}

			if tt.checkResp != nil {
				tt.checkResp(t, resp, tt.fetcher)
			}
		})
	}
}

func TestPingURL_Handler(t *testing.T) {
	tests := []struct {
		name       string
		params     string
		fetcher    *mockFetcher
		wantErr    bool
		errContain string
		checkResp  func(t *testing.T, resp pingURLResponse)
	}{
		{
			name:   "successful ping",
			params: `{"url":"https://example.com"}`,
			fetcher: &mockFetcher{
				pingResult: &httpfetch.PingResult{
					URL:        "https://example.com",
					StatusCode: 200,
					LatencyMs:  42,
				},
			},
			checkResp: func(t *testing.T, resp pingURLResponse) {
				t.Helper()
				if resp.URL != "https://example.com" {
					t.Errorf("expected URL %q, got %q", "https://example.com", resp.URL)
				}
				if resp.StatusCode != 200 {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
				}
				if resp.LatencyMs != 42 {
					t.Errorf("expected latency 42ms, got %d", resp.LatencyMs)
				}
			},
		},
		{
			name:       "missing url parameter",
			params:     `{}`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "missing required parameter",
		},
		{
			name:       "invalid JSON params",
			params:     `{invalid`,
			fetcher:    &mockFetcher{},
			wantErr:    true,
			errContain: "invalid parameters",
		},
		{
			name:   "fetcher returns error",
			params: `{"url":"https://example.com"}`,
			fetcher: &mockFetcher{
				err: fmt.Errorf("httpfetch: ping failed: connection refused"),
			},
			wantErr:    true,
			errContain: "ping failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := mcp.NewToolRegistry()
			if err := RegisterHTTPTools(registry, tt.fetcher); err != nil {
				t.Fatalf("RegisterHTTPTools failed: %v", err)
			}

			tool := registry.Get("ping_url")
			if tool == nil {
				t.Fatal("ping_url tool not found in registry")
			}

			result, err := tool.Handler(context.Background(), json.RawMessage(tt.params))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" {
					if got := err.Error(); !contains(got, tt.errContain) {
						t.Errorf("expected error containing %q, got %q", tt.errContain, got)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			resp, ok := result.(pingURLResponse)
			if !ok {
				t.Fatalf("expected pingURLResponse, got %T", result)
			}

			if tt.checkResp != nil {
				tt.checkResp(t, resp)
			}
		})
	}
}

func TestHTTPTools_ClearanceTiers(t *testing.T) {
	registry := mcp.NewToolRegistry()
	fetcher := &mockFetcher{}
	if err := RegisterHTTPTools(registry, fetcher); err != nil {
		t.Fatalf("RegisterHTTPTools failed: %v", err)
	}

	tests := []struct {
		name             string
		toolName         string
		expectedClearance mcp.ClearanceTier
	}{
		{"fetch_url is T1 (Internal)", "fetch_url", mcp.ClearanceInternal},
		{"http_request is T3 (Admin)", "http_request", mcp.ClearanceAdmin},
		{"ping_url is T1 (Internal)", "ping_url", mcp.ClearanceInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := registry.Get(tt.toolName)
			if tool == nil {
				t.Fatalf("tool %q not found in registry", tt.toolName)
			}
			if tool.MinClearance != tt.expectedClearance {
				t.Errorf("expected clearance %d, got %d", tt.expectedClearance, tool.MinClearance)
			}
		})
	}
}

func TestHTTPTools_AllRegistered(t *testing.T) {
	registry := mcp.NewToolRegistry()
	fetcher := &mockFetcher{}
	if err := RegisterHTTPTools(registry, fetcher); err != nil {
		t.Fatalf("RegisterHTTPTools failed: %v", err)
	}

	for _, name := range []string{"fetch_url", "http_request", "ping_url"} {
		tool := registry.Get(name)
		if tool == nil {
			t.Errorf("expected tool %q to be registered, but it was not found", name)
		}
	}
}

// contains checks if s contains substr (avoids importing strings in test).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
