package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NexixAI/nexixai-agentos/agentorchestrator"
	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// newMockVLLM creates a mock vLLM server that captures requests and returns
// configurable responses.
func newMockVLLM(t *testing.T) (*httptest.Server, *vllmCapture) {
	t.Helper()
	cap := &vllmCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("failed to read request body: %v", err)
				http.Error(w, "read error", http.StatusInternalServerError)
				return
			}
			var parsed map[string]any
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Errorf("failed to parse request body: %v", err)
				http.Error(w, "parse error", http.StatusBadRequest)
				return
			}
			cap.LastChatRequest = parsed
			cap.ChatCallCount++

			resp := map[string]any{
				"id":      "chatcmpl-test",
				"object":  "chat.completion",
				"created": 1700000000,
				"model":   parsed["model"],
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "mock response",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     10,
					"completion_tokens": 5,
					"total_tokens":      15,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			cap.ModelsCallCount++
			resp := map[string]any{
				"object": "list",
				"data": []map[string]any{
					{
						"id":       "Qwen/Qwen3-32B",
						"object":   "model",
						"created":  1700000000,
						"owned_by": "vllm",
					},
					{
						"id":       "Qwen/Qwen3-4B-AWQ",
						"object":   "model",
						"created":  1700000000,
						"owned_by": "vllm",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	return srv, cap
}

type vllmCapture struct {
	LastChatRequest map[string]any
	ChatCallCount   int
	ModelsCallCount int
}

func testCfg(baseURL string) ChatToolsConfig {
	return ChatToolsConfig{
		ModelConfig: config.ModelConfig{
			Provider:     "openai",
			BaseURL:      baseURL,
			APIKey:       "test-key",
			DefaultModel: "Qwen/Qwen3-32B",
		},
		RouterConfig: agentorchestrator.RouterConfig{
			Enabled:       false, // disable classifier for deterministic tests
			ClassifierURL: "",
		},
	}
}

func testState(defaultModel string) *chatToolsState {
	return &chatToolsState{
		defaultModel: defaultModel,
	}
}

func TestChatCompletionDispatch(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	state := testState(cfg.ModelConfig.DefaultModel)
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Messages: []chatMessage{
			{Role: "user", Content: textContent("Hello, world!")},
		},
		MaxTokens: 100,
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result, err := handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if cap.ChatCallCount != 1 {
		t.Errorf("expected 1 chat call, got %d", cap.ChatCallCount)
	}

	// Verify the request was sent with the correct model.
	if m, ok := cap.LastChatRequest["model"].(string); !ok || m != "Qwen/Qwen3-32B" {
		t.Errorf("expected model Qwen/Qwen3-32B, got %v", cap.LastChatRequest["model"])
	}

	// Verify result has expected structure.
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if resultMap["id"] != "chatcmpl-test" {
		t.Errorf("unexpected result id: %v", resultMap["id"])
	}
}

func TestChatCompletionEnableThinkingInjection(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	// Create a mock classifier that returns "R" (reasoning).
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "R"}},
			},
		})
	}))
	defer classifier.Close()

	cfg := ChatToolsConfig{
		ModelConfig: config.ModelConfig{
			Provider:     "openai",
			BaseURL:      vllm.URL,
			APIKey:       "test-key",
			DefaultModel: "Qwen/Qwen3-32B",
		},
		RouterConfig: agentorchestrator.RouterConfig{
			Enabled:       true,
			ClassifierURL: classifier.URL,
		},
	}

	state := testState(cfg.ModelConfig.DefaultModel)
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Messages: []chatMessage{
			{Role: "user", Content: textContent("Prove that sqrt(2) is irrational")},
		},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Verify enable_thinking was injected as true.
	kwargs, ok := cap.LastChatRequest["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat_template_kwargs in request, got %v", cap.LastChatRequest)
	}
	if kwargs["enable_thinking"] != true {
		t.Errorf("expected enable_thinking=true, got %v", kwargs["enable_thinking"])
	}
}

func TestChatCompletionDisabledRouter(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	state := testState(cfg.ModelConfig.DefaultModel)
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Messages: []chatMessage{
			{Role: "user", Content: textContent("Prove sqrt(2) is irrational")},
		},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// With router disabled, enable_thinking should be false.
	kwargs, ok := cap.LastChatRequest["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat_template_kwargs in request, got %v", cap.LastChatRequest)
	}
	if kwargs["enable_thinking"] != false {
		t.Errorf("expected enable_thinking=false when router disabled, got %v", kwargs["enable_thinking"])
	}
}

func TestChatCompletionCustomModel(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	state := testState(cfg.ModelConfig.DefaultModel)
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Model: "custom-model",
		Messages: []chatMessage{
			{Role: "user", Content: textContent("hello")},
		},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if m, ok := cap.LastChatRequest["model"].(string); !ok || m != "custom-model" {
		t.Errorf("expected model custom-model, got %v", cap.LastChatRequest["model"])
	}
}

func TestChatCompletionEmptyMessages(t *testing.T) {
	vllm, _ := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	state := testState(cfg.ModelConfig.DefaultModel)
	handler := chatCompletionHandler(cfg, state)

	argsJSON := json.RawMessage(`{"messages":[]}`)
	_, err := handler(context.Background(), argsJSON)
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestChatCompletionTemperature(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	state := testState(cfg.ModelConfig.DefaultModel)
	handler := chatCompletionHandler(cfg, state)

	temp := 0.7
	args := chatCompletionArgs{
		Messages: []chatMessage{
			{Role: "user", Content: textContent("hello")},
		},
		Temperature: &temp,
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if v, ok := cap.LastChatRequest["temperature"].(float64); !ok || v != 0.7 {
		t.Errorf("expected temperature=0.7, got %v", cap.LastChatRequest["temperature"])
	}
}

func TestListModels(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	handler := listModelsHandler(cfg)

	result, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if cap.ModelsCallCount != 1 {
		t.Errorf("expected 1 models call, got %d", cap.ModelsCallCount)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}

	data, ok := resultMap["data"].([]any)
	if !ok {
		t.Fatalf("expected data array, got %T", resultMap["data"])
	}
	if len(data) != 2 {
		t.Errorf("expected 2 models, got %d", len(data))
	}

	// Verify first model ID.
	first, ok := data[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map for first model, got %T", data[0])
	}
	if first["id"] != "Qwen/Qwen3-32B" {
		t.Errorf("unexpected first model id: %v", first["id"])
	}
}

func TestListModelsNoBaseURL(t *testing.T) {
	cfg := ChatToolsConfig{
		ModelConfig: config.ModelConfig{
			BaseURL: "",
		},
	}
	handler := listModelsHandler(cfg)
	_, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when base URL is empty")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetActiveModel(t *testing.T) {
	state := testState("Qwen/Qwen3-32B")

	handler := getActiveModelHandler(state)
	result, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	m, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", result)
	}
	if m["model"] != "Qwen/Qwen3-32B" {
		t.Errorf("expected model Qwen/Qwen3-32B, got %s", m["model"])
	}
}

func TestGetActiveModelNotConfigured(t *testing.T) {
	state := testState("")

	handler := getActiveModelHandler(state)
	_, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when model not configured")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegisterChatTools(t *testing.T) {
	vllm, _ := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	registry := mcp.NewToolRegistry()

	if err := RegisterChatTools(registry, cfg); err != nil {
		t.Fatalf("RegisterChatTools error: %v", err)
	}

	// Verify all six tools are registered.
	tools := registry.List()
	if len(tools) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{
		"chat_completion", "list_models", "get_active_model",
		"swap_model", "get_routing_config", "set_thinking_mode",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("tool %q not found in registry", name)
		}
	}

	// Verify clearance tiers: T0/T0/T1/T3/T3/T3.
	clearanceTests := []struct {
		name     string
		want     mcp.ClearanceTier
		wantDesc string
	}{
		{"list_models", mcp.ClearancePublic, "T0"},
		{"get_active_model", mcp.ClearancePublic, "T0"},
		{"chat_completion", mcp.ClearanceInternal, "T1"},
		{"swap_model", mcp.ClearanceAdmin, "T3"},
		{"get_routing_config", mcp.ClearanceAdmin, "T3"},
		{"set_thinking_mode", mcp.ClearanceAdmin, "T3"},
	}
	for _, tc := range clearanceTests {
		tool := registry.Get(tc.name)
		if tool == nil {
			t.Fatalf("%s tool not found", tc.name)
		}
		if tool.MinClearance != tc.want {
			t.Errorf("%s clearance = %d, want %d (%s)", tc.name, tool.MinClearance, tc.want, tc.wantDesc)
		}
	}

	chatTool := registry.Get("chat_completion")
	if chatTool == nil {
		t.Fatal("chat_completion tool not found")
	}
	if !strings.Contains(chatTool.Description, "raw upstream model output") {
		t.Fatalf("chat_completion description should declare the raw pass-through contract, got: %q", chatTool.Description)
	}
}

func TestRegisterChatToolsDuplicateError(t *testing.T) {
	vllm, _ := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	registry := mcp.NewToolRegistry()

	if err := RegisterChatTools(registry, cfg); err != nil {
		t.Fatalf("first RegisterChatTools error: %v", err)
	}

	// Second registration should fail.
	err := RegisterChatTools(registry, cfg)
	if err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}

// --- swap_model tests ---

func TestSwapModelChangesDefault(t *testing.T) {
	state := testState("Qwen/Qwen3-32B")
	handler := swapModelHandler(state)

	params := json.RawMessage(`{"model_name": "Qwen/Qwen3-4B-AWQ"}`)
	result, err := handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	m, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", result)
	}
	if m["previous_model"] != "Qwen/Qwen3-32B" {
		t.Errorf("expected previous_model=Qwen/Qwen3-32B, got %s", m["previous_model"])
	}
	if m["new_model"] != "Qwen/Qwen3-4B-AWQ" {
		t.Errorf("expected new_model=Qwen/Qwen3-4B-AWQ, got %s", m["new_model"])
	}
	if m["status"] != "ok" {
		t.Errorf("expected status=ok, got %s", m["status"])
	}

	// Verify the state was actually mutated.
	if got := state.DefaultModel(); got != "Qwen/Qwen3-4B-AWQ" {
		t.Errorf("state.DefaultModel() = %s, want Qwen/Qwen3-4B-AWQ", got)
	}
}

func TestSwapModelEmptyReturnsError(t *testing.T) {
	state := testState("Qwen/Qwen3-32B")
	handler := swapModelHandler(state)

	params := json.RawMessage(`{"model_name": ""}`)
	_, err := handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for empty model_name")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify the state was NOT mutated.
	if got := state.DefaultModel(); got != "Qwen/Qwen3-32B" {
		t.Errorf("state.DefaultModel() = %s, want Qwen/Qwen3-32B (unchanged)", got)
	}
}

func TestSwapModelWhitespaceOnlyReturnsError(t *testing.T) {
	state := testState("Qwen/Qwen3-32B")
	handler := swapModelHandler(state)

	params := json.RawMessage(`{"model_name": "   "}`)
	_, err := handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for whitespace-only model_name")
	}
}

func TestSwapModelVisibleToChatCompletion(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	state := testState(cfg.ModelConfig.DefaultModel)

	// Swap to a different model.
	state.SwapModel("new-model-v2")

	// A chat_completion without explicit model should use the swapped model.
	handler := chatCompletionHandler(cfg, state)
	args := chatCompletionArgs{
		Messages: []chatMessage{{Role: "user", Content: textContent("hello")}},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if m, ok := cap.LastChatRequest["model"].(string); !ok || m != "new-model-v2" {
		t.Errorf("expected model new-model-v2, got %v", cap.LastChatRequest["model"])
	}
}

// --- get_routing_config tests ---

func TestGetRoutingConfigReturnsCurrentConfig(t *testing.T) {
	cfg := ChatToolsConfig{
		ModelConfig: config.ModelConfig{
			BaseURL:      "http://localhost:8000",
			DefaultModel: "Qwen/Qwen3-32B",
		},
		RouterConfig: agentorchestrator.RouterConfig{
			Enabled:       true,
			ClassifierURL: "http://localhost:8002/v1/chat/completions",
		},
	}
	state := testState(cfg.ModelConfig.DefaultModel)

	handler := getRoutingConfigHandler(cfg, state)
	result, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}

	if m["classifier_url"] != "http://localhost:8002/v1/chat/completions" {
		t.Errorf("classifier_url = %v", m["classifier_url"])
	}
	if m["model_base_url"] != "http://localhost:8000" {
		t.Errorf("model_base_url = %v", m["model_base_url"])
	}
	if m["default_model"] != "Qwen/Qwen3-32B" {
		t.Errorf("default_model = %v", m["default_model"])
	}
	if m["router_enabled"] != true {
		t.Errorf("router_enabled = %v", m["router_enabled"])
	}
	if m["thinking_mode"] != "classifier" {
		t.Errorf("thinking_mode = %v, want classifier", m["thinking_mode"])
	}
}

func TestGetRoutingConfigReflectsSwappedModel(t *testing.T) {
	cfg := ChatToolsConfig{
		ModelConfig: config.ModelConfig{
			BaseURL:      "http://localhost:8000",
			DefaultModel: "Qwen/Qwen3-32B",
		},
		RouterConfig: agentorchestrator.RouterConfig{
			Enabled: true,
		},
	}
	state := testState(cfg.ModelConfig.DefaultModel)
	state.SwapModel("swapped-model")

	handler := getRoutingConfigHandler(cfg, state)
	result, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	m := result.(map[string]any)
	if m["default_model"] != "swapped-model" {
		t.Errorf("default_model = %v, want swapped-model", m["default_model"])
	}
}

// --- set_thinking_mode tests ---

func TestSetThinkingModeOverridesClassifier(t *testing.T) {
	state := testState("Qwen/Qwen3-32B")

	// Initially, thinking mode should be classifier-controlled.
	if override := state.ThinkingModeOverride(); override != nil {
		t.Fatalf("expected nil override initially, got %v", *override)
	}

	handler := setThinkingModeHandler(state)

	// Set thinking mode to true.
	result, err := handler(context.Background(), json.RawMessage(`{"enabled": true}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	m, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", result)
	}
	if m["mode"] != "true" {
		t.Errorf("mode = %s, want true", m["mode"])
	}
	if m["previous_mode"] != "classifier" {
		t.Errorf("previous_mode = %s, want classifier", m["previous_mode"])
	}

	// Verify state was updated.
	override := state.ThinkingModeOverride()
	if override == nil || !*override {
		t.Error("expected ThinkingModeOverride to be true after set")
	}

	// Set to false.
	result2, err := handler(context.Background(), json.RawMessage(`{"enabled": false}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	m2 := result2.(map[string]string)
	if m2["mode"] != "false" {
		t.Errorf("mode = %s, want false", m2["mode"])
	}
	if m2["previous_mode"] != "true" {
		t.Errorf("previous_mode = %s, want true", m2["previous_mode"])
	}

	override2 := state.ThinkingModeOverride()
	if override2 == nil || *override2 {
		t.Error("expected ThinkingModeOverride to be false after second set")
	}
}

func TestSetThinkingModeAffectsChatCompletion(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	cfg := testCfg(vllm.URL)
	// Router is disabled, so classifier would return false.
	state := testState(cfg.ModelConfig.DefaultModel)

	// Force thinking mode to true via override.
	state.SetThinkingMode(true)

	handler := chatCompletionHandler(cfg, state)
	args := chatCompletionArgs{
		Messages: []chatMessage{{Role: "user", Content: textContent("hello")}},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Even though router is disabled (which would default to false),
	// the override should force enable_thinking=true.
	kwargs, ok := cap.LastChatRequest["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat_template_kwargs in request, got %v", cap.LastChatRequest)
	}
	if kwargs["enable_thinking"] != true {
		t.Errorf("expected enable_thinking=true (override), got %v", kwargs["enable_thinking"])
	}
}

func TestSetThinkingModeFalseOverridesClassifier(t *testing.T) {
	vllm, cap := newMockVLLM(t)
	defer vllm.Close()

	// Set up an enabled router with a classifier that returns "R" (reasoning).
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "R"}},
			},
		})
	}))
	defer classifier.Close()

	cfg := ChatToolsConfig{
		ModelConfig: config.ModelConfig{
			Provider:     "openai",
			BaseURL:      vllm.URL,
			APIKey:       "test-key",
			DefaultModel: "Qwen/Qwen3-32B",
		},
		RouterConfig: agentorchestrator.RouterConfig{
			Enabled:       true,
			ClassifierURL: classifier.URL,
		},
	}
	state := testState(cfg.ModelConfig.DefaultModel)

	// Override thinking mode to false — should suppress classifier's "R" decision.
	state.SetThinkingMode(false)

	handler := chatCompletionHandler(cfg, state)
	args := chatCompletionArgs{
		Messages: []chatMessage{{Role: "user", Content: textContent("Prove sqrt(2) is irrational")}},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	kwargs, ok := cap.LastChatRequest["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat_template_kwargs in request, got %v", cap.LastChatRequest)
	}
	if kwargs["enable_thinking"] != false {
		t.Errorf("expected enable_thinking=false (override), got %v", kwargs["enable_thinking"])
	}
}

// =====================================================================
// v11.0 Issue #1: chatContent union-type unmarshaling and helpers
// =====================================================================

func TestChatContent_UnmarshalStringForm(t *testing.T) {
	var c chatContent
	if err := json.Unmarshal([]byte(`"hello world"`), &c); err != nil {
		t.Fatalf("unmarshal string form: %v", err)
	}
	if c.IsMultipart() {
		t.Errorf("string form should not be multipart")
	}
	if c.AsText() != "hello world" {
		t.Errorf("AsText=%q, want %q", c.AsText(), "hello world")
	}
	if c.HasImage() {
		t.Errorf("string form should not report HasImage")
	}
}

func TestChatContent_UnmarshalEmptyString(t *testing.T) {
	var c chatContent
	if err := json.Unmarshal([]byte(`""`), &c); err != nil {
		t.Fatalf("unmarshal empty string: %v", err)
	}
	if c.IsMultipart() || c.AsText() != "" || c.HasImage() {
		t.Errorf("empty string content unexpected state: multipart=%v text=%q image=%v", c.IsMultipart(), c.AsText(), c.HasImage())
	}
}

func TestChatContent_UnmarshalMultipartTextAndImage(t *testing.T) {
	in := `[{"type":"text","text":"what is this?"},{"type":"image_url","image_url":{"url":"https://example.com/pic.jpg","detail":"auto"}}]`
	var c chatContent
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal multipart: %v", err)
	}
	if !c.IsMultipart() {
		t.Errorf("should be multipart")
	}
	if c.AsText() != "what is this?" {
		t.Errorf("AsText=%q, want %q", c.AsText(), "what is this?")
	}
	if !c.HasImage() {
		t.Errorf("should report HasImage")
	}
	parts := c.Parts()
	if len(parts) != 2 {
		t.Fatalf("len(Parts)=%d, want 2", len(parts))
	}
	if parts[1].ImageURL == nil || parts[1].ImageURL.URL != "https://example.com/pic.jpg" {
		t.Errorf("image URL not preserved: %+v", parts[1].ImageURL)
	}
}

func TestChatContent_UnmarshalMultipleTextParts(t *testing.T) {
	in := `[{"type":"text","text":"hello"},{"type":"text","text":"world"}]`
	var c chatContent
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.AsText() != "hello world" {
		t.Errorf("AsText=%q, want %q", c.AsText(), "hello world")
	}
	if c.HasImage() {
		t.Errorf("text-only multipart should not report HasImage")
	}
}

func TestChatContent_UnmarshalInvalid(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"number", `42`},
		{"bool", `true`},
		{"null-object", `{"type":"text"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c chatContent
			if err := json.Unmarshal([]byte(tc.in), &c); err == nil {
				t.Errorf("expected error for %q, got nil", tc.in)
			}
		})
	}
}

func TestChatContent_MarshalRoundTripString(t *testing.T) {
	orig := textContent("hello")
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"hello"` {
		t.Errorf("marshaled=%q, want %q", string(data), `"hello"`)
	}
	var round chatContent
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if round.IsMultipart() || round.AsText() != "hello" {
		t.Errorf("round trip altered content")
	}
}

func TestChatContent_MarshalRoundTripMultipart(t *testing.T) {
	orig := multipartContent(
		chatContentPart{Type: "text", Text: "describe"},
		chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://x/y.png"}},
	)
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round chatContent
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if !round.IsMultipart() || !round.HasImage() || round.AsText() != "describe" {
		t.Errorf("round trip altered multipart: multipart=%v image=%v text=%q", round.IsMultipart(), round.HasImage(), round.AsText())
	}
}

func TestChatContent_MessageLiteralCompatibility(t *testing.T) {
	// Exercises the field-initialization path used across the codebase
	// (chatMessage struct literal with Content).
	msgs := []chatMessage{
		{Role: "system", Content: textContent("be brief")},
		{Role: "user", Content: multipartContent(
			chatContentPart{Type: "text", Text: "what is this?"},
			chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://x/y.png"}},
		)},
	}
	data, err := json.Marshal(map[string]any{"messages": msgs})
	if err != nil {
		t.Fatalf("marshal wrapping map: %v", err)
	}
	var parsed struct {
		Messages []chatMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal wrapping map: %v", err)
	}
	if len(parsed.Messages) != 2 {
		t.Fatalf("len(messages)=%d, want 2", len(parsed.Messages))
	}
	if parsed.Messages[0].Content.IsMultipart() {
		t.Errorf("message 0 should be string form")
	}
	if !parsed.Messages[1].Content.IsMultipart() || !parsed.Messages[1].Content.HasImage() {
		t.Errorf("message 1 should be multipart with image")
	}
}

// =====================================================================
// v11.0 Issue #2: content validation (URL scheme + per-message cap)
// =====================================================================

func TestValidateContent_StringFormAccepted(t *testing.T) {
	if err := validateContent(textContent("hello")); err != nil {
		t.Errorf("string form should be accepted, got error: %v", err)
	}
}

func TestValidateContent_HTTPSAccepted(t *testing.T) {
	c := multipartContent(
		chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/pic.jpg"}},
	)
	if err := validateContent(c); err != nil {
		t.Errorf("https URL should be accepted, got error: %v", err)
	}
}

func TestValidateContent_HTTPAccepted(t *testing.T) {
	c := multipartContent(
		chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "http://grafana.internal/panel.png"}},
	)
	if err := validateContent(c); err != nil {
		t.Errorf("http URL should be accepted (internal images), got: %v", err)
	}
}

func TestValidateContent_RejectedSchemes(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"file", "file:///etc/passwd"},
		{"data-base64", "data:image/png;base64,iVBORw0KG..."},
		{"ftp", "ftp://example.com/x.png"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := multipartContent(
				chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: tc.url}},
			)
			if err := validateContent(c); err == nil {
				t.Errorf("scheme %q should be rejected", tc.url)
			}
		})
	}
}

func TestValidateContent_NilImageURL(t *testing.T) {
	c := multipartContent(
		chatContentPart{Type: "image_url", ImageURL: nil},
	)
	if err := validateContent(c); err == nil {
		t.Errorf("nil ImageURL struct should be rejected")
	}
}

func TestValidateContent_UnknownPartType(t *testing.T) {
	c := multipartContent(
		chatContentPart{Type: "audio", Text: "beep"},
	)
	err := validateContent(c)
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("expected unsupported type error, got: %v", err)
	}
}

func TestValidateContent_MissingTypeField(t *testing.T) {
	c := multipartContent(
		chatContentPart{Type: "", Text: "x"},
	)
	if err := validateContent(c); err == nil {
		t.Errorf("empty type should be rejected")
	}
}

func TestValidateContent_CapEnforcement(t *testing.T) {
	// Build 9 image parts (exceeds cap of 8).
	var parts []chatContentPart
	for i := 0; i < 9; i++ {
		parts = append(parts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://x/y.png"}})
	}
	c := multipartContent(parts...)
	err := validateContent(c)
	if err == nil || !strings.Contains(err.Error(), "image part cap") {
		t.Errorf("expected cap error for 9 image parts, got: %v", err)
	}

	// At the boundary: exactly 8 must pass.
	c8 := multipartContent(parts[:8]...)
	if err := validateContent(c8); err != nil {
		t.Errorf("exactly %d image parts should be accepted, got: %v", chatMaxImageParts, err)
	}
}

func TestChatCompletionHandler_RejectsInvalidImageURL(t *testing.T) {
	// End-to-end: handler rejects the bad request before attempting upstream.
	mock, _ := newMockVLLM(t)
	defer mock.Close()

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Messages: []chatMessage{{
			Role: "user",
			Content: multipartContent(
				chatContentPart{Type: "text", Text: "what is this?"},
				chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "file:///etc/passwd"}},
			),
		}},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = handler(context.Background(), argsJSON)
	if err == nil {
		t.Errorf("handler should have rejected file:// URL")
	}
	if !strings.Contains(err.Error(), "file://") {
		t.Errorf("error should mention file://, got: %v", err)
	}
}

// =====================================================================
// v11.0 Issue #3: upstream forwarding preserves multipart content
// =====================================================================

func TestChatCompletion_ComposerPathPreservesMultipart(t *testing.T) {
	mock, cap := newMockVLLM(t)
	defer mock.Close()

	// Build a minimal composer: no KB, minimal intent config (defaults only,
	// no system prompts). Shallow-copies messages without touching user content.
	composer := agentorchestrator.NewComposer(minimalIntentConfigForTest(), nil)

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		Composer:     composer,
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Messages: []chatMessage{{
			Role: "user",
			Content: multipartContent(
				chatContentPart{Type: "text", Text: "describe this"},
				chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/pic.jpg"}},
			),
		}},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	msgs, ok := cap.LastChatRequest["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("expected messages in upstream request, got %v", cap.LastChatRequest)
	}
	// User message should be the last one; content should be an array of parts.
	userMsg, ok := msgs[len(msgs)-1].(map[string]any)
	if !ok {
		t.Fatalf("user message wrong shape: %T", msgs[len(msgs)-1])
	}
	content, ok := userMsg["content"].([]any)
	if !ok {
		t.Fatalf("composer path stringified multipart content: got %T (%v)", userMsg["content"], userMsg["content"])
	}
	if len(content) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(content))
	}
	part0 := content[0].(map[string]any)
	if part0["type"] != "text" || part0["text"] != "describe this" {
		t.Errorf("part 0 not preserved: %v", part0)
	}
	part1 := content[1].(map[string]any)
	if part1["type"] != "image_url" {
		t.Errorf("part 1 type not preserved: %v", part1)
	}
	imageURL := part1["image_url"].(map[string]any)
	if imageURL["url"] != "https://example.com/pic.jpg" {
		t.Errorf("image url not preserved: %v", imageURL)
	}
}

func TestChatCompletion_ComposerPathTextUnchanged(t *testing.T) {
	// Backward compat: text-only through Composer path still stringifies content.
	mock, cap := newMockVLLM(t)
	defer mock.Close()

	composer := agentorchestrator.NewComposer(minimalIntentConfigForTest(), nil)

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		Composer:     composer,
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Messages: []chatMessage{
			{Role: "user", Content: textContent("hello")},
		},
	}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	msgs, ok := cap.LastChatRequest["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("expected messages, got %v", cap.LastChatRequest)
	}
	userMsg := msgs[len(msgs)-1].(map[string]any)
	if content, ok := userMsg["content"].(string); !ok || content != "hello" {
		t.Errorf("text content regressed: got %T (%v)", userMsg["content"], userMsg["content"])
	}
}

func TestChatCompletion_NonComposerPathPreservesMultipart(t *testing.T) {
	// Non-composer path was already preserving multipart via chatContent.MarshalJSON
	// in Issue #1; this guards against regressions from Issue #3 changes.
	mock, cap := newMockVLLM(t)
	defer mock.Close()

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{
		Messages: []chatMessage{{
			Role: "user",
			Content: multipartContent(
				chatContentPart{Type: "text", Text: "hi"},
				chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/x.png"}},
			),
		}},
	}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	msgs := cap.LastChatRequest["messages"].([]any)
	userMsg := msgs[len(msgs)-1].(map[string]any)
	if _, ok := userMsg["content"].([]any); !ok {
		t.Errorf("non-composer path stringified multipart: %T", userMsg["content"])
	}
}

// minimalIntentConfigForTest returns the smallest IntentConfig that lets
// Composer.Compose run without panicking. All intents map to destination=model
// with no system prompt or overrides, so user messages pass through unchanged.
func minimalIntentConfigForTest() *agentorchestrator.IntentConfig {
	return &agentorchestrator.IntentConfig{
		Version: "test",
		Intents: map[agentorchestrator.IntentName]agentorchestrator.IntentDef{
			agentorchestrator.IntentGeneral: {Destination: "model"},
			agentorchestrator.IntentCode:    {Destination: "model"},
		},
	}
}

// =====================================================================
// v11.0 Issue #4: metrics + audit instrumentation
// =====================================================================

// spyAuditLogger records every audit.Entry for test inspection.
type spyAuditLogger struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (s *spyAuditLogger) Log(e audit.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
}

func (s *spyAuditLogger) Close() error { return nil }

func (s *spyAuditLogger) snapshot() []audit.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]audit.Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

func TestChatCompletion_TextOnlyEmitsNoAudit(t *testing.T) {
	mock, _ := newMockVLLM(t)
	defer mock.Close()
	auditLog := &spyAuditLogger{}

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		AuditLogger:  auditLog,
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{Role: "user", Content: textContent("hello")}}}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if got := len(auditLog.snapshot()); got != 0 {
		t.Errorf("text-only should not emit audit entries, got %d", got)
	}
}

func TestChatCompletion_MultipartEmitsAuditWithURLs(t *testing.T) {
	mock, _ := newMockVLLM(t)
	defer mock.Close()
	auditLog := &spyAuditLogger{}

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		AuditLogger:  auditLog,
		// ChatAuditImageHash defaults to false → URLs logged verbatim.
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{
		Role: "user",
		Content: multipartContent(
			chatContentPart{Type: "text", Text: "describe"},
			chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/a.png"}},
			chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/b.png"}},
		),
	}}}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	entries := auditLog.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Action != "chat.multimodal" || e.Resource != "tool:chat_completion" || e.Outcome != "accepted" {
		t.Errorf("audit entry fields unexpected: %+v", e)
	}
	refs, ok := e.Meta["image_refs"].([]string)
	if !ok || len(refs) != 2 {
		t.Fatalf("audit image_refs wrong: %v", e.Meta["image_refs"])
	}
	if refs[0] != "https://example.com/a.png" || refs[1] != "https://example.com/b.png" {
		t.Errorf("expected URLs logged verbatim, got %v", refs)
	}
	if got, _ := e.Meta["image_parts"].(int); got != 2 {
		t.Errorf("image_parts=%v, want 2", e.Meta["image_parts"])
	}
}

func TestChatCompletion_MultipartAuditHashMode(t *testing.T) {
	mock, _ := newMockVLLM(t)
	defer mock.Close()
	auditLog := &spyAuditLogger{}

	cfg := ChatToolsConfig{
		ModelConfig:        config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig:       agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		AuditLogger:        auditLog,
		ChatAuditImageHash: true,
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{
		Role: "user",
		Content: multipartContent(
			chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/secret?token=abc"}},
		),
	}}}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	entries := auditLog.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	refs := entries[0].Meta["image_refs"].([]string)
	if len(refs) != 1 || !strings.HasPrefix(refs[0], "sha256:") {
		t.Errorf("expected sha256: prefix in hash mode, got %v", refs)
	}
	if strings.Contains(refs[0], "secret") || strings.Contains(refs[0], "token") {
		t.Errorf("hash mode should not include plaintext URL parts: %s", refs[0])
	}
}

func TestImageRefForAudit(t *testing.T) {
	url := "https://example.com/pic.jpg"
	if got := imageRefForAudit(url, false); got != url {
		t.Errorf("verbatim mode should return URL, got %q", got)
	}
	h := imageRefForAudit(url, true)
	if !strings.HasPrefix(h, "sha256:") {
		t.Errorf("hash mode should prefix sha256:, got %q", h)
	}
	// Deterministic.
	if h2 := imageRefForAudit(url, true); h2 != h {
		t.Errorf("hash not deterministic: %q vs %q", h, h2)
	}
}

// =====================================================================
// v11.0 Issue #5: multimodal clearance gate
// =====================================================================

// stubClearanceStore returns a fixed tier (or error) for any agentID.
type stubClearanceStore struct {
	tier mcp.ClearanceTier
	err  error
}

func (s *stubClearanceStore) GetClearance(_ context.Context, _ string) (mcp.ClearanceTier, error) {
	return s.tier, s.err
}

func TestChatCompletion_MultipartBelowTierDenied(t *testing.T) {
	mock, _ := newMockVLLM(t)
	defer mock.Close()

	cfg := ChatToolsConfig{
		ModelConfig:    config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig:   agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		ClearanceStore: &stubClearanceStore{tier: mcp.ClearanceInternal}, // T1 < T2
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{
		Role: "user",
		Content: multipartContent(
			chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/x.png"}},
		),
	}}}
	argsJSON, _ := json.Marshal(args)
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{AgentID: "agent-1", TenantID: "t1"})
	_, err := handler(ctx, argsJSON)
	if err == nil || !strings.Contains(err.Error(), "insufficient clearance for multimodal") {
		t.Errorf("expected multimodal clearance denial, got: %v", err)
	}
}

func TestChatCompletion_MultipartAtTierAllowed(t *testing.T) {
	mock, _ := newMockVLLM(t)
	defer mock.Close()

	cfg := ChatToolsConfig{
		ModelConfig:    config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig:   agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		ClearanceStore: &stubClearanceStore{tier: mcp.ClearanceExecute}, // T2 == required
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{
		Role: "user",
		Content: multipartContent(
			chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/x.png"}},
		),
	}}}
	argsJSON, _ := json.Marshal(args)
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{AgentID: "agent-1", TenantID: "t1"})
	if _, err := handler(ctx, argsJSON); err != nil {
		t.Errorf("tier-at-required should succeed, got error: %v", err)
	}
}

func TestChatCompletion_TextBelowTierUnaffected(t *testing.T) {
	// Backward compat: a low-tier caller with text-only content passes
	// through without the multimodal gate blocking.
	mock, _ := newMockVLLM(t)
	defer mock.Close()

	cfg := ChatToolsConfig{
		ModelConfig:    config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig:   agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		ClearanceStore: &stubClearanceStore{tier: mcp.ClearancePublic}, // T0
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{Role: "user", Content: textContent("hello")}}}
	argsJSON, _ := json.Marshal(args)
	ctx := mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{AgentID: "agent-1", TenantID: "t1"})
	if _, err := handler(ctx, argsJSON); err != nil {
		t.Errorf("text-only should not be gated by multimodal tier, got: %v", err)
	}
}

func TestChatCompletion_MultipartClearanceStoreNilSkipsGate(t *testing.T) {
	// When ClearanceStore is nil (tests/dev), the gate is skipped. This must
	// not affect existing deployments that do wire a store.
	mock, _ := newMockVLLM(t)
	defer mock.Close()

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: mock.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: mock.URL, Enabled: false},
		// ClearanceStore: nil
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{
		Role:    "user",
		Content: multipartContent(chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://x/y.png"}}),
	}}}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Errorf("nil ClearanceStore should skip gate, got: %v", err)
	}
}

// =====================================================================
// v11.0 Issue #6: classifier bypass on multipart content
// =====================================================================

func TestChatCompletion_MultipartBypassesClassifier(t *testing.T) {
	// Mock classifier that fails the test if called. With multipart content,
	// it should never be hit.
	classifierCalled := false
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		classifierCalled = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "R"}},
			},
		})
	}))
	defer classifier.Close()

	vllm, _ := newMockVLLM(t)
	defer vllm.Close()

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: vllm.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: classifier.URL, Enabled: true},
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{
		Role: "user",
		Content: multipartContent(
			chatContentPart{Type: "text", Text: "what is this?"},
			chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/x.png"}},
		),
	}}}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if classifierCalled {
		t.Errorf("classifier should NOT be called for multipart content with image")
	}
}

func TestChatCompletion_TextCallStillClassifies(t *testing.T) {
	// Backward compat: text-only requests still hit the classifier.
	classifierCalled := false
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		classifierCalled = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "G"}},
			},
		})
	}))
	defer classifier.Close()

	vllm, _ := newMockVLLM(t)
	defer vllm.Close()

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: vllm.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: classifier.URL, Enabled: true},
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{Role: "user", Content: textContent("hello world")}}}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !classifierCalled {
		t.Errorf("classifier should be called for text-only content")
	}
}

func TestChatCompletion_MultimodalTextOnlyPartsStillClassifies(t *testing.T) {
	// Edge case: multipart content that contains ONLY text parts (no images)
	// should still go through the classifier normally.
	classifierCalled := false
	classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		classifierCalled = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "G"}}},
		})
	}))
	defer classifier.Close()

	vllm, _ := newMockVLLM(t)
	defer vllm.Close()

	cfg := ChatToolsConfig{
		ModelConfig:  config.ModelConfig{BaseURL: vllm.URL, DefaultModel: "test-model"},
		RouterConfig: agentorchestrator.RouterConfig{ClassifierURL: classifier.URL, Enabled: true},
	}
	state := &chatToolsState{defaultModel: "test-model"}
	handler := chatCompletionHandler(cfg, state)

	args := chatCompletionArgs{Messages: []chatMessage{{
		Role: "user",
		Content: multipartContent(
			chatContentPart{Type: "text", Text: "part 1"},
			chatContentPart{Type: "text", Text: "part 2"},
		),
	}}}
	argsJSON, _ := json.Marshal(args)
	if _, err := handler(context.Background(), argsJSON); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !classifierCalled {
		t.Errorf("classifier should run for multipart-all-text (no image)")
	}
}
