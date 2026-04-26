package agentorchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassify_ReasoningLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "reasoning"}},
			},
		})
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	d := Classify(context.Background(), cfg, "Prove that the square root of 2 is irrational")

	if d.Label != LabelReasoning {
		t.Errorf("expected reasoning, got %q", d.Label)
	}
	if d.Destination != "reasoning" {
		t.Errorf("expected destination reasoning, got %q", d.Destination)
	}
	if d.Intent != IntentReasoning {
		t.Errorf("expected intent reasoning, got %q", d.Intent)
	}
	if d.Fallback {
		t.Error("expected fallback=false")
	}
	if d.EnableThinking != true {
		t.Errorf("expected EnableThinking=true, got %v", d.EnableThinking)
	}
}

func TestClassify_GeneralLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "general"}},
			},
		})
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	d := Classify(context.Background(), cfg, "Write a hello world function")

	if d.Label != LabelGeneral {
		t.Errorf("expected general, got %q", d.Label)
	}
	if d.Destination != "general" {
		t.Errorf("expected destination general, got %q", d.Destination)
	}
	if d.Fallback {
		t.Error("expected fallback=false")
	}
}

func TestClassify_OllamaFormat(t *testing.T) {
	// Ollama returns { "message": { "content": "..." } } instead of choices array.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"content": "reasoning"},
		})
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	d := Classify(context.Background(), cfg, "What is 17 * 23?")

	if d.Label != LabelReasoning {
		t.Errorf("expected reasoning from Ollama format, got %q", d.Label)
	}
}

func TestClassify_TimeoutFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // exceed 500ms timeout
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "reasoning"}},
			},
		})
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	d := Classify(context.Background(), cfg, "complex math problem")

	if d.Label != LabelGeneral {
		t.Errorf("expected fallback to general, got %q", d.Label)
	}
	if !d.Fallback {
		t.Error("expected fallback=true on timeout")
	}
	if d.Error == "" {
		t.Error("expected error message on fallback")
	}
}

func TestClassify_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	d := Classify(context.Background(), cfg, "test prompt")

	if d.Label != LabelGeneral {
		t.Errorf("expected fallback to general on 500, got %q", d.Label)
	}
	if !d.Fallback {
		t.Error("expected fallback=true on server error")
	}
}

func TestClassify_ConnectionRefused(t *testing.T) {
	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: "http://127.0.0.1:1/v1/chat/completions", // nothing listening
	}

	d := Classify(context.Background(), cfg, "test prompt")

	if d.Label != LabelGeneral {
		t.Errorf("expected fallback to general on connection refused, got %q", d.Label)
	}
	if !d.Fallback {
		t.Error("expected fallback=true on connection refused")
	}
}

func TestClassify_Disabled(t *testing.T) {
	cfg := RouterConfig{
		Enabled:       false,
		ClassifierURL: "http://localhost:8002/v1/chat/completions",
	}

	d := Classify(context.Background(), cfg, "test prompt")

	if d.Label != LabelGeneral {
		t.Errorf("expected general when disabled, got %q", d.Label)
	}
	if !d.Fallback {
		t.Error("expected fallback=true when disabled")
	}
}

func TestClassify_UnknownLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "I think this is a creative task"}},
			},
		})
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	d := Classify(context.Background(), cfg, "write me a poem")

	if d.Label != LabelGeneral {
		t.Errorf("expected unknown label to map to general, got %q", d.Label)
	}
	if d.Fallback {
		t.Error("expected fallback=false for unknown label (successful classification)")
	}
}

func TestClassify_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	d := Classify(context.Background(), cfg, "test")

	if d.Label != LabelGeneral {
		t.Errorf("expected fallback on malformed json, got %q", d.Label)
	}
	if !d.Fallback {
		t.Error("expected fallback=true on malformed json")
	}
}

func TestClassify_SendsSystemPrompt(t *testing.T) {
	var receivedBody classifierRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "general"}},
			},
		})
	}))
	defer srv.Close()

	cfg := RouterConfig{
		Enabled:       true,
		ClassifierURL: srv.URL,
	}

	Classify(context.Background(), cfg, "hello world")

	if len(receivedBody.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(receivedBody.Messages))
	}
	if receivedBody.Messages[0].Role != "system" {
		t.Errorf("expected system role, got %q", receivedBody.Messages[0].Role)
	}
	if receivedBody.Messages[1].Content != "hello world" {
		t.Errorf("expected user prompt 'hello world', got %q", receivedBody.Messages[1].Content)
	}
	if receivedBody.Stream {
		t.Error("expected stream=false for classifier")
	}
}

func TestParseLabel(t *testing.T) {
	tests := []struct {
		input    string
		expected ClassifierLabel
	}{
		// All 6 intents
		{"code", LabelCode},
		{"reasoning", LabelReasoning},
		{"ops", LabelOps},
		{"search", LabelSearch},
		{"agent", LabelAgent},
		{"general", LabelGeneral},
		// Backward compat
		{"r", LabelReasoning},
		{"R", LabelReasoning},
		{"g", LabelGeneral},
		{"G", LabelGeneral},
		// Case insensitive
		{"Code", LabelCode},
		{"REASONING", LabelReasoning},
		{"OPS", LabelOps},
		// Unknown → general
		{"this requires reasoning", LabelGeneral},
		{"", LabelGeneral},
		{"something else", LabelGeneral},
	}

	for _, tt := range tests {
		got := parseLabel(tt.input)
		if got != tt.expected {
			t.Errorf("parseLabel(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLoadRouterConfig_Defaults(t *testing.T) {
	// Clear env vars.
	t.Setenv("ROUTER_ENABLED", "")
	t.Setenv("AGENTOS_CLASSIFIER_URL", "")

	cfg := LoadRouterConfig()

	if !cfg.Enabled {
		t.Error("expected enabled by default")
	}
	if cfg.ClassifierURL != "http://localhost:8002/v1/chat/completions" {
		t.Errorf("unexpected classifier URL: %q", cfg.ClassifierURL)
	}

}

func TestLoadRouterConfig_Disabled(t *testing.T) {
	t.Setenv("ROUTER_ENABLED", "false")

	cfg := LoadRouterConfig()

	if cfg.Enabled {
		t.Error("expected disabled when ROUTER_ENABLED=false")
	}
}

func TestLoadRouterConfig_CustomURLs(t *testing.T) {
	t.Setenv("ROUTER_ENABLED", "true")
	t.Setenv("AGENTOS_CLASSIFIER_URL", "http://custom:9999/classify")


	cfg := LoadRouterConfig()

	if cfg.ClassifierURL != "http://custom:9999/classify" {
		t.Errorf("expected custom classifier URL, got %q", cfg.ClassifierURL)
	}

}
