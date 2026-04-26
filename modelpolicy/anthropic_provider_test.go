package modelpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicChatComplete_Basic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key=test-key, got %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version header")
		}

		// Verify system message extracted to top-level.
		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.System != "You are helpful." {
			t.Errorf("expected system='You are helpful.', got %q", req.System)
		}
		// System message should NOT be in messages array.
		for _, m := range req.Messages {
			role, _ := m.Role, m.Content
			if role == "system" {
				t.Error("system message should not be in messages array")
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{
			ID:   "msg_123",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Hello!"},
			},
			StopReason: "end_turn",
			Usage:      anthropicUsage{InputTokens: 10, OutputTokens: 5},
		})
	}))
	defer srv.Close()

	p := &anthropicProvider{
		baseURL:      srv.URL,
		apiKey:       "test-key",
		httpClient:   http.DefaultClient,
		defaultModel: "claude-test",
	}

	resp, err := p.ChatComplete(context.Background(), ChatRequest{
		Model: "claude-test",
		Messages: []ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %q", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestAnthropicChatComplete_ToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{
			ID:   "msg_456",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "I'll fetch that."},
				{Type: "tool_use", ID: "tu_1", Name: "http_fetch", Input: map[string]any{"url": "https://example.com"}},
			},
			StopReason: "tool_use",
			Usage:      anthropicUsage{InputTokens: 20, OutputTokens: 10},
		})
	}))
	defer srv.Close()

	p := &anthropicProvider{
		baseURL:    srv.URL,
		apiKey:     "test-key",
		httpClient: http.DefaultClient,
	}

	resp, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "claude-test",
		Messages: []ChatMessage{{Role: "user", Content: "Fetch example.com"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason=tool_calls, got %q", resp.Choices[0].FinishReason)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Choices[0].Message.ToolCalls))
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.ID != "tu_1" || tc.Function.Name != "http_fetch" {
		t.Errorf("unexpected tool call: %+v", tc)
	}
}

func TestAnthropicChatComplete_ToolResultMapping(t *testing.T) {
	var capturedReq anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{
			ID:         "msg_789",
			Type:       "message",
			Role:       "assistant",
			Content:    []anthropicContentBlock{{Type: "text", Text: "Done"}},
			StopReason: "end_turn",
			Usage:      anthropicUsage{InputTokens: 5, OutputTokens: 5},
		})
	}))
	defer srv.Close()

	p := &anthropicProvider{
		baseURL:    srv.URL,
		apiKey:     "test-key",
		httpClient: http.DefaultClient,
	}

	_, err := p.ChatComplete(context.Background(), ChatRequest{
		Model: "claude-test",
		Messages: []ChatMessage{
			{Role: "user", Content: "Fetch it"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{
				ID: "tu_1", Type: "function",
				Function: FunctionCall{Name: "http_fetch", Arguments: `{"url":"https://example.com"}`},
			}}},
			{Role: "tool", Content: `{"status":200}`, ToolCallID: "tu_1"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The tool result should be mapped as a user message with tool_result content block.
	if len(capturedReq.Messages) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(capturedReq.Messages))
	}
	toolResultMsg := capturedReq.Messages[2]
	if toolResultMsg.Role != "user" {
		t.Errorf("expected tool result mapped as user role, got %q", toolResultMsg.Role)
	}
}

func TestAnthropicChatComplete_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"too many requests"}}`)
	}))
	defer srv.Close()

	p := &anthropicProvider{
		baseURL:    srv.URL,
		apiKey:     "test-key",
		httpClient: http.DefaultClient,
	}

	_, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "claude-test",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 429")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pe.Code != "rate_limited" {
		t.Errorf("expected code=rate_limited, got %q", pe.Code)
	}
	if pe.RetryAfter != "30" {
		t.Errorf("expected retry_after=30, got %q", pe.RetryAfter)
	}
}

func TestAnthropicChatComplete_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	p := &anthropicProvider{
		baseURL:    srv.URL,
		apiKey:     "test-key",
		httpClient: http.DefaultClient,
	}

	_, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "claude-test",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pe.Code != "provider_error" {
		t.Errorf("expected code=provider_error, got %q", pe.Code)
	}
}

func TestAnthropicChatCompleteStream_ContentBlockDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		events := []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
			`{"type":"message_delta","type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		}
		for _, evt := range events {
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", evt)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := &anthropicProvider{
		baseURL:    srv.URL,
		apiKey:     "test-key",
		httpClient: http.DefaultClient,
	}

	ch, err := p.ChatCompleteStream(context.Background(), ChatRequest{
		Model:    "claude-test",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []StreamChunk
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		chunks = append(chunks, chunk)
	}

	// Should have text deltas and a final chunk.
	var fullText strings.Builder
	for _, c := range chunks {
		fullText.WriteString(c.Delta)
	}
	if fullText.String() != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", fullText.String())
	}
}
