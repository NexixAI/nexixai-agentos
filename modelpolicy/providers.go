package modelpolicy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/tokens"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

type provider interface {
	// Invoke sends a non-streaming request and returns output + usage.
	// Retained for backward compatibility with stub provider.
	Invoke(req types.ModelInvokeRequest) (map[string]any, map[string]any, error)

	// ChatComplete sends a chat completions request (OpenAI format).
	ChatComplete(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// ChatCompleteStream sends a streaming chat completions request.
	// Tokens are delivered via the returned channel. Channel closes on completion.
	ChatCompleteStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
}

// NewProviderFromConfig creates a provider from environment configuration.
// Returns the provider if AGENTOS_MODEL_PROVIDER is set and valid.
func NewProviderFromConfig() (provider, error) {
	cfg := config.LoadModelConfig()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return newProvider(cfg)
}

// newProvider creates a provider based on configuration.
// Non-stub providers are wrapped with resilience (retry + circuit breaker).
func newProvider(cfg config.ModelConfig) (provider, error) {
	var p provider
	switch cfg.Provider {
	case "openai":
		p = newOpenAIProvider(cfg)
	case "anthropic":
		p = newAnthropicProvider(cfg)
	case "stub":
		return &stubProvider{}, nil
	case "":
		return nil, fmt.Errorf("AGENTOS_MODEL_PROVIDER is required (valid: openai, anthropic, stub)")
	default:
		return nil, fmt.Errorf("unknown model provider %q (valid: openai, anthropic, stub)", cfg.Provider)
	}
	return NewResilientProvider(p, DefaultRetryConfig(), DefaultCircuitBreakerConfig()), nil
}

type providerEntry struct {
	model    types.Model
	impl     provider
	defaults bool
}

type registry struct {
	mu    sync.RWMutex
	model map[string]providerEntry
}

func newRegistry() *registry {
	r := &registry{
		model: make(map[string]providerEntry),
	}
	r.register(types.Model{
		ModelID:      "local-stub-llm",
		Provider:     "stub",
		DisplayName:  "Local Stub LLM",
		Capabilities: map[string]any{"chat": true},
	}, &stubProvider{}, true)
	return r
}

func (r *registry) register(model types.Model, impl provider, defaults bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.model[model.ModelID] = providerEntry{model: model, impl: impl, defaults: defaults}
}

func (r *registry) Models() []types.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]types.Model, 0, len(r.model))
	for _, entry := range r.model {
		out = append(out, entry.model)
	}
	return out
}

func (r *registry) Resolve(modelID string) (provider, types.Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.model[modelID]; ok {
		return entry.impl, entry.model, true
	}
	// fall back to default provider if requested model missing
	for _, entry := range r.model {
		if entry.defaults {
			return entry.impl, entry.model, true
		}
	}
	return nil, types.Model{}, false
}

type stubProvider struct{}

func (*stubProvider) Invoke(req types.ModelInvokeRequest) (map[string]any, map[string]any, error) {
	text := "stub response"
	if inputText, ok := req.Input["text"].(string); ok && strings.TrimSpace(inputText) != "" {
		text = fmt.Sprintf("stub: %s", inputText)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	output := map[string]any{
		"type": "text",
		"text": text,
		"echo": req.Input,
		"ts":   now,
	}

	usage := map[string]any{
		"prompt_tokens":     tokenEstimateFromInput(req.Input),
		"completion_tokens": 32,
		"total_tokens":      tokenEstimateFromInput(req.Input) + 32,
		"model_id":          req.ModelID,
		"provider":          "stub",
		"timestamp":         now,
	}

	return output, usage, nil
}

// ChatComplete implements the chat completion interface for the stub provider.
// It returns a mock response with estimated token usage.
func (*stubProvider) ChatComplete(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	text := "stub response"
	if len(req.Messages) > 0 {
		last := req.Messages[len(req.Messages)-1]
		if lastText := MessageText(last); strings.TrimSpace(lastText) != "" {
			text = fmt.Sprintf("stub: %s", lastText)
		}
	}

	promptTokens := estimateTokensFromChatReq(req)
	completionTokens := 32

	return &ChatResponse{
		Model: req.Model,
		Choices: []ChatChoice{
			{
				Message:      ChatMessage{Role: "assistant", Content: text},
				FinishReason: "stop",
			},
		},
		Usage: ChatUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}, nil
}

// ChatCompleteStream implements the streaming chat completion interface for the stub provider.
// It returns a single chunk with the complete response and closes the channel.
func (*stubProvider) ChatCompleteStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	resp, err := (&stubProvider{}).ChatComplete(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 2)
	go func() {
		defer close(ch)
		if len(resp.Choices) > 0 {
			ch <- StreamChunk{
				Delta:        MessageText(resp.Choices[0].Message),
				FinishReason: resp.Choices[0].FinishReason,
				Usage:        &resp.Usage,
			}
		}
	}()
	return ch, nil
}

// tokenEstimateFromInput estimates tokens for a structured input map.
func tokenEstimateFromInput(input map[string]any) int {
	if input == nil {
		return 8
	}
	if txt, ok := input["text"].(string); ok {
		n := tokens.EstimateTokens(txt)
		if n == 0 {
			return 8
		}
		return n
	}
	return 12
}

// estimateTokensFromChatReq estimates total prompt tokens for a ChatRequest.
func estimateTokensFromChatReq(req ChatRequest) int {
	total := 0
	for _, msg := range req.Messages {
		total += tokens.EstimateTokens(MessageText(msg)) + 4
	}
	return total
}
