package modelpolicy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/tokens"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// ProviderError represents a structured error from the model provider.
type ProviderError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter string // Retry-After header value, if present (HTTP 429)
}

func (e *ProviderError) Error() string {
	if e.RetryAfter != "" {
		return fmt.Sprintf("provider_error: %d %s (retry_after=%s)", e.StatusCode, e.Message, e.RetryAfter)
	}
	return fmt.Sprintf("provider_error: %d %s", e.StatusCode, e.Message)
}

// openaiProvider implements the provider interface for OpenAI-compatible APIs.
type openaiProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	timeout    time.Duration
}

// newOpenAIProvider creates a new OpenAI-compatible provider from configuration.
func newOpenAIProvider(cfg config.ModelConfig) *openaiProvider {
	return &openaiProvider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		timeout: cfg.Timeout,
	}
}

// Invoke implements the legacy provider interface. It translates an invoke request
// into a chat completion call and returns the result in the legacy format.
func (p *openaiProvider) Invoke(req types.ModelInvokeRequest) (map[string]any, map[string]any, error) {
	inputText, _ := req.Input["text"].(string)
	chatReq := ChatRequest{
		Model: req.ModelID,
		Messages: []ChatMessage{
			{Role: "user", Content: inputText},
		},
	}

	resp, err := p.ChatComplete(context.Background(), chatReq)
	if err != nil {
		return nil, nil, err
	}

	text := ""
	if len(resp.Choices) > 0 {
		text = MessageText(resp.Choices[0].Message)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	output := map[string]any{
		"type": "text",
		"text": text,
		"echo": req.Input,
		"ts":   now,
	}

	usage := map[string]any{
		"prompt_tokens":     resp.Usage.PromptTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
		"total_tokens":      resp.Usage.TotalTokens,
		"model_id":          req.ModelID,
		"provider":          "openai",
		"timestamp":         now,
	}

	return output, usage, nil
}

// ChatComplete sends a non-streaming chat completions request.
func (p *openaiProvider) ChatComplete(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false

	body, err := marshalChatRequest(req)
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider_error: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, p.mapHTTPError(httpResp)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("provider_error: failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// ChatCompleteStream sends a streaming chat completions request.
// Tokens are delivered via the returned channel. The channel closes on completion.
func (p *openaiProvider) ChatCompleteStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	req.Stream = true

	body, err := marshalChatRequest(req)
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider_error: request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		return nil, p.mapHTTPError(httpResp)
	}

	ch := make(chan StreamChunk, 16)
	go p.readSSE(ctx, httpResp.Body, ch)
	return ch, nil
}

// sseChunkResponse is the JSON structure within each SSE data line.
type sseChunkResponse struct {
	Choices []sseChunkChoice `json:"choices"`
	Usage   *ChatUsage       `json:"usage,omitempty"`
}

type sseChunkChoice struct {
	Index        int           `json:"index"`
	Delta        sseChunkDelta `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
}

type sseChunkDelta struct {
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// readSSE reads SSE lines from the response body and sends StreamChunks.
func (p *openaiProvider) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamChunk) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamChunk{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()

		// SSE format: lines starting with "data: "
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// "[DONE]" signals end of stream
		if data == "[DONE]" {
			return
		}

		var chunk sseChunkResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- StreamChunk{Err: fmt.Errorf("provider_error: failed to parse SSE chunk: %w", err)}
			return
		}

		sc := StreamChunk{
			Index: 0,
			Usage: chunk.Usage,
		}
		if len(chunk.Choices) > 0 {
			sc.Index = chunk.Choices[0].Index
			sc.Delta = chunk.Choices[0].Delta.Content
			sc.ToolCalls = chunk.Choices[0].Delta.ToolCalls
			if chunk.Choices[0].FinishReason != nil {
				sc.FinishReason = *chunk.Choices[0].FinishReason
			}
		}

		ch <- sc
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamChunk{Err: fmt.Errorf("provider_error: stream read error: %w", err)}
	}
}

// mapHTTPError translates an HTTP error response to a ProviderError.
func (p *openaiProvider) mapHTTPError(resp *http.Response) error {
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096)) //nolint:errcheck // best-effort error body read
	msg := string(bodyBytes)
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}

	pe := &ProviderError{
		StatusCode: resp.StatusCode,
		Code:       "provider_error",
		Message:    msg,
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		pe.Code = "rate_limited"
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			pe.RetryAfter = ra
		}
	}

	return pe
}

// estimateTokensFromChat provides a rough token estimate when the API response
// does not include usage information. Delegates to the shared tokens package.
func estimateTokensFromChat(req ChatRequest) int {
	total := 0
	for _, msg := range req.Messages {
		total += tokens.EstimateTokens(MessageText(msg)) + 4
	}
	return total
}

// marshalChatRequest serializes a ChatRequest to JSON, merging any Extra fields
// into the top-level object. This allows injecting provider-specific fields like
// chat_template_kwargs without modifying the core ChatRequest struct.
func marshalChatRequest(req ChatRequest) ([]byte, error) {
	if len(req.Extra) == 0 {
		return json.Marshal(req)
	}

	// Marshal the base request, unmarshal into a map, merge extra, re-marshal.
	base, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range req.Extra {
		m[k] = v
	}
	return json.Marshal(m)
}

// retryAfterSeconds parses Retry-After as integer seconds. Returns 0 if unparseable.
func retryAfterSeconds(pe *ProviderError) int {
	if pe == nil || pe.RetryAfter == "" {
		return 0
	}
	n, err := strconv.Atoi(pe.RetryAfter)
	if err != nil {
		return 0
	}
	return n
}
