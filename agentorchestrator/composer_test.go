package agentorchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/kbclient"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

func testIntentConfig() *IntentConfig {
	temp03 := 0.3
	temp02 := 0.2
	temp04 := 0.4
	mult2 := 2.0
	mult3 := 3.0
	mult15 := 1.5
	mult1 := 1.0
	trueVal := true
	falseVal := false
	kbLimit5 := 5
	kbLimit10 := 10
	kbLimit15 := 15

	return &IntentConfig{
		Version: "3.1",
		Defaults: IntentDefaults{
			MaxTokensMult:   1.0,
			EnableThinking:  false,
			KBSearchEnabled: false,
			KBSearchLimit:   5,
		},
		Intents: map[IntentName]IntentDef{
			IntentCode: {
				SystemPrompt:    "You are a senior software engineer.",
				Temperature:     &temp03,
				MaxTokensMult:   &mult2,
				KBSearchEnabled: &trueVal,
				KBSearchLimit:   &kbLimit5,
				Destination:     "model",
			},
			IntentReasoning: {
				EnableThinking: &trueVal,
				MaxTokensMult:  &mult3,
				Destination:    "model",
			},
			IntentOps: {
				SystemPrompt:    "You are an infrastructure specialist.",
				Temperature:     &temp02,
				MaxTokensMult:   &mult15,
				KBSearchEnabled: &trueVal,
				KBSearchLimit:   &kbLimit10,
				Destination:     "model",
			},
			IntentSearch: {
				SystemPrompt:    "Synthesize from KB context.",
				Temperature:     &temp04,
				KBSearchEnabled: &trueVal,
				KBSearchLimit:   &kbLimit15,
				Destination:     "model",
			},
			IntentAgent: {
				Destination: "agent",
			},
			IntentGeneral: {
				MaxTokensMult:   &mult1,
				KBSearchEnabled: &falseVal,
				Destination:     "model",
			},
		},
	}
}

func baseRequest() modelpolicy.ChatRequest {
	return modelpolicy.ChatRequest{
		Model: "test-model",
		Messages: []modelpolicy.ChatMessage{
			{Role: "user", Content: "Hello world"},
		},
		MaxTokens: 1000,
	}
}

func makeDecision(intent IntentName) RoutingDecision {
	return RoutingDecision{
		Label:  ClassifierLabel(intent),
		Intent: intent,
	}
}

func TestCompose_GeneralIntent(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	result := c.Compose(context.Background(), makeDecision(IntentGeneral), baseRequest(), "tnt_test")

	if result.IntentName != IntentGeneral {
		t.Errorf("expected general intent, got %q", result.IntentName)
	}
	if result.IsAgentDispatch {
		t.Error("general should not be agent dispatch")
	}
	// Should not add system messages (no system prompt, no KB)
	if len(result.Request.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Request.Messages))
	}
}

func TestCompose_CodeIntent_SystemPrompt(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	result := c.Compose(context.Background(), makeDecision(IntentCode), baseRequest(), "tnt_test")

	if len(result.Request.Messages) < 2 {
		t.Fatalf("expected system prompt prepended, got %d messages", len(result.Request.Messages))
	}
	if result.Request.Messages[0].Role != "system" {
		t.Errorf("expected system role, got %q", result.Request.Messages[0].Role)
	}
	if !strings.Contains(modelpolicy.MessageText(result.Request.Messages[0]), "senior software engineer") {
		t.Error("expected code system prompt content")
	}
}

func TestCompose_CodeIntent_Temperature(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	result := c.Compose(context.Background(), makeDecision(IntentCode), baseRequest(), "tnt_test")

	if result.Request.Temperature == nil || *result.Request.Temperature != 0.3 {
		t.Error("expected temperature 0.3 for code intent")
	}
}

func TestCompose_CodeIntent_MaxTokensMultiplier(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	result := c.Compose(context.Background(), makeDecision(IntentCode), baseRequest(), "tnt_test")

	if result.Request.MaxTokens != 2000 {
		t.Errorf("expected max_tokens 2000 (1000 * 2.0), got %d", result.Request.MaxTokens)
	}
}

func TestCompose_ReasoningIntent_Thinking(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	result := c.Compose(context.Background(), makeDecision(IntentReasoning), baseRequest(), "tnt_test")

	extra := result.Request.Extra
	if extra == nil {
		t.Fatal("expected Extra to be set")
	}
	kwargs, ok := extra["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatal("expected chat_template_kwargs")
	}
	if kwargs["enable_thinking"] != true {
		t.Error("expected enable_thinking=true for reasoning")
	}
	if result.Request.MaxTokens != 3000 {
		t.Errorf("expected max_tokens 3000 (1000 * 3.0), got %d", result.Request.MaxTokens)
	}
}

func TestCompose_AgentIntent_Dispatch(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	result := c.Compose(context.Background(), makeDecision(IntentAgent), baseRequest(), "tnt_test")

	if !result.IsAgentDispatch {
		t.Error("expected IsAgentDispatch=true for agent intent")
	}
}

func TestCompose_OpsIntent_WithKB(t *testing.T) {
	// Mock KB server — returns raw array of searchResultWire objects
	kbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"ID": "1", "Repo": "nexixai-infra", "Path": "docs/cluster.md", "Title": "Cluster", "Snippet": "GPU nodes run on 192.168.50.x", "Score": 0.9},
			{"ID": "2", "Repo": "nexixai-infra", "Path": "docs/slos.md", "Title": "SLOs", "Snippet": "Inference latency p99 < 5s", "Score": 0.8},
		})
	}))
	defer kbSrv.Close()

	kbClient := kbclient.NewKBClient(kbSrv.URL, "test-key")
	c := NewComposer(testIntentConfig(), kbClient)
	result := c.Compose(context.Background(), makeDecision(IntentOps), baseRequest(), "tnt_test")

	if result.KBResultCount != 2 {
		t.Errorf("expected 2 KB results, got %d", result.KBResultCount)
	}
	if result.KBLatency == 0 {
		t.Error("expected non-zero KB latency")
	}

	// Should have system prompt + KB context + original user message
	if len(result.Request.Messages) < 3 {
		t.Fatalf("expected >= 3 messages (system + KB + user), got %d", len(result.Request.Messages))
	}
	// First message should be the intent system prompt (prepended last)
	if !strings.Contains(modelpolicy.MessageText(result.Request.Messages[0]), "infrastructure specialist") {
		t.Error("expected ops system prompt as first message")
	}
	// Second should be KB context (KB is prepended before system prompt)
	kbText := modelpolicy.MessageText(result.Request.Messages[1])
	if !strings.Contains(kbText, "Knowledge Base Context") {
		preview := kbText
		if len(preview) > 80 {
			preview = preview[:80]
		}
		t.Errorf("expected KB context as second message, got: %s", preview)
	}
}

func TestCompose_KBFailure_Graceful(t *testing.T) {
	// KB server that returns error
	kbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer kbSrv.Close()

	kbClient := kbclient.NewKBClient(kbSrv.URL, "test-key")
	c := NewComposer(testIntentConfig(), kbClient)
	result := c.Compose(context.Background(), makeDecision(IntentOps), baseRequest(), "tnt_test")

	// Should still succeed, just without KB context
	if result.KBResultCount != 0 {
		t.Errorf("expected 0 KB results on failure, got %d", result.KBResultCount)
	}
	// Should still have system prompt
	if len(result.Request.Messages) < 2 {
		t.Error("expected at least system prompt + user message")
	}
}

func TestCompose_NilKBClient(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	result := c.Compose(context.Background(), makeDecision(IntentOps), baseRequest(), "tnt_test")

	// No KB injection when client is nil
	if result.KBResultCount != 0 {
		t.Errorf("expected 0 KB results with nil client, got %d", result.KBResultCount)
	}
}

func TestCompose_DoesNotMutateOriginal(t *testing.T) {
	c := NewComposer(testIntentConfig(), nil)
	orig := baseRequest()
	origLen := len(orig.Messages)

	_ = c.Compose(context.Background(), makeDecision(IntentCode), orig, "tnt_test")

	if len(orig.Messages) != origLen {
		t.Error("Compose mutated original request messages")
	}
	if orig.Temperature != nil {
		t.Error("Compose mutated original request temperature")
	}
}
