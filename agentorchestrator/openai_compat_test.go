package agentorchestrator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

// newOpenAITestServer builds a minimal Server with the given provider, suitable
// for testing the OpenAI-compatibility endpoints without full env-based
// storage initialisation. It wires the handler via handleChatCompletions and
// handleModels directly (no auth middleware) so tests stay fast and isolated.
func newOpenAITestServer(t *testing.T, provider ModelProvider) *Server {
	t.Helper()
	runStore := newTestRunStore(t)
	memStore := newTestMemoryStore(t)
	exec := NewExecutor(
		newTestExecConfig(),
		newTestStorageConfig(),
		provider,
		nil, // no tool registry needed
		memStore,
		runStore,
		noopAuditLogger{},
		WithDefaultModel("test-default-model"),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		exec.Shutdown(ctx)
	})
	return &Server{
		version:      "test",
		defaultModel: "test-default-model",
		executor:     exec,
	}
}

// serveChat is a convenience helper: POST /v1/chat/completions with the given
// JSON body, returning the recorder.
func serveChat(srv *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	// Inject auth context with a test tenant (tenant_id is now required).
	ac := auth.AuthContext{TenantID: "tnt_test"}
	req = req.WithContext(auth.WithContext(req.Context(), ac))
	rec := httptest.NewRecorder()
	srv.handleChatCompletions(rec, req)
	return rec
}

// --- Non-streaming tests ---

func TestChatCompletions_ValidRequest(t *testing.T) {
	provider := newMockProvider(&modelpolicy.ChatResponse{
		Model: "gpt-test",
		Choices: []modelpolicy.ChatChoice{{
			Index:        0,
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "Hello!"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
	})
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{
		"model": "gpt-test",
		"messages": [{"role":"user","content":"Hi"}]
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp openaiChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Object != "chat.completion" {
		t.Errorf("expected object chat.completion, got %s", resp.Object)
	}
	if !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Errorf("expected id prefix chatcmpl-, got %s", resp.ID)
	}
	if resp.Model != "gpt-test" {
		t.Errorf("expected model gpt-test, got %s", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("expected content 'Hello!', got %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason stop, got %s", resp.Choices[0].FinishReason)
	}
	if resp.Usage.TotalTokens != 11 {
		t.Errorf("expected total_tokens 11, got %d", resp.Usage.TotalTokens)
	}
	if provider.CallCount() != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.CallCount())
	}
}

func TestChatCompletions_ModelDefaultFallback(t *testing.T) {
	// When the request omits the model field, the server should fill in its
	// defaultModel before calling the provider.
	var captured modelpolicy.ChatRequest
	provider := &capturingProvider{
		response: &modelpolicy.ChatResponse{
			Model: "test-default-model",
			Choices: []modelpolicy.ChatChoice{{
				Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: modelpolicy.ChatUsage{},
		},
		captured: &captured,
	}
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{"messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.Model != "test-default-model" {
		t.Errorf("expected model to be set to default 'test-default-model', got %q", captured.Model)
	}
}

func TestChatCompletions_ModelPassedThrough(t *testing.T) {
	var captured modelpolicy.ChatRequest
	provider := &capturingProvider{
		response: &modelpolicy.ChatResponse{
			Model: "custom-model-7b",
			Choices: []modelpolicy.ChatChoice{{
				Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: modelpolicy.ChatUsage{},
		},
		captured: &captured,
	}
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{"model":"custom-model-7b","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.Model != "custom-model-7b" {
		t.Errorf("expected model custom-model-7b, got %q", captured.Model)
	}
}

func TestChatCompletions_InvalidJSON(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{not valid json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var errResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	errObj, ok := errResp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object in response, got %+v", errResp)
	}
	if errObj["code"] != "invalid_json" {
		t.Errorf("expected error code invalid_json, got %v", errObj["code"])
	}
	if provider.CallCount() != 0 {
		t.Errorf("provider should not have been called for invalid JSON")
	}
}

func TestChatCompletions_EmptyBody(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, ``)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletions_MethodNotAllowed(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	srv.handleChatCompletions(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletions_ProviderError(t *testing.T) {
	provider := &mockProvider{
		errors: []error{fmt.Errorf("upstream timeout")},
	}
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}

	var errResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errObj := errResp["error"].(map[string]any)
	if errObj["code"] != "model_error" {
		t.Errorf("expected error code model_error, got %v", errObj["code"])
	}
	if !strings.Contains(errObj["message"].(string), "upstream timeout") {
		t.Errorf("expected error message to contain 'upstream timeout', got %v", errObj["message"])
	}
}

// --- Streaming tests (non-streaming provider wrapped in SSE) ---

func TestChatCompletions_StreamFallback(t *testing.T) {
	// When the provider does NOT implement ModelStreamProvider, a stream=true
	// request should be served as a single SSE chunk + [DONE].
	provider := newMockProvider(&modelpolicy.ChatResponse{
		Model: "fallback-model",
		Choices: []modelpolicy.ChatChoice{{
			Index:        0,
			Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "streamed fallback"},
			FinishReason: "stop",
		}},
		Usage: modelpolicy.ChatUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	})
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{"model":"fallback-model","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", ct)
	}

	// Parse SSE lines.
	body := rec.Body.String()
	lines := strings.Split(body, "\n")
	var dataLines []string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	if len(dataLines) < 2 {
		t.Fatalf("expected at least 2 data lines (chunk + [DONE]), got %d in body:\n%s", len(dataLines), body)
	}

	// Last data line should be [DONE].
	if dataLines[len(dataLines)-1] != "[DONE]" {
		t.Errorf("expected last data line to be [DONE], got %q", dataLines[len(dataLines)-1])
	}

	// First data line should be a valid chunk.
	var chunk openaiStreamChunk
	if err := json.Unmarshal([]byte(dataLines[0]), &chunk); err != nil {
		t.Fatalf("unmarshal stream chunk: %v", err)
	}
	if chunk.Object != "chat.completion.chunk" {
		t.Errorf("expected object chat.completion.chunk, got %s", chunk.Object)
	}
	if chunk.Model != "fallback-model" {
		t.Errorf("expected model fallback-model, got %s", chunk.Model)
	}
	if len(chunk.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(chunk.Choices))
	}
	if chunk.Choices[0].Delta.Content != "streamed fallback" {
		t.Errorf("expected delta content 'streamed fallback', got %q", chunk.Choices[0].Delta.Content)
	}
}

func TestChatCompletions_PreservesChoiceIndexes(t *testing.T) {
	provider := newMockProvider(&modelpolicy.ChatResponse{
		Model: "gpt-test",
		Choices: []modelpolicy.ChatChoice{
			{
				Index:        0,
				Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "first"},
				FinishReason: "stop",
			},
			{
				Index:        3,
				Message:      modelpolicy.ChatMessage{Role: "assistant", Content: "second"},
				FinishReason: "length",
			},
		},
		Usage: modelpolicy.ChatUsage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9},
	})
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{
		"model": "gpt-test",
		"messages": [{"role":"user","content":"Hi"}]
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp openaiChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Index != 0 {
		t.Errorf("expected first choice index 0, got %d", resp.Choices[0].Index)
	}
	if resp.Choices[1].Index != 3 {
		t.Errorf("expected second choice index 3, got %d", resp.Choices[1].Index)
	}
}

func TestChatCompletions_StreamProviderError(t *testing.T) {
	// When streaming falls back to non-streaming and the provider errors,
	// we should get a 502.
	provider := &mockProvider{
		errors: []error{fmt.Errorf("provider down")},
	}
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- True streaming (provider implements ModelStreamProvider) ---

func TestChatCompletions_TrueStreaming(t *testing.T) {
	base := newMockProvider() // not used for streaming path
	sp := &mockStreamProvider{
		mockProvider: base,
		streamChunks: []modelpolicy.StreamChunk{
			{Index: 0, Delta: "Hello"},
			{Delta: " world"},
			{Index: 2, Delta: "!", FinishReason: "stop", Usage: &modelpolicy.ChatUsage{
				PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13,
			}},
		},
	}
	srv := newOpenAITestServer(t, sp)

	rec := serveChat(srv, `{"model":"stream-model","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", ct)
	}

	// Parse SSE data lines.
	scanner := bufio.NewScanner(rec.Body)
	var chunks []openaiStreamChunk
	gotDone := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			gotDone = true
			continue
		}
		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("unmarshal chunk: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if !gotDone {
		t.Error("expected [DONE] sentinel in SSE stream")
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	// First chunk should have role=assistant.
	if chunks[0].Choices[0].Delta.Role != "assistant" {
		t.Errorf("expected first chunk delta role 'assistant', got %q", chunks[0].Choices[0].Delta.Role)
	}
	if chunks[0].Choices[0].Delta.Content != "Hello" {
		t.Errorf("expected first chunk content 'Hello', got %q", chunks[0].Choices[0].Delta.Content)
	}

	// Second chunk should not have role.
	if chunks[1].Choices[0].Delta.Role != "" {
		t.Errorf("expected subsequent chunk to have empty role, got %q", chunks[1].Choices[0].Delta.Role)
	}

	// Last chunk should have finish_reason and usage.
	last := chunks[2]
	if last.Choices[0].Index != 2 {
		t.Errorf("expected last chunk index 2, got %d", last.Choices[0].Index)
	}
	if last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason stop on last chunk")
	}
	if last.Usage == nil || last.Usage.TotalTokens != 13 {
		t.Errorf("expected usage with total_tokens=13 on last chunk, got %+v", last.Usage)
	}

	// All chunks should share the same completion ID.
	id := chunks[0].ID
	for i, c := range chunks {
		if c.ID != id {
			t.Errorf("chunk %d has different ID %q (expected %q)", i, c.ID, id)
		}
	}
}

func TestChatCompletions_TrueStreamingStopsWithoutDoneOnProviderChunkError(t *testing.T) {
	base := newMockProvider()
	sp := &mockStreamProvider{
		mockProvider: base,
		streamChunks: []modelpolicy.StreamChunk{
			{Index: 0, Delta: "partial"},
			{Err: fmt.Errorf("stream exploded")},
		},
	}
	srv := newOpenAITestServer(t, sp)

	rec := serveChat(srv, `{"model":"stream-model","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, "data: [DONE]") {
		t.Fatalf("did not expect [DONE] after stream error, body:\n%s", body)
	}
	if !strings.Contains(body, "partial") {
		t.Fatalf("expected partial chunk before error, body:\n%s", body)
	}
}

func TestChatCompletions_TrueStreamingProviderInitError(t *testing.T) {
	base := newMockProvider()
	sp := &errStreamProvider{
		mockProvider: base,
		err:          fmt.Errorf("stream init failed"),
	}
	srv := newOpenAITestServer(t, sp)

	rec := serveChat(srv, `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- GET /v1/models ---

func TestHandleModels(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	srv.handleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp openaiModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("expected object 'list', got %s", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 model, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != "test-default-model" {
		t.Errorf("expected model id test-default-model, got %s", resp.Data[0].ID)
	}
	if resp.Data[0].OwnedBy != "agentos" {
		t.Errorf("expected owned_by agentos, got %s", resp.Data[0].OwnedBy)
	}
}

func TestHandleModels_ProxiesToUpstream(t *testing.T) {
	// Start a fake vLLM that serves /v1/models.
	upstreamModels := openaiModelList{
		Object: "list",
		Data: []openaiModel{
			{ID: "Qwen/Qwen2.5-Coder-32B-Instruct-AWQ", Object: "model", Created: 1, OwnedBy: "vllm"},
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(upstreamModels)
	}))
	defer upstream.Close()

	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)
	// Point modelBaseURL at the fake upstream (note: base URL includes /v1).
	srv.modelBaseURL = upstream.URL + "/v1"

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	srv.handleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp openaiModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 model, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != "Qwen/Qwen2.5-Coder-32B-Instruct-AWQ" {
		t.Errorf("expected proxied model id, got %s", resp.Data[0].ID)
	}
	if resp.Data[0].OwnedBy != "vllm" {
		t.Errorf("expected owned_by vllm, got %s", resp.Data[0].OwnedBy)
	}
}

func TestHandleModels_FallbackOnUpstreamError(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)
	// Point at a non-existent upstream.
	srv.modelBaseURL = "http://127.0.0.1:1/v1"

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	srv.handleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp openaiModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "test-default-model" {
		t.Errorf("expected fallback to default model, got %+v", resp.Data)
	}
}

func TestHandleModels_MethodNotAllowed(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	req := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	rec := httptest.NewRecorder()
	srv.handleModels(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// --- Test helpers ---

// capturingProvider records the ChatRequest it receives.
type capturingProvider struct {
	response *modelpolicy.ChatResponse
	captured *modelpolicy.ChatRequest
}

func (c *capturingProvider) ChatComplete(_ context.Context, req modelpolicy.ChatRequest) (*modelpolicy.ChatResponse, error) {
	*c.captured = req
	return c.response, nil
}

// errStreamProvider implements ModelStreamProvider but always errors on
// ChatCompleteStream.
type errStreamProvider struct {
	*mockProvider
	err error
}

func (e *errStreamProvider) ChatCompleteStream(_ context.Context, _ modelpolicy.ChatRequest) (<-chan modelpolicy.StreamChunk, error) {
	return nil, e.err
}

// ---------------------------------------------------------------------------
// v11.1 multipart (vision) HTTP tests
// ---------------------------------------------------------------------------

// serveChatWithScopes injects a specific OIDC scopes list into the auth
// context. Used by v11.1 tests that exercise the multimodal scope gate.
func serveChatWithScopes(srv *Server, body string, scopes ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ac := auth.AuthContext{TenantID: "tnt_test", Scopes: scopes}
	req = req.WithContext(auth.WithContext(req.Context(), ac))
	rec := httptest.NewRecorder()
	srv.handleChatCompletions(rec, req)
	return rec
}

func TestChatCompletions_V11_1_MultipartTextOnlyStillText(t *testing.T) {
	// Backward compat: a text-only request wrapped in the "multipart with
	// only text parts" form should behave like a text call (no gate, no
	// audit, classifier still runs). Though we don't enable classifier in
	// test by default — at minimum, no 400/403.
	provider := newMockProvider(&modelpolicy.ChatResponse{
		Model: "test-default-model",
		Choices: []modelpolicy.ChatChoice{{
			Message: modelpolicy.ChatMessage{Role: "assistant", Content: "ok"},
		}},
	})
	srv := newOpenAITestServer(t, provider)

	rec := serveChat(srv, `{
		"model":"test-default-model",
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for multipart-text-only, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletions_V11_1_MultipartWithImageMissingScope(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	body := `{
		"model":"test-default-model",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe"},
			{"type":"image_url","image_url":{"url":"https://example.com/x.png"}}
		]}]
	}`
	rec := serveChat(srv, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for multipart without scope, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "scope_missing") {
		t.Errorf("expected scope_missing error code, got body: %s", rec.Body.String())
	}
}

func TestChatCompletions_V11_1_MultipartWithImageAndScope(t *testing.T) {
	provider := newMockProvider(&modelpolicy.ChatResponse{
		Model: "test-default-model",
		Choices: []modelpolicy.ChatChoice{{
			Message: modelpolicy.ChatMessage{Role: "assistant", Content: "a dog"},
		}},
	})
	srv := newOpenAITestServer(t, provider)

	body := `{
		"model":"test-default-model",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"what is this?"},
			{"type":"image_url","image_url":{"url":"https://example.com/dog.png"}}
		]}]
	}`
	rec := serveChatWithScopes(srv, body, "chat:multimodal")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with scope, got %d: %s", rec.Code, rec.Body.String())
	}
	// The mock provider should have received the ChatRequest with Content as
	// a multipart slice (preserved through), not stringified.
	last := provider.LastRequest()
	if last == nil {
		t.Fatal("provider did not receive a request")
	}
	if _, isString := last.Messages[0].Content.(string); isString {
		t.Errorf("provider saw stringified Content; expected multipart slice")
	}
}

func TestChatCompletions_V11_1_RejectsBadSchemes(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	cases := []struct {
		name string
		url  string
	}{
		{"file", "file:///etc/passwd"},
		{"data", "data:image/png;base64,AAA"},
		{"empty", ""},
		{"ftp", "ftp://example.com/x.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"model":"test-default-model",
				"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":%q}}]}]
			}`, tc.url)
			rec := serveChatWithScopes(srv, body, "chat:multimodal")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for url %q, got %d: %s", tc.url, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "invalid_content") {
				t.Errorf("expected invalid_content code, got: %s", rec.Body.String())
			}
		})
	}
}

func TestChatCompletions_V11_1_RejectsCapExceeded(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	// Build 9 image parts (exceeds multimodal.MaxImageParts = 8).
	var parts []string
	for i := 0; i < 9; i++ {
		parts = append(parts, `{"type":"image_url","image_url":{"url":"https://x/y.png"}}`)
	}
	body := fmt.Sprintf(`{"model":"test-default-model","messages":[{"role":"user","content":[%s]}]}`, strings.Join(parts, ","))
	rec := serveChatWithScopes(srv, body, "chat:multimodal")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for cap exceeded, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "image part cap") {
		t.Errorf("expected cap error, got: %s", rec.Body.String())
	}
}

func TestChatCompletions_V11_1_RejectsUnsupportedContentType(t *testing.T) {
	provider := newMockProvider()
	srv := newOpenAITestServer(t, provider)

	body := `{"model":"test-default-model","messages":[{"role":"user","content":[{"type":"audio","text":"beep"}]}]}`
	rec := serveChatWithScopes(srv, body, "chat:multimodal")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for audio part, got %d", rec.Code)
	}
}
