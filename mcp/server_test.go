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

	"github.com/NexixAI/nexixai-agentos/internal/auth"
)

// newTestServer creates an MCP server backed by the given registry,
// wrapped in an httptest.Server for testing.  The server is NOT
// initialized — call initializeServer if tools/list or tools/call
// will be invoked.
func newTestServer(t *testing.T, reg *ToolRegistry) *httptest.Server {
	t.Helper()
	srv := NewServer(":0", "test-0.0.1", reg, NewInMemoryClearanceStore(WithDefaultTier(ClearanceAdmin)))
	return httptest.NewServer(srv.http.Handler)
}

// initializeServer performs the MCP initialize handshake, required before
// calling tools/list or tools/call (v9.0 L-7).
func initializeServer(t *testing.T, url string) {
	t.Helper()
	resp := rpcCall(t, url, "initialize", InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo:      ClientInfo{Name: "test-client", Version: "0.1"},
	})
	if resp.Error != nil {
		t.Fatalf("initialize returned error: %+v", resp.Error)
	}
}

// rpcCall sends a JSON-RPC request to the test server and decodes the response.
func rpcCall(t *testing.T, url string, method string, params any) Response {
	t.Helper()
	return rpcCallWithHeaders(t, url, method, params, nil)
}

// rpcCallWithHeaders sends a JSON-RPC request to the test server with optional headers.
func rpcCallWithHeaders(t *testing.T, url string, method string, params any, headers map[string]string) Response {
	t.Helper()

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
		t.Fatalf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var rpcResp Response
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		t.Fatalf("failed to decode response: %v\nbody: %s", err, respBody)
	}
	return rpcResp
}

func TestInitialize(t *testing.T) {
	reg := NewToolRegistry()
	ts := newTestServer(t, reg)
	defer ts.Close()

	resp := rpcCall(t, ts.URL, "initialize", InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo:      ClientInfo{Name: "test-client", Version: "0.1"},
	})

	if resp.Error != nil {
		t.Fatalf("initialize returned error: %+v", resp.Error)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("ProtocolVersion = %q, want %q", result.ProtocolVersion, ProtocolVersion)
	}
	if result.ServerInfo.Name != ServerName {
		t.Errorf("ServerInfo.Name = %q, want %q", result.ServerInfo.Name, ServerName)
	}
	if result.ServerInfo.Version != "test-0.0.1" {
		t.Errorf("ServerInfo.Version = %q, want %q", result.ServerInfo.Version, "test-0.0.1")
	}
	if result.Capabilities.Tools == nil {
		t.Fatal("Capabilities.Tools should not be nil")
	}
}

func TestToolsListEmpty(t *testing.T) {
	reg := NewToolRegistry()
	ts := newTestServer(t, reg)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCall(t, ts.URL, "tools/list", nil)

	if resp.Error != nil {
		t.Fatalf("tools/list returned error: %+v", resp.Error)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result ToolsListResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if len(result.Tools) != 0 {
		t.Errorf("tools/list returned %d tools, want 0", len(result.Tools))
	}
}

func TestToolsListWithRegistered(t *testing.T) {
	reg := NewToolRegistry()
	if err := reg.Register(Tool{
		Name:        "echo",
		Description: "echoes input",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
		Handler: func(_ context.Context, params json.RawMessage) (any, error) {
			return string(params), nil
		},
		Static: true,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	ts := newTestServer(t, reg)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCall(t, ts.URL, "tools/list", nil)

	if resp.Error != nil {
		t.Fatalf("tools/list returned error: %+v", resp.Error)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result ToolsListResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("tools/list returned %d tools, want 1", len(result.Tools))
	}
	if result.Tools[0].Name != "echo" {
		t.Errorf("tool name = %q, want %q", result.Tools[0].Name, "echo")
	}
	if result.Tools[0].Description != "echoes input" {
		t.Errorf("tool description = %q, want %q", result.Tools[0].Description, "echoes input")
	}
}

func TestToolsCallDispatch(t *testing.T) {
	t.Setenv("AGENTOS_DEV_MODE", "true") // tests are not auth tests — use dev mode
	reg := NewToolRegistry()
	if err := reg.Register(Tool{
		Name:        "greet",
		Description: "returns a greeting",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
		Handler: func(_ context.Context, params json.RawMessage) (any, error) {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, fmt.Errorf("bad params: %w", err)
			}
			return map[string]string{"greeting": "hello " + p.Name}, nil
		},
		Static: true,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	ts := newTestServer(t, reg)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCallWithHeaders(t, ts.URL, "tools/call", ToolCallParams{
		Name:      "greet",
		Arguments: json.RawMessage(`{"name":"world"}`),
	}, map[string]string{"X-Agent-ID": "test-agent"})

	if resp.Error != nil {
		t.Fatalf("tools/call returned error: %+v", resp.Error)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result ToolCallResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if result.IsError {
		t.Error("expected IsError=false for successful call")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" {
		t.Errorf("content type = %q, want %q", result.Content[0].Type, "text")
	}

	// The text should be the JSON-encoded handler result.
	var greeting map[string]string
	if err := json.Unmarshal([]byte(result.Content[0].Text), &greeting); err != nil {
		t.Fatalf("failed to decode greeting: %v", err)
	}
	if greeting["greeting"] != "hello world" {
		t.Errorf("greeting = %q, want %q", greeting["greeting"], "hello world")
	}
}

func TestToolsCallHandlerError(t *testing.T) {
	t.Setenv("AGENTOS_DEV_MODE", "true")
	reg := NewToolRegistry()
	if err := reg.Register(Tool{
		Name:        "fail",
		Description: "always fails",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, fmt.Errorf("intentional failure")
		},
		Static: true,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	ts := newTestServer(t, reg)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCallWithHeaders(t, ts.URL, "tools/call", ToolCallParams{
		Name:      "fail",
		Arguments: json.RawMessage(`{}`),
	}, map[string]string{"X-Agent-ID": "test-agent"})

	// Handler errors are returned as isError=true in the result, not as RPC errors.
	if resp.Error != nil {
		t.Fatalf("tools/call returned RPC error: %+v", resp.Error)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result ToolCallResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError=true for failed handler")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least 1 content block")
	}
	if result.Content[0].Text != "intentional failure" {
		t.Errorf("error text = %q, want %q", result.Content[0].Text, "intentional failure")
	}
}

func TestToolsCallDeniedWithoutAuthContext(t *testing.T) {
	reg := NewToolRegistry()
	if err := reg.Register(Tool{
		Name:        "internal-tool",
		Description: "requires auth context",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			t.Fatal("handler should not run when auth is missing")
			return nil, nil
		},
		MinClearance: ClearanceInternal,
		Static:       true,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	ts := newTestServer(t, reg)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCall(t, ts.URL, "tools/call", ToolCallParams{
		Name:      "internal-tool",
		Arguments: json.RawMessage(`{}`),
	})

	if resp.Error == nil {
		t.Fatal("expected auth error for missing MCP auth context")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidRequest)
	}
}

func TestToolsCallDeniedForInsufficientClearance(t *testing.T) {
	t.Setenv("AGENTOS_DEV_MODE", "true") // testing clearance, not auth
	reg := NewToolRegistry()
	if err := reg.Register(Tool{
		Name:        "admin-tool",
		Description: "requires admin clearance",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			t.Fatal("handler should not run when clearance is insufficient")
			return nil, nil
		},
		MinClearance: ClearanceAdmin,
		Static:       true,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	store := NewInMemoryClearanceStore(WithAgents(map[string]ClearanceTier{
		"agent-low": ClearancePublic,
	}))
	srv := NewServer(":0", "test-0.0.1", reg, store)
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCallWithHeaders(t, ts.URL, "tools/call", ToolCallParams{
		Name:      "admin-tool",
		Arguments: json.RawMessage(`{}`),
	}, map[string]string{"X-Agent-ID": "agent-low"})

	if resp.Error == nil {
		t.Fatal("expected auth error for insufficient clearance")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidRequest)
	}
}

func TestUnknownMethod(t *testing.T) {
	reg := NewToolRegistry()
	ts := newTestServer(t, reg)
	defer ts.Close()

	resp := rpcCall(t, ts.URL, "nonexistent/method", nil)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
	}
}

func TestUnknownTool(t *testing.T) {
	reg := NewToolRegistry()
	ts := newTestServer(t, reg)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCall(t, ts.URL, "tools/call", ToolCallParams{
		Name:      "nonexistent-tool",
		Arguments: json.RawMessage(`{}`),
	})

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestInvalidJSON(t *testing.T) {
	reg := NewToolRegistry()
	ts := newTestServer(t, reg)
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader([]byte(`{not json`)))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var rpcResp Response
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if rpcResp.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if rpcResp.Error.Code != CodeParseError {
		t.Errorf("error code = %d, want %d", rpcResp.Error.Code, CodeParseError)
	}
}

func TestGETMethodRejected(t *testing.T) {
	reg := NewToolRegistry()
	ts := newTestServer(t, reg)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var rpcResp Response
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if rpcResp.Error == nil {
		t.Fatal("expected error for GET request")
	}
	if rpcResp.Error.Code != CodeInvalidRequest {
		t.Errorf("error code = %d, want %d", rpcResp.Error.Code, CodeInvalidRequest)
	}
}

func TestToolsCall_RejectsUnvalidatedIdentityHeaders(t *testing.T) {
	// When auth context is NOT validated and NOT in dev mode,
	// X-Agent-ID / X-Tenant-ID headers should be rejected.
	t.Setenv("AGENTOS_DEV_MODE", "")

	reg := NewToolRegistry()
	reg.Register(Tool{
		Name:        "ping",
		Description: "test tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Static:      true,
		Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
			return map[string]string{"ok": "true"}, nil
		},
	})

	srv := newTestServer(t, reg)
	defer srv.Close()

	initializeServer(t, srv.URL)
	// Request with spoofed identity headers but no auth — should be rejected.
	resp := rpcCallWithHeaders(t, srv.URL, "tools/call", map[string]any{
		"name":      "ping",
		"arguments": map[string]any{},
	}, map[string]string{
		"X-Agent-ID":  "spoofed-agent",
		"X-Tenant-ID": "spoofed-tenant",
	})

	if resp.Error == nil {
		t.Fatal("expected error for unvalidated identity headers, got success")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("expected code %d, got %d: %s", CodeInvalidRequest, resp.Error.Code, resp.Error.Message)
	}
}

func TestToolsCall_AllowsValidatedIdentityHeaders(t *testing.T) {
	// When auth context IS validated, headers should be accepted.
	t.Setenv("AGENTOS_DEV_MODE", "")

	reg := NewToolRegistry()
	reg.Register(Tool{
		Name:         "ping",
		Description:  "test tool",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		MinClearance: ClearanceInternal,
		Static:       true,
		Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
			mc, ok := GetMCPAuth(ctx)
			if !ok {
				return nil, fmt.Errorf("no MCP auth context")
			}
			return map[string]string{"agent": mc.AgentID}, nil
		},
	})

	// Create server with governance disabled and admin clearance.
	mcpSrv := NewServer(":0", "test", reg, NewInMemoryClearanceStore(WithDefaultTier(ClearanceAdmin)))

	// Wrap handler with middleware that sets validated auth context.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ac := auth.AuthContext{Validated: true, TenantID: "test-tenant"}
		ctx = auth.WithContext(ctx, ac)
		mcpSrv.http.Handler.ServeHTTP(w, r.WithContext(ctx))
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	initializeServer(t, ts.URL)
	resp := rpcCallWithHeaders(t, ts.URL, "tools/call", map[string]any{
		"name":      "ping",
		"arguments": map[string]any{},
	}, map[string]string{
		"X-Agent-ID":  "sre-agent",
		"X-Tenant-ID": "system",
	})

	if resp.Error != nil {
		t.Fatalf("expected success for validated auth, got error: %s", resp.Error.Message)
	}
}

func TestToolsCall_DevModeAllowsUnvalidatedHeaders(t *testing.T) {
	t.Setenv("AGENTOS_DEV_MODE", "true")

	reg := NewToolRegistry()
	reg.Register(Tool{
		Name:         "ping",
		Description:  "test tool",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		MinClearance: ClearanceInternal,
		Static:       true,
		Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
			mc, ok := GetMCPAuth(ctx)
			if !ok {
				return nil, fmt.Errorf("no MCP auth context")
			}
			return map[string]string{"agent": mc.AgentID}, nil
		},
	})

	srv := newTestServer(t, reg)
	defer srv.Close()

	initializeServer(t, srv.URL)
	resp := rpcCallWithHeaders(t, srv.URL, "tools/call", map[string]any{
		"name":      "ping",
		"arguments": map[string]any{},
	}, map[string]string{
		"X-Agent-ID":  "dev-agent",
		"X-Tenant-ID": "dev-tenant",
	})

	if resp.Error != nil {
		t.Fatalf("expected dev mode to allow unvalidated headers, got: %s", resp.Error.Message)
	}
}
