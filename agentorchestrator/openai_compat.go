package agentorchestrator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/id"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/multimodal"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

// chatMultimodalScope is the OIDC scope required to submit multipart
// (vision) content on the HTTP path. Callers authenticated without this
// scope get 403. Text-only callers are unaffected. v11.1.
//
// MCP-side parity note: MCP uses tier-based ClearanceExecute (T2) because
// MCP auth is clearance-tier-based. HTTP auth is OIDC/scope-based, so the
// HTTP path uses a scope name. Effect is the same (privileged callers
// only), encoded in each transport's native authz model.
const chatMultimodalScope = "chat:multimodal"

// containsScope reports whether the given scope list includes want.
func containsScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// chatAuditImageHash reflects the runtime config for whether audit log
// entries record image URLs verbatim or as SHA-256 hex. Defaults to
// false (URLs verbatim) for debuggability; operators set
// AGENTOS_CHAT_AUDIT_IMAGE_HASH=1 in privacy-strict deploys.
func chatAuditImageHash() bool {
	return strings.ToLower(strings.TrimSpace(
		os.Getenv("AGENTOS_CHAT_AUDIT_IMAGE_HASH"),
	)) == "1"
}

// inspectChatMessages walks an incoming ChatRequest's messages looking
// for multipart content (Content typed as `[]any` after JSON decode into
// an interface field). Returns:
//
//   - anyMultipart: true if any message carries multipart form content
//   - imageCount: total number of image_url parts across all messages
//   - imageRefs: audit-ready references (URL or SHA-256 per chatAuditImageHash)
//   - err: validation failure (unsupported scheme, empty URL, type missing,
//     cap exceeded, unknown part type). Callers should surface as 400.
//
// String-form content is skipped entirely — legacy callers pay no cost
// and cannot trip the validation.
func inspectChatMessages(messages []modelpolicy.ChatMessage, hashAudit bool) (anyMultipart bool, imageCount int, imageRefs []string, err error) {
	for i, msg := range messages {
		if _, ok := msg.Content.(string); ok {
			continue
		}
		if msg.Content == nil {
			continue
		}
		// Re-encode then decode into the strict Content union so we get
		// v11.0 validation semantics (type enum, URL scheme, cap, etc.).
		raw, mErr := json.Marshal(msg.Content)
		if mErr != nil {
			return false, 0, nil, fmt.Errorf("messages[%d]: cannot encode content: %w", i, mErr)
		}
		var c multimodal.Content
		if uErr := c.UnmarshalJSON(raw); uErr != nil {
			return false, 0, nil, fmt.Errorf("messages[%d]: %w", i, uErr)
		}
		if !c.IsMultipart() {
			// Round-trip decoded back to string — treat as text.
			continue
		}
		if vErr := multimodal.ValidateContent(c); vErr != nil {
			return false, 0, nil, fmt.Errorf("messages[%d]: %w", i, vErr)
		}
		anyMultipart = true
		for _, p := range c.Parts() {
			if p.Type != "image_url" || p.ImageURL == nil {
				continue
			}
			imageCount++
			imageRefs = append(imageRefs, multimodal.ImageRefForAudit(p.ImageURL.URL, hashAudit))
		}
	}
	return anyMultipart, imageCount, imageRefs, nil
}


// providerName returns the configured model provider name for metrics labels.
func providerName() string {
	cfg := config.LoadModelConfig()
	if cfg.Provider == "" {
		return "stub"
	}
	return cfg.Provider
}

// handleChatCompletions implements an OpenAI-compatible /v1/chat/completions
// endpoint. This allows tools like Open WebUI to use AgentOS as a drop-in
// OpenAI API backend, routing through the configured model provider (e.g. vLLM).
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	ac, _ := auth.Get(r.Context())
	tenantID := ac.TenantID
	if tenantID == "" {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "tenant_id required", httpx.CorrelationID(r), false)
		return
	}

	// Concurrency limit — reject with 429 if too many in-flight chat requests.
	if s.chatConcurrency != nil {
		select {
		case s.chatConcurrency <- struct{}{}:
			defer func() { <-s.chatConcurrency }()
		default:
			metrics.IncQuotaDenied("agent-orchestrator", "chat_concurrency")
			w.Header().Set("Retry-After", "5")
			httpx.Error(w, http.StatusTooManyRequests, "quota_exceeded",
				"chat concurrency limit reached", httpx.CorrelationID(r), true)
			return
		}
	}

	// Check token budget if configured.
	if s.tokenBudget != nil && !s.tokenBudget.Check(tenantID) {
		w.Header().Set("Retry-After", "60")
		httpx.Error(w, http.StatusTooManyRequests, "token_budget_exceeded", "hourly token budget exceeded", httpx.CorrelationID(r), true)
		return
	}

	var req modelpolicy.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}

	if req.Model == "" {
		req.Model = s.defaultModel
	}

	// v11.1: inspect for multipart content, validate, gate, meter.
	// `hasVision` gates on image presence (not multipart form), matching
	// v11.0 MCP behavior. Multipart-text-only messages route as text.
	_, imageCount, imageRefs, validateErr := inspectChatMessages(req.Messages, chatAuditImageHash())
	if validateErr != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_content", validateErr.Error(), httpx.CorrelationID(r), false)
		return
	}
	hasVision := imageCount > 0

	// Anthropic pre-guard: multipart is not yet translated to Anthropic
	// content blocks. Fail fast with a clear error rather than letting the
	// provider error or silently flatten to text.
	if hasVision && providerName() == "anthropic" {
		httpx.Error(w, http.StatusBadRequest, "unsupported_for_provider",
			"multipart content with images is not supported on the anthropic provider in v11.1",
			httpx.CorrelationID(r), false)
		return
	}

	// Scope gate for multimodal (v11.1 parity with v11.0 MCP's T2 gate).
	if hasVision && !containsScope(ac.Scopes, chatMultimodalScope) {
		httpx.Error(w, http.StatusForbidden, "scope_missing",
			fmt.Sprintf("multimodal content requires the %q scope", chatMultimodalScope),
			httpx.CorrelationID(r), false)
		return
	}

	// Content-type metric increments per call regardless of audit wiring.
	contentType := "text"
	if hasVision {
		contentType = "multimodal"
	}
	metrics.ObserveChatMessage(tenantID, contentType, imageCount)

	// Audit emission for multimodal only, when a logger is wired.
	if hasVision && s.audit != nil {
		s.audit.Log(audit.Entry{
			TenantID: tenantID,
			Action:   "chat.multimodal",
			Resource: "http:/v1/chat/completions",
			Outcome:  "accepted",
			Meta: map[string]any{
				"image_parts": imageCount,
				"image_refs":  imageRefs,
				"image_hash":  chatAuditImageHash(),
			},
		})
	}

	// Intent-based routing: classify, compose, and optionally dispatch.
	var routingDecision *RoutingDecision
	var composeResult *ComposeResult
	switch {
	case hasVision:
		// v11.1: Qwen3-4B classifier is text-only; stripped text can
		// misroute vision prompts. Synthesize a general-intent decision
		// and skip Classify (+ its metrics) entirely.
		d := RoutingDecision{
			Intent:         IntentGeneral,
			Destination:    "model",
			EnableThinking: false,
			Fallback:       false,
		}
		routingDecision = &d
		slog.Info("openai_compat: classifier bypass for multimodal", "tenant_id", tenantID, "intent", d.Intent)
	case s.routerCfg.Enabled:
		userPrompt := lastUserMessage(req.Messages)
		if userPrompt != "" {
			d := Classify(r.Context(), s.routerCfg, userPrompt)
			routingDecision = &d

			metrics.ObserveIntentClassification(string(d.Intent), d.Fallback)
			metrics.ObserveIntentClassifyDuration(string(d.Intent), d.Latency.Seconds())

			if s.composer != nil {
				cr := s.composer.Compose(r.Context(), d, req, tenantID)
				composeResult = &cr
				req = cr.Request

				if cr.KBResultCount > 0 {
					metrics.ObserveIntentKBSearch(string(cr.IntentName), cr.KBLatency.Seconds(), cr.KBResultCount)
				}

				if cr.IsAgentDispatch {
					s.handleAgentDispatch(w, r, tenantID, routingDecision)
					return
				}
			} else {
				// Legacy fallback: binary thinking toggle
				if d.Label == LabelReasoning {
					req.Extra = map[string]any{
						"chat_template_kwargs": map[string]any{"enable_thinking": true},
					}
				} else {
					req.Extra = map[string]any{
						"chat_template_kwargs": map[string]any{"enable_thinking": false},
					}
				}
			}
		}
	}

	if req.Stream {
		s.handleChatCompletionsStream(w, r, tenantID, req)
		return
	}

	// Non-streaming request.
	start := time.Now()
	resp, err := s.executor.provider.ChatComplete(r.Context(), req)
	elapsed := time.Since(start)

	if err != nil {
		metrics.ObserveModelCall(req.Model, providerName(), "error", elapsed.Seconds())
		httpx.Error(w, http.StatusBadGateway, "model_error", err.Error(), httpx.CorrelationID(r), true)
		return
	}

	metrics.ObserveModelCall(req.Model, providerName(), "ok", elapsed.Seconds())
	metrics.AddModelTokens(tenantID, req.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)

	// Record token usage in budget.
	if s.tokenBudget != nil {
		s.tokenBudget.Record(tenantID, resp.Usage.TotalTokens)
	}

	// Build OpenAI-format response.
	out := openaiChatResponse{
		ID:      "chatcmpl-" + id.New(""),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Usage:   resp.Usage,
	}
	for i, c := range resp.Choices {
		idx := c.Index
		if idx == 0 && i != 0 {
			idx = i
		}
		out.Choices = append(out.Choices, openaiChoice{
			Index:        idx,
			Message:      c.Message,
			FinishReason: c.FinishReason,
		})
	}

	// Log routing decision to audit.
	if routingDecision != nil && s.audit != nil {
		s.audit.Log(audit.Entry{
			TenantID:      tenantID,
			PrincipalID:   ac.PrincipalID,
			Action:        "chat.route",
			Resource:      "model/" + req.Model,
			Outcome:       "routed",
			CorrelationID: httpx.CorrelationID(r),
			RequestID:     r.Header.Get("X-Request-Id"),
			Meta: map[string]any{
				"classifier_label":   string(routingDecision.Label),
				"intent":             string(routingDecision.Intent),
				"destination":        routingDecision.Destination,
				"enable_thinking":    routingDecision.EnableThinking,
				"classifier_latency": routingDecision.Latency.Milliseconds(),
				"fallback":           routingDecision.Fallback,
				"kb_results":         composeResultKBCount(composeResult),
				"kb_latency_ms":      composeResultKBLatency(composeResult),
			},
		})
	}

	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleChatCompletionsStream(w http.ResponseWriter, r *http.Request, tenantID string, req modelpolicy.ChatRequest) {
	streamProvider, ok := s.executor.provider.(ModelStreamProvider)
	if !ok {
		// Provider doesn't support streaming; fall back to non-streaming wrapped in SSE.
		req.Stream = false
		start := time.Now()
		resp, err := s.executor.provider.ChatComplete(r.Context(), req)
		elapsed := time.Since(start)
		if err != nil {
			metrics.ObserveModelCall(req.Model, providerName(), "error", elapsed.Seconds())
			httpx.Error(w, http.StatusBadGateway, "model_error", err.Error(), httpx.CorrelationID(r), true)
			return
		}
		metrics.ObserveModelCall(req.Model, providerName(), "ok", elapsed.Seconds())
		metrics.AddModelTokens(tenantID, req.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		if s.tokenBudget != nil {
			s.tokenBudget.Record(tenantID, resp.Usage.TotalTokens)
		}
		// Emit single SSE chunk + [DONE].
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		bw := bufio.NewWriter(w)
		chunk := openaiStreamChunk{
			ID:      "chatcmpl-" + id.New(""),
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   resp.Model,
		}
		for i, c := range resp.Choices {
			idx := c.Index
			if idx == 0 && i != 0 {
				idx = i
			}
			chunk.Choices = append(chunk.Choices, openaiStreamChoice{
				Index: idx,
				Delta: openaiDelta{
					Role:    c.Message.Role,
					Content: modelpolicy.MessageText(c.Message),
				},
				FinishReason: strPtr(c.FinishReason),
			})
		}
		b, _ := json.Marshal(chunk)
		_, _ = bw.WriteString("data: " + string(b) + "\n\n")
		_, _ = bw.WriteString("data: [DONE]\n\n")
		_ = bw.Flush()
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// True streaming via provider.
	start := time.Now()
	ch, err := streamProvider.ChatCompleteStream(r.Context(), req)
	if err != nil {
		metrics.ObserveModelCall(req.Model, providerName(), "error", time.Since(start).Seconds())
		httpx.Error(w, http.StatusBadGateway, "model_error", err.Error(), httpx.CorrelationID(r), true)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	bw := bufio.NewWriter(w)
	completionID := "chatcmpl-" + id.New("")
	created := time.Now().Unix()
	firstChunk := true
	streamErr := false

	for sc := range ch {
		if sc.Err != nil {
			metrics.ObserveModelCall(req.Model, providerName(), "error", time.Since(start).Seconds())
			streamErr = true
			break
		}

		chunk := openaiStreamChunk{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
		}

		delta := openaiDelta{
			Content: sc.Delta,
		}
		if firstChunk {
			delta.Role = "assistant"
			firstChunk = false
		}

		choice := openaiStreamChoice{
			Index: sc.Index,
			Delta: delta,
		}
		if sc.FinishReason != "" {
			choice.FinishReason = strPtr(sc.FinishReason)
		}
		chunk.Choices = []openaiStreamChoice{choice}

		if sc.Usage != nil {
			chunk.Usage = sc.Usage
			metrics.AddModelTokens(tenantID, req.Model, sc.Usage.PromptTokens, sc.Usage.CompletionTokens)
			if s.tokenBudget != nil {
				s.tokenBudget.Record(tenantID, sc.Usage.TotalTokens)
			}
		}

		b, _ := json.Marshal(chunk)
		_, _ = bw.WriteString("data: " + string(b) + "\n\n")
		_ = bw.Flush()
		flusher.Flush()
	}

	if streamErr {
		_ = bw.Flush()
		flusher.Flush()
		return
	}

	metrics.ObserveModelCall(req.Model, providerName(), "ok", time.Since(start).Seconds())
	_, _ = bw.WriteString("data: [DONE]\n\n")
	_ = bw.Flush()
	flusher.Flush()
}

// handleModels implements GET /v1/models in OpenAI format.
// It proxies to the upstream model provider (e.g. vLLM) so the model list
// always reflects what is actually loaded, without requiring a redeploy.
// Falls back to the static defaultModel if the provider is unreachable.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	// Try to proxy from the upstream provider.
	if s.modelBaseURL != "" {
		proxyURL := strings.TrimRight(s.modelBaseURL, "/") + "/models"
		ctx, cancel := contextWithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		proxyReq, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL, nil)
		if err == nil {
			cfg := config.LoadModelConfig()
			if cfg.APIKey != "" {
				proxyReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}
			resp, err := http.DefaultClient.Do(proxyReq)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					io.Copy(w, resp.Body)
					return
				}
			}
		}
	}

	// Fallback: return the static default model.
	models := []openaiModel{
		{
			ID:      s.defaultModel,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "agentos",
		},
	}

	httpx.JSON(w, http.StatusOK, openaiModelList{
		Object: "list",
		Data:   models,
	})
}

// OpenAI-compatible response types.

type openaiChatResponse struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []openaiChoice      `json:"choices"`
	Usage   modelpolicy.ChatUsage `json:"usage"`
}

type openaiChoice struct {
	Index        int                    `json:"index"`
	Message      modelpolicy.ChatMessage `json:"message"`
	FinishReason string                 `json:"finish_reason"`
}

type openaiStreamChunk struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []openaiStreamChoice   `json:"choices"`
	Usage   *modelpolicy.ChatUsage `json:"usage,omitempty"`
}

type openaiStreamChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type openaiDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openaiModelList struct {
	Object string        `json:"object"`
	Data   []openaiModel `json:"data"`
}

type openaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func strPtr(s string) *string {
	return &s
}

// handleModelByID implements GET /v1/models/{model_id} in OpenAI format.
// Proxies to the upstream provider, falling back to matching against defaultModel.
func (s *Server) handleModelByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	modelID := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	modelID = strings.Trim(modelID, "/")
	if modelID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "model_id is required", httpx.CorrelationID(r), false)
		return
	}

	// Try to proxy from the upstream provider.
	if s.modelBaseURL != "" {
		proxyURL := strings.TrimRight(s.modelBaseURL, "/") + "/models/" + modelID
		ctx, cancel := contextWithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		proxyReq, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL, nil)
		if err == nil {
			cfg := config.LoadModelConfig()
			if cfg.APIKey != "" {
				proxyReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}
			resp, err := http.DefaultClient.Do(proxyReq)
			if err == nil {
				defer resp.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				io.Copy(w, resp.Body)
				return
			}
		}
	}

	// Fallback: match against static default.
	if modelID != s.defaultModel {
		httpx.Error(w, http.StatusNotFound, "model_not_found", fmt.Sprintf("model %q not found", modelID), httpx.CorrelationID(r), false)
		return
	}

	model := openaiModel{
		ID:      modelID,
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "agentos",
	}
	httpx.JSON(w, http.StatusOK, model)
}

// handleEmbeddings proxies POST /v1/embeddings to the configured model provider.
func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	cfg := config.LoadModelConfig()
	if cfg.BaseURL == "" {
		httpx.Error(w, http.StatusNotImplemented, "not_configured", "model provider base URL not configured for embeddings", httpx.CorrelationID(r), false)
		return
	}

	// Read the request body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "read_error", "failed to read request body", httpx.CorrelationID(r), false)
		return
	}

	// Proxy to provider.
	proxyURL := strings.TrimRight(cfg.BaseURL, "/") + "/v1/embeddings"
	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, proxyURL, bytes.NewReader(body))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "proxy_error", "failed to create proxy request", httpx.CorrelationID(r), true)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		proxyReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	client := &http.Client{Timeout: cfg.Timeout}
	resp, err := client.Do(proxyReq)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "provider_error", "embeddings provider unreachable: "+err.Error(), httpx.CorrelationID(r), true)
		return
	}
	defer resp.Body.Close()

	// Forward the response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// contextWithTimeout derives a context with the given timeout, respecting
// any earlier deadline already set on the parent.
func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// lastUserMessage extracts the content of the last "user" role message from
// the request. For multipart content, returns the text-only view (classifier
// cannot reason about images; v11.1 handler bypasses the classifier when the
// content contains image parts anyway).
func lastUserMessage(messages []modelpolicy.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			if t := modelpolicy.MessageText(messages[i]); t != "" {
				return t
			}
		}
	}
	return ""
}

func composeResultKBCount(cr *ComposeResult) int {
	if cr == nil {
		return 0
	}
	return cr.KBResultCount
}

func composeResultKBLatency(cr *ComposeResult) int64 {
	if cr == nil {
		return 0
	}
	return cr.KBLatency.Milliseconds()
}

// handleAgentDispatch returns a structured response for agent-class requests.
// Full agent loop integration deferred to v3.2.
func (s *Server) handleAgentDispatch(w http.ResponseWriter, r *http.Request, tenantID string, decision *RoutingDecision) {
	slog.Info("routing: agent dispatch",
		"intent", decision.Intent,
		"latency_ms", decision.Latency.Milliseconds(),
	)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"object":  "agent.dispatch",
		"intent":  string(decision.Intent),
		"status":  "recognized",
		"message": "This request has been classified as requiring agent orchestration. Agent dispatch will be available in v3.2.",
	})
}
