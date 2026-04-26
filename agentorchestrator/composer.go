package agentorchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/kbclient"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

const kbSearchTimeout = 2 * time.Second

// Composer applies per-intent composition to a ChatRequest.
type Composer struct {
	intentCfg *IntentConfig
	kbClient  *kbclient.KBClient // nil if KB not configured
}

// NewComposer creates a Composer. kbClient may be nil (KB features silently skipped).
func NewComposer(intentCfg *IntentConfig, kbClient *kbclient.KBClient) *Composer {
	return &Composer{intentCfg: intentCfg, kbClient: kbClient}
}

// ComposeResult holds the composed request and metadata for audit/metrics.
type ComposeResult struct {
	Request         modelpolicy.ChatRequest
	IntentName      IntentName
	KBResultCount   int
	KBLatency       time.Duration
	IsAgentDispatch bool
}

// Compose applies intent-specific modifications to a copy of the request.
func (c *Composer) Compose(ctx context.Context, decision RoutingDecision, req modelpolicy.ChatRequest, tenantID string) ComposeResult {
	intent := decision.Intent
	def := c.intentCfg.ResolvedIntent(intent)

	result := ComposeResult{
		Request:    copyRequest(req),
		IntentName: intent,
	}

	// Agent dispatch — don't modify request for model
	if def.Destination == "agent" {
		result.IsAgentDispatch = true
		return result
	}

	// Temperature override
	if def.Temperature != nil {
		result.Request.Temperature = def.Temperature
	}

	// Max tokens multiplier
	if def.MaxTokensMult != nil && *def.MaxTokensMult != 1.0 && result.Request.MaxTokens > 0 {
		result.Request.MaxTokens = int(float64(result.Request.MaxTokens) * *def.MaxTokensMult)
	}

	// Enable thinking
	if def.EnableThinking != nil && *def.EnableThinking {
		if result.Request.Extra == nil {
			result.Request.Extra = make(map[string]any)
		}
		result.Request.Extra["chat_template_kwargs"] = map[string]any{"enable_thinking": true}
	} else {
		if result.Request.Extra == nil {
			result.Request.Extra = make(map[string]any)
		}
		result.Request.Extra["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}

	// KB context injection
	if def.KBSearchEnabled != nil && *def.KBSearchEnabled && c.kbClient != nil {
		userPrompt := lastUserMsg(result.Request.Messages)
		if userPrompt != "" {
			limit := 5
			if def.KBSearchLimit != nil {
				limit = *def.KBSearchLimit
			}
			kbCtx, count, latency := c.searchKB(ctx, tenantID, userPrompt, limit)
			result.KBResultCount = count
			result.KBLatency = latency
			if kbCtx != "" {
				result.Request.Messages = prependSystemMessage(result.Request.Messages, kbCtx)
			}
		}
	}

	// System prompt injection
	if def.SystemPrompt != "" {
		result.Request.Messages = prependSystemMessage(result.Request.Messages, def.SystemPrompt)
	}

	return result
}

func (c *Composer) searchKB(ctx context.Context, tenantID, query string, limit int) (kbText string, count int, latency time.Duration) {
	kbCtx, cancel := context.WithTimeout(ctx, kbSearchTimeout)
	defer cancel()

	start := time.Now()
	results, err := c.kbClient.Search(kbCtx, tenantID, query, limit)
	latency = time.Since(start)

	if err != nil {
		slog.Warn("composer: KB search failed (non-fatal)", "error", err, "latency_ms", latency.Milliseconds())
		return "", 0, latency
	}

	if len(results) == 0 {
		return "", 0, latency
	}

	var sb strings.Builder
	sb.WriteString("[Knowledge Base Context]\n")
	for i, r := range results {
		snippet := strings.TrimSpace(r.Snippet)
		if snippet == "" {
			snippet = strings.TrimSpace(r.Content)
		}
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%d] %s\n%s\n\n", i+1, r.Source, snippet))
	}
	sb.WriteString("[End Knowledge Base Context]")

	return sb.String(), len(results), latency
}

func copyRequest(req modelpolicy.ChatRequest) modelpolicy.ChatRequest {
	cp := req
	cp.Messages = make([]modelpolicy.ChatMessage, len(req.Messages))
	copy(cp.Messages, req.Messages)
	if req.Extra != nil {
		cp.Extra = make(map[string]any, len(req.Extra))
		for k, v := range req.Extra {
			cp.Extra[k] = v
		}
	}
	return cp
}

func prependSystemMessage(msgs []modelpolicy.ChatMessage, content string) []modelpolicy.ChatMessage {
	sys := modelpolicy.ChatMessage{Role: "system", Content: content}
	return append([]modelpolicy.ChatMessage{sys}, msgs...)
}

func lastUserMsg(msgs []modelpolicy.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return modelpolicy.MessageText(msgs[i])
		}
	}
	return ""
}
