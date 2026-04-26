package modelpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

// newTestProvider creates an openaiProvider pointing at the given test server URL.
func newTestProvider(url string) *openaiProvider {
	return newOpenAIProvider(config.ModelConfig{
		Provider: "openai",
		BaseURL:  url,
		APIKey:   "test-key-123",
		Timeout:  5 * time.Second,
	})
}

func TestChatCompleteSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key-123" {
			t.Errorf("expected Bearer test-key-123, got %s", auth)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}

		// Verify request body
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "gpt-4" {
			t.Errorf("expected model gpt-4, got %s", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}

		resp := ChatResponse{
			Model: "gpt-4",
			Choices: []ChatChoice{
				{
					Message:      ChatMessage{Role: "assistant", Content: "Hello! How can I help you?"},
					FinishReason: "stop",
				},
			},
			Usage: ChatUsage{
				PromptTokens:     10,
				CompletionTokens: 8,
				TotalTokens:      18,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	resp, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != "gpt-4" {
		t.Errorf("expected model gpt-4, got %s", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello! How can I help you?" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason stop, got %s", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 8 || resp.Usage.TotalTokens != 18 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestChatCompleteStreamSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		chunks := []string{
			`{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":"!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	ch, err := p.ChatCompleteStream(context.Background(), ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var deltas []string
	var lastChunk StreamChunk
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		if chunk.Delta != "" {
			deltas = append(deltas, chunk.Delta)
		}
		lastChunk = chunk
	}

	combined := strings.Join(deltas, "")
	if combined != "Hello world!" {
		t.Errorf("expected 'Hello world!', got '%s'", combined)
	}
	if lastChunk.FinishReason != "stop" {
		t.Errorf("expected finish_reason stop in last chunk, got %s", lastChunk.FinishReason)
	}
	if lastChunk.Usage == nil {
		t.Fatal("expected usage in final chunk")
	}
	if lastChunk.Usage.TotalTokens != 8 {
		t.Errorf("expected total_tokens=8, got %d", lastChunk.Usage.TotalTokens)
	}
}

func TestChatCompleteHTTP429RetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	_, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 429")
	}

	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if pe.StatusCode != 429 {
		t.Errorf("expected status 429, got %d", pe.StatusCode)
	}
	if pe.RetryAfter != "30" {
		t.Errorf("expected Retry-After=30, got %s", pe.RetryAfter)
	}
	if pe.Code != "rate_limited" {
		t.Errorf("expected code rate_limited, got %s", pe.Code)
	}
	if !strings.Contains(pe.Error(), "retry_after=30") {
		t.Errorf("expected error message to contain retry_after=30, got %s", pe.Error())
	}
}

func TestChatCompleteHTTP500Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"internal server error"}}`))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	_, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 500")
	}

	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if pe.StatusCode != 500 {
		t.Errorf("expected status 500, got %d", pe.StatusCode)
	}
	if pe.Code != "provider_error" {
		t.Errorf("expected code provider_error, got %s", pe.Code)
	}
}

func TestChatCompleteTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the client timeout
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newOpenAIProvider(config.ModelConfig{
		Provider: "openai",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Timeout:  500 * time.Millisecond,
	})

	_, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "Timeout") {
		t.Errorf("expected timeout-related error, got: %v", err)
	}
}

func TestChatCompleteTokenUsageExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Model: "gpt-4",
			Choices: []ChatChoice{
				{
					Message:      ChatMessage{Role: "assistant", Content: "hi"},
					FinishReason: "stop",
				},
			},
			Usage: ChatUsage{
				PromptTokens:     42,
				CompletionTokens: 7,
				TotalTokens:      49,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	resp, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Usage.PromptTokens != 42 {
		t.Errorf("expected prompt_tokens=42, got %d", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 7 {
		t.Errorf("expected completion_tokens=7, got %d", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 49 {
		t.Errorf("expected total_tokens=49, got %d", resp.Usage.TotalTokens)
	}
}

func TestStubProviderChatComplete(t *testing.T) {
	sp := &stubProvider{}
	resp, err := sp.ChatComplete(context.Background(), ChatRequest{
		Model:    "local-stub-llm",
		Messages: []ChatMessage{{Role: "user", Content: "hello world"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected role assistant, got %s", resp.Choices[0].Message.Role)
	}
	if respText := MessageText(resp.Choices[0].Message); !strings.Contains(respText, "hello world") {
		t.Errorf("expected response to contain input, got %s", respText)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason stop, got %s", resp.Choices[0].FinishReason)
	}
	if resp.Usage.TotalTokens <= 0 {
		t.Errorf("expected positive total_tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestStubProviderChatCompleteStream(t *testing.T) {
	sp := &stubProvider{}
	ch, err := sp.ChatCompleteStream(context.Background(), ChatRequest{
		Model:    "local-stub-llm",
		Messages: []ChatMessage{{Role: "user", Content: "test stream"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []StreamChunk
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	last := chunks[len(chunks)-1]
	if last.FinishReason != "stop" {
		t.Errorf("expected finish_reason stop, got %s", last.FinishReason)
	}
	if last.Usage == nil {
		t.Fatal("expected usage in final chunk")
	}
}

func TestNewProviderFactory(t *testing.T) {
	// Test stub provider creation
	p, err := newProvider(config.ModelConfig{Provider: "stub"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*stubProvider); !ok {
		t.Errorf("expected *stubProvider, got %T", p)
	}

	// Test empty provider returns error (fail-fast, no silent stub default)
	_, err = newProvider(config.ModelConfig{Provider: ""})
	if err == nil {
		t.Fatal("expected error for empty provider")
	}
	if !strings.Contains(err.Error(), "AGENTOS_MODEL_PROVIDER is required") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Test openai provider creation
	p, err = newProvider(config.ModelConfig{
		Provider: "openai",
		BaseURL:  "http://localhost:8080",
		APIKey:   "key",
		Timeout:  30 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*ResilientProvider); !ok {
		t.Errorf("expected *ResilientProvider (wrapping openaiProvider), got %T", p)
	}

	// Test unknown provider
	_, err = newProvider(config.ModelConfig{Provider: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown model provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStreamingContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		// Send one chunk, then hang
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`)
		flusher.Flush()

		// Wait for client to cancel
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.ChatCompleteStream(ctx, ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read first chunk
	chunk := <-ch
	if chunk.Delta != "hello" {
		t.Errorf("expected delta 'hello', got '%s'", chunk.Delta)
	}

	// Cancel and drain
	cancel()

	// Channel should close eventually
	for range ch {
		// drain remaining chunks
	}
}

func TestTokenUsageFeedsUsageMeter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Model: "gpt-4",
			Choices: []ChatChoice{
				{
					Message:      ChatMessage{Role: "assistant", Content: "hello"},
					FinishReason: "stop",
				},
			},
			Usage: ChatUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	resp, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage can be fed to the usage meter
	meter := newUsageMeter()
	usageMap := map[string]any{
		"prompt_tokens":     resp.Usage.PromptTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
		"total_tokens":      resp.Usage.TotalTokens,
	}
	meter.Record("test-tenant", usageMap)

	hourly, daily := meter.GetUsage("test-tenant")
	if hourly != 150 {
		t.Errorf("expected hourly usage 150, got %d", hourly)
	}
	if daily != 150 {
		t.Errorf("expected daily usage 150, got %d", daily)
	}
}
