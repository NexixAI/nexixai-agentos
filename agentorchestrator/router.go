package agentorchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// RouterConfig holds classifier-based routing configuration.
// Routing does not send requests to a different endpoint — it toggles
// enable_thinking on the same model via chat_template_kwargs.
type RouterConfig struct {
	Enabled       bool
	ClassifierURL string
}

// LoadRouterConfig reads routing env vars. Defaults are safe for a disabled state.
func LoadRouterConfig() RouterConfig {
	enabled := true
	if v := os.Getenv("ROUTER_ENABLED"); v != "" {
		enabled = strings.EqualFold(v, "true") || v == "1"
	}
	return RouterConfig{
		Enabled:       enabled,
		ClassifierURL: envOr("AGENTOS_CLASSIFIER_URL", "http://localhost:8002/v1/chat/completions"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ClassifierLabel is the routing decision from the classifier.
type ClassifierLabel string

const (
	LabelCode      ClassifierLabel = "code"
	LabelReasoning ClassifierLabel = "reasoning"
	LabelOps       ClassifierLabel = "ops"
	LabelSearch    ClassifierLabel = "search"
	LabelAgent     ClassifierLabel = "agent"
	LabelGeneral   ClassifierLabel = "general"
)

// classifierRequest is the OpenAI-compatible chat completion request sent to the classifier.
type classifierRequest struct {
	Model                string              `json:"model"`
	Messages             []classifierMessage `json:"messages"`
	Stream               bool                `json:"stream"`
	MaxTokens            int                 `json:"max_tokens,omitempty"`
	ChatTemplateKwargs   map[string]any      `json:"chat_template_kwargs,omitempty"`
}

type classifierMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type classifierResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	// Ollama format
	Message *struct {
		Content string `json:"content"`
	} `json:"message,omitempty"`
}

// RoutingDecision captures the outcome of a classifier-based routing call.
type RoutingDecision struct {
	Label           ClassifierLabel `json:"label"`
	Intent          IntentName      `json:"intent"`
	Destination     string          `json:"destination"`
	EnableThinking  bool            `json:"enable_thinking"`
	Latency         time.Duration   `json:"latency_ms"`
	Fallback        bool            `json:"fallback"`
	Error           string          `json:"error,omitempty"`
}

const (
	classifierTimeout    = 500 * time.Millisecond
	classifierModel      = "Qwen/Qwen3-4B-AWQ"
	classifierMaxTokens  = 8
	classifierSystemPrompt = `Classify the user's intent into exactly one category. Answer with the category name only.

Categories:
- code: Writing, reviewing, debugging, or explaining code and programs
- reasoning: Mathematical proofs, formal logic, equation solving, step-by-step derivations
- ops: Infrastructure, deployment, configuration, monitoring, DevOps tasks
- search: Looking up facts, documentation, references, or knowledge base queries
- agent: Tasks requiring multi-step autonomous execution or tool orchestration
- general: Casual conversation, simple Q&A, creative writing, anything else

Examples:
"Write a Python sort function" → code
"Fix the null pointer in auth.go" → code
"Prove sqrt(2) is irrational" → reasoning
"Solve 3x+7=22" → reasoning
"Why is GPU temp high on node2" → ops
"How do I configure the nginx proxy" → ops
"What port does AgentOS run on" → search
"Show me the SRE agent config" → search
"Restart the inference service" → agent
"Analyze this repo and create a migration plan" → agent
"What is the capital of France" → general
"Explain Docker networking" → general

Answer with the category name only.`
)

// Classify sends the user prompt to the classifier and returns a routing decision.
// On any error or timeout, returns LabelGeneral with Fallback=true.
func Classify(ctx context.Context, cfg RouterConfig, userPrompt string) RoutingDecision {
	if !cfg.Enabled || cfg.ClassifierURL == "" {
		return RoutingDecision{
			Label:          LabelGeneral,
			Intent:         IntentGeneral,
			Destination:    "general",
			EnableThinking: false,
			Fallback:       true,
			Error:          "router disabled",
		}
	}

	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, classifierTimeout)
	defer cancel()

	body := classifierRequest{
		Model: classifierModel,
		Messages: []classifierMessage{
			{Role: "system", Content: classifierSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:    false,
		MaxTokens: classifierMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fallbackDecision(start, fmt.Sprintf("marshal error: %v", err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ClassifierURL, strings.NewReader(string(payload)))
	if err != nil {
		return fallbackDecision(start, fmt.Sprintf("request error: %v", err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallbackDecision(start, fmt.Sprintf("classifier unreachable: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallbackDecision(start, fmt.Sprintf("classifier returned %d", resp.StatusCode))
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fallbackDecision(start, fmt.Sprintf("read error: %v", err))
	}

	var cr classifierResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return fallbackDecision(start, fmt.Sprintf("json decode error: %v", err))
	}

	// Extract content — handle both OpenAI and Ollama response formats.
	var content string
	if len(cr.Choices) > 0 {
		content = cr.Choices[0].Message.Content
	} else if cr.Message != nil {
		content = cr.Message.Content
	}

	label := parseLabel(strings.TrimSpace(strings.ToLower(content)))
	elapsed := time.Since(start)

	intent := labelToIntent(label)
	thinking := label == LabelReasoning

	return RoutingDecision{
		Label:          label,
		Intent:         intent,
		Destination:    string(intent),
		EnableThinking: thinking,
		Latency:        elapsed,
		Fallback:       false,
	}
}

func parseLabel(s string) ClassifierLabel {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "code":
		return LabelCode
	case "reasoning", "r":
		return LabelReasoning
	case "ops":
		return LabelOps
	case "search":
		return LabelSearch
	case "agent":
		return LabelAgent
	case "general", "g":
		return LabelGeneral
	default:
		return LabelGeneral
	}
}

func labelToIntent(l ClassifierLabel) IntentName {
	switch l {
	case LabelCode:
		return IntentCode
	case LabelReasoning:
		return IntentReasoning
	case LabelOps:
		return IntentOps
	case LabelSearch:
		return IntentSearch
	case LabelAgent:
		return IntentAgent
	default:
		return IntentGeneral
	}
}

func fallbackDecision(start time.Time, reason string) RoutingDecision {
	slog.Warn("classifier fallback", "reason", reason, "latency_ms", time.Since(start).Milliseconds())
	return RoutingDecision{
		Label:          LabelGeneral,
		Intent:         IntentGeneral,
		Destination:    "general",
		EnableThinking: false,
		Latency:        time.Since(start),
		Fallback:       true,
		Error:          reason,
	}
}
