package modelpolicy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// anthropicProvider implements the provider interface for the Anthropic Messages API.
type anthropicProvider struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	timeout      time.Duration
	defaultModel string
}

// newAnthropicProvider creates a new Anthropic provider from configuration.
func newAnthropicProvider(cfg config.ModelConfig) *anthropicProvider {
	defaultModel := cfg.DefaultModel
	if defaultModel == "" {
		defaultModel = "claude-sonnet-4-20250514"
	}
	return &anthropicProvider{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:       cfg.APIKey,
		httpClient:   &http.Client{Timeout: cfg.Timeout},
		timeout:      cfg.Timeout,
		defaultModel: defaultModel,
	}
}

// Invoke implements the legacy provider interface.
func (p *anthropicProvider) Invoke(req types.ModelInvokeRequest) (map[string]any, map[string]any, error) {
	inputText, _ := req.Input["text"].(string)
	chatReq := ChatRequest{
		Model:    req.ModelID,
		Messages: []ChatMessage{{Role: "user", Content: inputText}},
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
	return map[string]any{"type": "text", "text": text, "ts": now},
		map[string]any{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
			"model_id":          req.ModelID,
			"provider":          "anthropic",
			"timestamp":         now,
		}, nil
}

// --- Anthropic API types ---

type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type      string `json:"type"`                 // "text", "tool_use", "tool_result"
	Text      string `json:"text,omitempty"`        // for type="text"
	ID        string `json:"id,omitempty"`          // for type="tool_use"
	Name      string `json:"name,omitempty"`        // for type="tool_use"
	Input     any    `json:"input,omitempty"`       // for type="tool_use"
	ToolUseID string `json:"tool_use_id,omitempty"` // for type="tool_result"
	Content   string `json:"content,omitempty"`     // for type="tool_result" (overloaded with Text)
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ErrAnthropicMultimodalUnsupported is returned when a request with
// multipart content (e.g., image_url parts) is routed to the Anthropic
// provider. v11.1 only enables multipart on the OpenAI-compatible path;
// translating OpenAI content parts into Anthropic image blocks is
// deferred to a future PRS. Callers should gate on this at the HTTP
// layer and return a structured 400 before dispatch.
var ErrAnthropicMultimodalUnsupported = fmt.Errorf("anthropic provider does not support multipart content in v11.1")

// hasMultipart reports whether any message in the request carries
// non-string Content (i.e., the OpenAI multipart array form).
func hasMultipart(req ChatRequest) bool {
	for _, m := range req.Messages {
		if _, ok := m.Content.(string); !ok && m.Content != nil {
			return true
		}
	}
	return false
}

// ChatComplete sends a non-streaming request to the Anthropic Messages API.
func (p *anthropicProvider) ChatComplete(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if hasMultipart(req) {
		return nil, ErrAnthropicMultimodalUnsupported
	}
	anthReq := p.buildRequest(req)
	anthReq.Stream = false

	body, err := json.Marshal(anthReq)
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to create request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider_error: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, p.mapHTTPError(httpResp)
	}

	var anthResp anthropicResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&anthResp); err != nil {
		return nil, fmt.Errorf("provider_error: failed to decode response: %w", err)
	}

	return p.mapResponse(anthResp), nil
}

// ChatCompleteStream sends a streaming request to the Anthropic Messages API.
func (p *anthropicProvider) ChatCompleteStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if hasMultipart(req) {
		return nil, ErrAnthropicMultimodalUnsupported
	}
	anthReq := p.buildRequest(req)
	anthReq.Stream = true

	body, err := json.Marshal(anthReq)
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("provider_error: failed to create request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider_error: request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		return nil, p.mapHTTPError(httpResp)
	}

	ch := make(chan StreamChunk, 16)
	go p.readAnthropicSSE(ctx, httpResp.Body, ch)
	return ch, nil
}

func (p *anthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
}

func (p *anthropicProvider) buildRequest(req ChatRequest) anthropicRequest {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	anthReq := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
	}

	// Extract system message and convert messages.
	for _, msg := range req.Messages {
		// v11.1: Anthropic provider does not yet support multipart content.
		// Flatten via MessageText (text-only view) for now. If multipart is
		// present, the HTTP handler is expected to reject at a higher layer
		// via the anthropic-provider guard before reaching here.
		msgText := MessageText(msg)
		if msg.Role == "system" {
			anthReq.System = msgText
			continue
		}

		if msg.Role == "tool" {
			// Tool result messages → content blocks with type "tool_result"
			block := anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msgText,
			}
			anthReq.Messages = append(anthReq.Messages, anthropicMessage{
				Role:    "user",
				Content: []anthropicContentBlock{block},
			})
			continue
		}

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Assistant message with tool calls → content blocks
			var blocks []anthropicContentBlock
			if msgText != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: msgText})
			}
			for _, tc := range msg.ToolCalls {
				var input any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input) //nolint:errcheck // best-effort tool arg parse
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			anthReq.Messages = append(anthReq.Messages, anthropicMessage{
				Role:    "assistant",
				Content: blocks,
			})
			continue
		}

		anthReq.Messages = append(anthReq.Messages, anthropicMessage{
			Role:    msg.Role,
			Content: msgText,
		})
	}

	// Convert tool definitions.
	for _, t := range req.Tools {
		anthReq.Tools = append(anthReq.Tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	return anthReq
}

func (p *anthropicProvider) mapResponse(resp anthropicResponse) *ChatResponse {
	var content string
	var toolCalls []ToolCall
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content += block.Text
		case "tool_use":
			argsJSON, err := json.Marshal(block.Input)
			if err != nil {
				argsJSON = []byte("{}")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	finishReason := "stop"
	if resp.StopReason == "tool_use" {
		finishReason = "tool_calls"
	}

	return &ChatResponse{
		Model: resp.ID,
		Choices: []ChatChoice{{
			Message: ChatMessage{
				Role:      "assistant",
				Content:   content,
				ToolCalls: toolCalls,
			},
			FinishReason: finishReason,
		}},
		Usage: ChatUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

// --- Anthropic SSE event types ---

type anthropicSSEEvent struct {
	Type string `json:"type"`
}

type anthropicContentBlockDelta struct {
	Type  string                    `json:"type"`
	Index int                       `json:"index"`
	Delta anthropicContentDeltaData `json:"delta"`
}

type anthropicContentDeltaData struct {
	Type        string `json:"type"` // "text_delta" or "input_json_delta"
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicContentBlockStart struct {
	Type         string                `json:"type"`
	Index        int                   `json:"index"`
	ContentBlock anthropicContentBlock `json:"content_block"`
}

type anthropicMessageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

func (p *anthropicProvider) readAnthropicSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamChunk) {
	defer close(ch)
	defer body.Close()

	// Track tool_use blocks being built.
	type pendingToolUse struct {
		id          string
		name        string
		argsBuilder strings.Builder
	}
	var currentToolUses []pendingToolUse

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamChunk{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		// Determine event type.
		var evt anthropicSSEEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}

		switch evt.Type {
		case "content_block_start":
			var start anthropicContentBlockStart
			if err := json.Unmarshal([]byte(data), &start); err == nil && start.ContentBlock.Type == "tool_use" {
				currentToolUses = append(currentToolUses, pendingToolUse{
					id:   start.ContentBlock.ID,
					name: start.ContentBlock.Name,
				})
			}

		case "content_block_delta":
			var delta anthropicContentBlockDelta
			if err := json.Unmarshal([]byte(data), &delta); err != nil {
				continue
			}
			switch delta.Delta.Type {
			case "text_delta":
				ch <- StreamChunk{Delta: delta.Delta.Text}
			case "input_json_delta":
				// Accumulate tool call JSON.
				if len(currentToolUses) > 0 {
					currentToolUses[len(currentToolUses)-1].argsBuilder.WriteString(delta.Delta.PartialJSON)
				}
			}

		case "message_delta":
			var msgDelta anthropicMessageDelta
			if err := json.Unmarshal([]byte(data), &msgDelta); err != nil {
				continue
			}

			sc := StreamChunk{}
			if msgDelta.Delta.StopReason == "end_turn" {
				sc.FinishReason = "stop"
			} else if msgDelta.Delta.StopReason == "tool_use" {
				sc.FinishReason = "tool_calls"
				// Emit accumulated tool calls.
				for _, tu := range currentToolUses {
					sc.ToolCalls = append(sc.ToolCalls, ToolCall{
						ID:   tu.id,
						Type: "function",
						Function: FunctionCall{
							Name:      tu.name,
							Arguments: tu.argsBuilder.String(),
						},
					})
				}
				currentToolUses = nil
			}
			if msgDelta.Usage != nil {
				sc.Usage = &ChatUsage{
					PromptTokens:     msgDelta.Usage.InputTokens,
					CompletionTokens: msgDelta.Usage.OutputTokens,
					TotalTokens:      msgDelta.Usage.InputTokens + msgDelta.Usage.OutputTokens,
				}
			}
			ch <- sc

		case "message_stop":
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamChunk{Err: fmt.Errorf("provider_error: stream read error: %w", err)}
	}
}

func (p *anthropicProvider) mapHTTPError(resp *http.Response) error {
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

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		pe.Code = "rate_limited"
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			pe.RetryAfter = ra
		}
	case resp.StatusCode == http.StatusBadRequest:
		pe.Code = "invalid_request"
	case resp.StatusCode >= 500:
		pe.Code = "provider_error"
	}

	return pe
}
