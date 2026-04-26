package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- helpers ---

// registerMockTools adds n mock tools to the given registry.
// Each tool has a no-op handler that returns immediately to measure
// framework overhead rather than tool execution time.
func registerMockTools(b *testing.B, reg *ToolRegistry, n int) {
	b.Helper()
	for i := 0; i < n; i++ {
		err := reg.Register(Tool{
			Name:        fmt.Sprintf("mock-tool-%d", i),
			Description: fmt.Sprintf("mock tool %d for benchmarking", i),
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
				return map[string]string{"status": "ok"}, nil
			},
			MinClearance: ClearancePublic,
			Static:       true,
		})
		if err != nil {
			b.Fatalf("failed to register mock tool %d: %v", i, err)
		}
	}
}

// benchRPC sends a JSON-RPC request to the given URL and reads the response.
// It does not use testing.T so it can be called inside b.RunParallel.
func benchRPC(url string, method string, params any) error {
	return benchRPCWithHeaders(url, method, params, nil)
}

func benchRPCWithHeaders(url string, method string, params any, headers map[string]string) error {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		reqBody["params"] = params
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // bench test URL
	if err != nil {
		return fmt.Errorf("POST: %w", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	return nil
}

// --- BenchmarkToolsList ---

func BenchmarkToolsList(b *testing.B) {
	for _, size := range []int{10, 20, 30} {
		b.Run(fmt.Sprintf("tools=%d", size), func(b *testing.B) {
			reg := NewToolRegistry()
			registerMockTools(b, reg, size)

			srv := NewServer(":0", "bench", reg, NewInMemoryClearanceStore(WithDefaultTier(ClearanceAdmin)))
			ts := httptest.NewServer(srv.http.Handler)
			defer ts.Close()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := benchRPC(ts.URL, "tools/list", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- BenchmarkToolsCall_Simple ---

func BenchmarkToolsCall_Simple(b *testing.B) {
	reg := NewToolRegistry()
	err := reg.Register(Tool{
		Name:        "noop",
		Description: "no-op tool for benchmarking",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]string{"status": "ok"}, nil
		},
		MinClearance: ClearancePublic,
		Static:       true,
	})
	if err != nil {
		b.Fatalf("register noop tool: %v", err)
	}

	srv := NewServer(":0", "bench", reg, NewInMemoryClearanceStore(WithDefaultTier(ClearanceAdmin)))
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()

	params := ToolCallParams{
		Name:      "noop",
		Arguments: json.RawMessage(`{}`),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := benchRPCWithHeaders(ts.URL, "tools/call", params, map[string]string{"X-Agent-ID": "bench-agent"}); err != nil {
			b.Fatal(err)
		}
	}
}

// --- BenchmarkToolsCall_WithAuth ---

// benchClearanceStore is a minimal ClearanceStore for auth benchmarking.
type benchClearanceStore struct {
	tiers map[string]ClearanceTier
}

func (s *benchClearanceStore) GetClearance(_ context.Context, agentID string) (ClearanceTier, error) {
	tier, ok := s.tiers[agentID]
	if !ok {
		return 0, fmt.Errorf("agent %q not found", agentID)
	}
	return tier, nil
}

func BenchmarkToolsCall_WithAuth(b *testing.B) {
	store := &benchClearanceStore{
		tiers: map[string]ClearanceTier{
			"bench-agent": ClearanceAdmin,
		},
	}

	reg := NewToolRegistry()

	innerTool := &Tool{
		Name:        "auth-noop",
		Description: "no-op tool with auth middleware",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]string{"status": "ok"}, nil
		},
		MinClearance: ClearanceInternal,
		Static:       true,
	}

	err := reg.Register(Tool{
		Name:         innerTool.Name,
		Description:  innerTool.Description,
		InputSchema:  innerTool.InputSchema,
		Handler:      innerTool.Handler,
		MinClearance: innerTool.MinClearance,
		Static:       innerTool.Static,
	})
	if err != nil {
		b.Fatalf("register auth-noop tool: %v", err)
	}

	srv := NewServer(":0", "bench", reg, store)
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()

	params := ToolCallParams{
		Name:      "auth-noop",
		Arguments: json.RawMessage(`{}`),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := benchRPCWithHeaders(ts.URL, "tools/call", params, map[string]string{
			"X-Agent-ID":  "bench-agent",
			"X-Tenant-ID": "bench-tenant",
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// --- BenchmarkRegistryLookup ---

func BenchmarkRegistryLookup(b *testing.B) {
	for _, size := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			reg := NewToolRegistry()
			registerMockTools(b, reg, size)

			// Look up the last tool registered (worst case for iteration,
			// but map lookup is O(1) — this measures lock contention).
			target := fmt.Sprintf("mock-tool-%d", size-1)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				t := reg.Get(target)
				if t == nil {
					b.Fatalf("Get(%q) returned nil", target)
				}
			}
		})
	}
}

// --- BenchmarkRegistryList ---

func BenchmarkRegistryList(b *testing.B) {
	for _, size := range []int{10, 20, 30} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			reg := NewToolRegistry()
			registerMockTools(b, reg, size)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tools := reg.List()
				if len(tools) != size {
					b.Fatalf("List() returned %d tools, want %d", len(tools), size)
				}
			}
		})
	}
}
