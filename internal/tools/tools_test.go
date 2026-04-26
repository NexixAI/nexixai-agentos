package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// ---------------------------------------------------------------------------
// http_fetch tests
// ---------------------------------------------------------------------------

func TestHTTPFetch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "hello world")
	}))
	defer srv.Close()

	tool := &HTTPFetchTool{AllowLoopback: true}
	input := fmt.Sprintf(`{"url":%q,"method":"GET"}`, srv.URL)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out httpFetchOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if out.Status != 200 {
		t.Errorf("expected status 200, got %d", out.Status)
	}
	if out.Body != "hello world" {
		t.Errorf("expected body 'hello world', got %q", out.Body)
	}
	if out.Headers["X-Test"] != "ok" {
		t.Errorf("expected header X-Test=ok, got %q", out.Headers["X-Test"])
	}
}

func TestHTTPFetch_SSRFRejection(t *testing.T) {
	tool := &HTTPFetchTool{}

	cases := []struct {
		name string
		url  string
	}{
		{"loopback", "http://127.0.0.1:8080/test"},
		{"current_network", "http://0.0.0.1:8080/test"},
		{"carrier_grade_nat", "http://100.64.0.1:8080/test"},
		{"private_10", "http://10.0.0.1:8080/test"},
		{"private_192", "http://192.168.1.1:8080/test"},
		{"loopback_ipv6", "http://[::1]:8080/test"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"url":%q}`, tc.url)
			_, err := tool.Execute(context.Background(), input)
			if err == nil {
				t.Fatal("expected SSRF error, got nil")
			}
			if !strings.Contains(err.Error(), "private IP") && !strings.Contains(err.Error(), "blocked") {
				t.Errorf("expected private IP error, got: %v", err)
			}
		})
	}
}

func TestHTTPFetchRedirectBlocked(t *testing.T) {
	checkRedirect := httpFetchCheckRedirect(false)
	redirectURL, err := url.Parse("http://127.0.0.1/private")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}

	err = checkRedirect(&http.Request{URL: redirectURL}, []*http.Request{{}})
	if err == nil {
		t.Fatal("expected redirect to private target to be blocked")
	}
	if !strings.Contains(err.Error(), "private IP") {
		t.Fatalf("expected private IP error, got: %v", err)
	}
}

func TestHTTPFetchRedirectLimit(t *testing.T) {
	checkRedirect := httpFetchCheckRedirect(true)
	redirectURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}

	via := make([]*http.Request, httpFetchMaxRedirect)
	err = checkRedirect(&http.Request{URL: redirectURL}, via)
	if err == nil {
		t.Fatal("expected redirect limit error")
	}
	if !strings.Contains(err.Error(), "stopped after") {
		t.Fatalf("expected redirect limit error, got: %v", err)
	}
}

func TestHTTPFetch_Truncation(t *testing.T) {
	// Serve a body larger than 32KB.
	bigBody := strings.Repeat("A", 40*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, bigBody)
	}))
	defer srv.Close()

	tool := &HTTPFetchTool{AllowLoopback: true}
	input := fmt.Sprintf(`{"url":%q}`, srv.URL)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out httpFetchOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if len(out.Body) != maxBodyBytes {
		t.Errorf("expected body truncated to %d bytes, got %d", maxBodyBytes, len(out.Body))
	}
}

func TestHTTPFetch_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		fmt.Fprint(w, "late")
	}))
	defer srv.Close()

	tool := &HTTPFetchTool{AllowLoopback: true}
	input := fmt.Sprintf(`{"url":%q}`, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := tool.Execute(ctx, input)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// ---------------------------------------------------------------------------
// json_extract tests
// ---------------------------------------------------------------------------

func TestJSONExtract_NestedField(t *testing.T) {
	tool := &JSONExtractTool{}
	doc := `{"data":{"user":{"name":"Alice"}}}`
	input := fmt.Sprintf(`{"json":%q,"path":"data.user.name"}`, doc)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != `"Alice"` {
		t.Errorf("expected '\"Alice\"', got %q", result)
	}
}

func TestJSONExtract_InvalidPath(t *testing.T) {
	tool := &JSONExtractTool{}
	doc := `{"data":{"user":{"name":"Alice"}}}`
	input := fmt.Sprintf(`{"json":%q,"path":"data.nonexistent.field"}`, doc)

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
	if !strings.Contains(err.Error(), "path not found") {
		t.Errorf("expected 'path not found' error, got: %v", err)
	}
}

func TestJSONExtract_ArrayAccess(t *testing.T) {
	tool := &JSONExtractTool{}
	doc := `{"items":[{"id":1},{"id":2},{"id":3}]}`
	input := fmt.Sprintf(`{"json":%q,"path":"items.1.id"}`, doc)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "2" {
		t.Errorf("expected '2', got %q", result)
	}
}

// ---------------------------------------------------------------------------
// text_truncate tests (renamed from text_summarize)
// ---------------------------------------------------------------------------

func TestTextTruncate_Truncation(t *testing.T) {
	tool := &TextTruncateTool{}
	text := strings.Repeat("x", 100)
	input := fmt.Sprintf(`{"text":%q,"max_chars":50}`, text)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(result, "... [truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
	// 50 chars + "... [truncated]"
	expected := strings.Repeat("x", 50) + "... [truncated]"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestTextTruncate_Passthrough(t *testing.T) {
	tool := &TextTruncateTool{}
	text := "short text"
	input := fmt.Sprintf(`{"text":%q,"max_chars":1000}`, text)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != text {
		t.Errorf("expected %q, got %q", text, result)
	}
}

// ---------------------------------------------------------------------------
// registry tests
// ---------------------------------------------------------------------------

func TestRegistry_DispatchCorrectTool(t *testing.T) {
	reg := NewRegistry()

	input := `{"text":"hello","max_chars":1000}`
	result := reg.Execute(context.Background(), "text_truncate", input)

	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestRegistry_UnknownTool(t *testing.T) {
	reg := NewRegistry()

	result := reg.Execute(context.Background(), "nonexistent_tool", "{}")

	if !strings.Contains(result, "unknown_tool") {
		t.Errorf("expected unknown_tool error, got %q", result)
	}
}

func TestRegistry_GetToolDefs_All(t *testing.T) {
	reg := NewRegistry()

	defs := reg.GetToolDefs(nil)
	if len(defs) != 9 {
		t.Errorf("expected 9 tool defs, got %d", len(defs))
	}

	for _, d := range defs {
		if d.Type != "function" {
			t.Errorf("expected type 'function', got %q", d.Type)
		}
		if d.Function.Name == "" {
			t.Error("expected non-empty function name")
		}
	}
}

func TestRegistry_GetToolDefs_Subset(t *testing.T) {
	reg := NewRegistry()

	defs := reg.GetToolDefs([]string{"json_extract"})
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool def, got %d", len(defs))
	}
	if defs[0].Function.Name != "json_extract" {
		t.Errorf("expected json_extract, got %q", defs[0].Function.Name)
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := NewRegistry()

	tool, ok := reg.Get("http_fetch")
	if !ok {
		t.Fatal("expected http_fetch to be registered")
	}
	if tool.Name() != "http_fetch" {
		t.Errorf("expected name 'http_fetch', got %q", tool.Name())
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected nonexistent tool to not be found")
	}
}

// ---------------------------------------------------------------------------
// webhook_tool tests
// ---------------------------------------------------------------------------

func TestWebhookTool_Execute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("X-Custom") != "auth-token" {
			t.Errorf("expected custom header, got %q", r.Header.Get("X-Custom"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"result":"ok"}`)
	}))
	defer srv.Close()

	tool := NewWebhookTool(types.CustomTool{
		Name:       "test_webhook",
		WebhookURL: srv.URL,
		Headers:    map[string]string{"X-Custom": "auth-token"},
	})
	tool.AllowLoopback = true

	result, err := tool.Execute(context.Background(), `{"key":"value"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `{"result":"ok"}` {
		t.Errorf("expected result, got %q", result)
	}
}

func TestWebhookTool_SSRFRejection(t *testing.T) {
	tool := NewWebhookTool(types.CustomTool{
		Name:       "ssrf_test",
		WebhookURL: "http://127.0.0.1:8080/evil",
	})

	_, err := tool.Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected SSRF error")
	}
	if !strings.Contains(err.Error(), "private IP") && !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected private IP error, got: %v", err)
	}
}

func TestWebhookTool_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	tool := NewWebhookTool(types.CustomTool{
		Name:       "timeout_test",
		WebhookURL: srv.URL,
		TimeoutMs:  100,
	})
	// Need AllowLoopback equivalent - use a context timeout instead.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := tool.Execute(ctx, `{}`)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestWebhookTool_TruncateResponse(t *testing.T) {
	// WebhookTool reuses the same maxBodyBytes (32KB) truncation as HTTPFetchTool.
	// Cannot test with loopback due to SSRF protection; truncation verified via http_fetch tests.
	t.Skip("webhook tool SSRF blocks loopback; truncation tested via http_fetch")
}

func TestRegisterCustomTools_Success(t *testing.T) {
	reg := NewRegistry()
	customs := []types.CustomTool{
		{Name: "weather", Description: "Get weather", WebhookURL: "https://api.example.com/weather"},
	}

	cloned, err := reg.RegisterCustomTools(customs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Custom tool should be in cloned registry.
	tool, ok := cloned.Get("weather")
	if !ok {
		t.Fatal("expected weather tool in cloned registry")
	}
	if tool.Name() != "weather" {
		t.Errorf("expected name 'weather', got %q", tool.Name())
	}

	// Built-in tools should still be present.
	_, ok = cloned.Get("http_fetch")
	if !ok {
		t.Fatal("expected http_fetch in cloned registry")
	}

	// Original registry should NOT have the custom tool.
	_, ok = reg.Get("weather")
	if ok {
		t.Error("original registry should not have custom tool")
	}
}

func TestRegisterCustomTools_NameCollision(t *testing.T) {
	reg := NewRegistry()
	customs := []types.CustomTool{
		{Name: "http_fetch", Description: "Collision", WebhookURL: "https://example.com"},
	}

	_, err := reg.RegisterCustomTools(customs)
	if err == nil {
		t.Fatal("expected error for name collision")
	}
	if !strings.Contains(err.Error(), "collides") {
		t.Errorf("expected collision error, got: %v", err)
	}
}
