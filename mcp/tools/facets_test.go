package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexixAI/nexixai-agentos/agentorchestrator"
	"github.com/NexixAI/nexixai-agentos/mcp"
	"github.com/NexixAI/nexixai-agentos/mcp/facets"
)

// newClassifierServer returns an httptest server that always responds with the
// given label ("R" for reasoning, "G" for general).
func newClassifierServer(t *testing.T, label string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := `{"choices":[{"message":{"content":"` + label + `"}}]}`
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(resp))
		if err != nil {
			t.Errorf("classifier server write error: %v", err)
		}
	}))
}

func TestClassifyPrompt_Reasoning(t *testing.T) {
	srv := newClassifierServer(t, "R")
	defer srv.Close()

	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, srv.URL); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("classify_prompt")
	if tool == nil {
		t.Fatal("classify_prompt tool not found")
	}

	params, _ := json.Marshal(classifyPromptArgs{Prompt: "Prove that sqrt(2) is irrational"})
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(classifyPromptResult)
	if !ok {
		t.Fatalf("expected classifyPromptResult, got %T", result)
	}

	if resp.Facets["task"] != "reasoning" {
		t.Errorf("task = %q, want %q", resp.Facets["task"], "reasoning")
	}
	if resp.Facets["thinking"] != "true" {
		t.Errorf("thinking = %q, want %q", resp.Facets["thinking"], "true")
	}
	if resp.Facets["complexity"] == "" {
		t.Error("complexity should not be empty")
	}
}

func TestClassifyPrompt_Coding(t *testing.T) {
	srv := newClassifierServer(t, "G")
	defer srv.Close()

	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, srv.URL); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("classify_prompt")
	params, _ := json.Marshal(classifyPromptArgs{Prompt: "Write a Python function to sort a list"})
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(classifyPromptResult)
	if !ok {
		t.Fatalf("expected classifyPromptResult, got %T", result)
	}

	if resp.Facets["task"] != "code" {
		t.Errorf("task = %q, want %q", resp.Facets["task"], "code")
	}
	if resp.Facets["thinking"] != "false" {
		t.Errorf("thinking = %q, want %q", resp.Facets["thinking"], "false")
	}
}

func TestClassifyPrompt_ClassifierDown(t *testing.T) {
	// Create and immediately close the server to simulate it being down.
	srv := newClassifierServer(t, "G")
	url := srv.URL
	srv.Close()

	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, url); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("classify_prompt")
	params, _ := json.Marshal(classifyPromptArgs{Prompt: "What is the capital of France?"})
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(classifyPromptResult)
	if !ok {
		t.Fatalf("expected classifyPromptResult, got %T", result)
	}

	// Classifier down → LabelGeneral fallback → thinking=false, task=factual.
	if resp.Facets["thinking"] != "false" {
		t.Errorf("thinking = %q, want %q (classifier down → general fallback)", resp.Facets["thinking"], "false")
	}
	if resp.Facets["task"] != "factual" {
		t.Errorf("task = %q, want %q (classifier down → general fallback, no code keywords)", resp.Facets["task"], "factual")
	}
	if resp.Facets["complexity"] == "" {
		t.Error("complexity should not be empty even with classifier down")
	}
}

func TestClassifyPrompt_EmptyPrompt(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("classify_prompt")
	params, _ := json.Marshal(classifyPromptArgs{Prompt: ""})
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestClassifyPrompt_InvalidJSON(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("classify_prompt")
	_, err := tool.Handler(context.Background(), json.RawMessage(`{bad json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestListFacets(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("list_facets")
	if tool == nil {
		t.Fatal("list_facets tool not found")
	}

	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(listFacetsResult)
	if !ok {
		t.Fatalf("expected listFacetsResult, got %T", result)
	}

	if len(resp.Facets) < 3 {
		t.Fatalf("expected at least 3 default facets, got %d", len(resp.Facets))
	}

	names := make(map[string]bool)
	for _, f := range resp.Facets {
		names[f.Name] = true
		if f.Description == "" {
			t.Errorf("facet %q has empty description", f.Name)
		}
		if len(f.Values) == 0 {
			t.Errorf("facet %q has no suggested values", f.Name)
		}
	}

	for _, want := range []string{"task", "complexity", "thinking"} {
		if !names[want] {
			t.Errorf("default facet %q missing from list_facets response", want)
		}
	}
}

func TestListFacets_ReflectsRegistryChanges(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	// Add a custom facet.
	if err := facetReg.Register(facets.Facet{
		Name:        "language",
		Description: "Natural language of the prompt.",
		Values:      []string{"en", "es"},
		Dynamic:     true,
	}); err != nil {
		t.Fatalf("facetReg.Register() failed: %v", err)
	}

	tool := registry.Get("list_facets")
	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp := result.(listFacetsResult)
	found := false
	for _, f := range resp.Facets {
		if f.Name == "language" {
			found = true
			if !f.Dynamic {
				t.Error("language facet should be dynamic")
			}
			break
		}
	}
	if !found {
		t.Error("dynamically registered facet 'language' not in list_facets output")
	}
}

func TestClearanceTiers(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tests := []struct {
		name          string
		wantClearance mcp.ClearanceTier
	}{
		{"classify_prompt", mcp.ClearanceInternal},
		{"list_facets", mcp.ClearancePublic},
		{"register_facet", mcp.ClearanceSuperAdmin},
		{"unregister_facet", mcp.ClearanceSuperAdmin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := registry.Get(tt.name)
			if tool == nil {
				t.Fatalf("tool %q not found", tt.name)
			}
			if tool.MinClearance != tt.wantClearance {
				t.Errorf("MinClearance = %d, want %d", tool.MinClearance, tt.wantClearance)
			}
		})
	}
}

func TestClassifyPrompt_ToolRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("classify_prompt")
	if tool == nil {
		t.Fatal("classify_prompt tool not found")
	}
	if tool.Name != "classify_prompt" {
		t.Errorf("Name = %q, want %q", tool.Name, "classify_prompt")
	}
	if tool.MinClearance != mcp.ClearanceInternal {
		t.Errorf("MinClearance = %d, want ClearanceInternal (%d)", tool.MinClearance, mcp.ClearanceInternal)
	}
	if !tool.Static {
		t.Error("classify_prompt should be static")
	}
	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("invalid InputSchema JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("InputSchema type = %v, want %q", schema["type"], "object")
	}
}

func TestDuplicateRegistration(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()

	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("first RegisterFacetTools failed: %v", err)
	}
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}
}

func TestListFacets_ResponseSerializesToJSON(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("list_facets")
	result, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result JSON: %v", err)
	}

	if _, ok := parsed["facets"]; !ok {
		t.Error("missing 'facets' key in serialized response")
	}
}

func TestInferComplexity(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "short simple",
			prompt: "Hi",
			want:   "low",
		},
		{
			name:   "medium length",
			prompt: "Can you explain how Docker containers work and how they differ from virtual machines in terms of resource usage?",
			want:   "medium",
		},
		{
			name:   "long complex",
			prompt: strings.Repeat("Please analyze this long prompt with many details. ", 20),
			want:   "high",
		},
		{
			name:   "multiple questions",
			prompt: "What is Go? How does it compare to Rust? What about memory safety? Which one should I learn?",
			want:   "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferComplexity(tt.prompt)
			if got != tt.want {
				t.Errorf("inferComplexity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInferTask(t *testing.T) {
	tests := []struct {
		name   string
		label  string
		prompt string
		want   string
	}{
		{
			name:   "reasoning label",
			label:  "reasoning",
			prompt: "anything",
			want:   "reasoning",
		},
		{
			name:   "code keywords",
			label:  "general",
			prompt: "Write a function to sort",
			want:   "code",
		},
		{
			name:   "creative keywords",
			label:  "general",
			prompt: "Write a story about a cat",
			want:   "creative",
		},
		{
			name:   "analysis keywords",
			label:  "general",
			prompt: "Compare and analyze these two approaches",
			want:   "analysis",
		},
		{
			name:   "factual default",
			label:  "general",
			prompt: "What is the capital of France?",
			want:   "factual",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var label agentorchestrator.ClassifierLabel
			if tt.label == "reasoning" {
				label = agentorchestrator.LabelReasoning
			} else {
				label = agentorchestrator.LabelGeneral
			}
			got := inferTask(label, tt.prompt)
			if got != tt.want {
				t.Errorf("inferTask() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegisterFacet_Success(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("register_facet")
	if tool == nil {
		t.Fatal("register_facet tool not found")
	}

	params, _ := json.Marshal(registerFacetArgs{
		Name:        "language",
		Description: "Natural language of the prompt.",
		Values:      []string{"en", "es", "fr"},
	})
	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(registerFacetResult)
	if !ok {
		t.Fatalf("expected registerFacetResult, got %T", result)
	}
	if resp.Registered != "language" {
		t.Errorf("Registered = %q, want %q", resp.Registered, "language")
	}

	// Verify it actually exists in the registry.
	f := facetReg.Get("language")
	if f == nil {
		t.Fatal("facet 'language' not found in registry after registration")
	}
	if f.Description != "Natural language of the prompt." {
		t.Errorf("description = %q, want %q", f.Description, "Natural language of the prompt.")
	}
	if len(f.Values) != 3 {
		t.Errorf("values count = %d, want 3", len(f.Values))
	}
	if !f.Dynamic {
		t.Error("dynamically registered facet should be marked dynamic")
	}
}

func TestRegisterFacet_MissingFields(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("register_facet")

	tests := []struct {
		name    string
		args    registerFacetArgs
		wantErr string
	}{
		{
			name:    "missing name",
			args:    registerFacetArgs{Name: "", Description: "desc", Values: []string{"a"}},
			wantErr: "name must not be empty",
		},
		{
			name:    "whitespace-only name",
			args:    registerFacetArgs{Name: "   ", Description: "desc", Values: []string{"a"}},
			wantErr: "name must not be empty",
		},
		{
			name:    "missing description",
			args:    registerFacetArgs{Name: "test", Description: "", Values: []string{"a"}},
			wantErr: "description must not be empty",
		},
		{
			name:    "whitespace-only description",
			args:    registerFacetArgs{Name: "test", Description: "  ", Values: []string{"a"}},
			wantErr: "description must not be empty",
		},
		{
			name:    "missing values",
			args:    registerFacetArgs{Name: "test", Description: "desc", Values: nil},
			wantErr: "values must not be empty",
		},
		{
			name:    "empty values slice",
			args:    registerFacetArgs{Name: "test", Description: "desc", Values: []string{}},
			wantErr: "values must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.args)
			_, err := tool.Handler(context.Background(), params)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRegisterFacet_Duplicate(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("register_facet")

	params, _ := json.Marshal(registerFacetArgs{
		Name:        "sentiment",
		Description: "Prompt sentiment.",
		Values:      []string{"positive", "negative", "neutral"},
	})

	// First registration should succeed.
	if _, err := tool.Handler(context.Background(), params); err != nil {
		t.Fatalf("first register failed: %v", err)
	}

	// Second registration of the same name should fail.
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for duplicate facet registration, got nil")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want containing %q", err.Error(), "already registered")
	}
}

func TestUnregisterFacet_Success(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	// Register a facet first, then unregister it.
	regTool := registry.Get("register_facet")
	regParams, _ := json.Marshal(registerFacetArgs{
		Name:        "priority",
		Description: "Request priority level.",
		Values:      []string{"low", "medium", "high"},
	})
	if _, err := regTool.Handler(context.Background(), regParams); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	unregTool := registry.Get("unregister_facet")
	if unregTool == nil {
		t.Fatal("unregister_facet tool not found")
	}

	unregParams, _ := json.Marshal(unregisterFacetArgs{Name: "priority"})
	result, err := unregTool.Handler(context.Background(), unregParams)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	resp, ok := result.(unregisterFacetResult)
	if !ok {
		t.Fatalf("expected unregisterFacetResult, got %T", result)
	}
	if resp.Unregistered != "priority" {
		t.Errorf("Unregistered = %q, want %q", resp.Unregistered, "priority")
	}

	// Verify it's actually gone.
	if f := facetReg.Get("priority"); f != nil {
		t.Error("facet 'priority' still in registry after unregister")
	}
}

func TestUnregisterFacet_NotFound(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	tool := registry.Get("unregister_facet")
	params, _ := json.Marshal(unregisterFacetArgs{Name: "nonexistent"})
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unregistering nonexistent facet, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want containing %q", err.Error(), "not found")
	}
}

func TestRegisterUnregister_ReflectedInListFacets(t *testing.T) {
	registry := mcp.NewToolRegistry()
	facetReg := facets.NewFacetRegistry()
	if err := RegisterFacetTools(registry, facetReg, "http://localhost:9999"); err != nil {
		t.Fatalf("RegisterFacetTools failed: %v", err)
	}

	listTool := registry.Get("list_facets")
	regTool := registry.Get("register_facet")
	unregTool := registry.Get("unregister_facet")

	// Count initial facets.
	initialResult, err := listTool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("list_facets error: %v", err)
	}
	initialCount := len(initialResult.(listFacetsResult).Facets)

	// Register a new facet.
	regParams, _ := json.Marshal(registerFacetArgs{
		Name:        "domain",
		Description: "Knowledge domain.",
		Values:      []string{"science", "engineering", "humanities"},
	})
	if _, err := regTool.Handler(context.Background(), regParams); err != nil {
		t.Fatalf("register_facet error: %v", err)
	}

	// list_facets should now show one more.
	afterRegResult, err := listTool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("list_facets error: %v", err)
	}
	afterRegFacets := afterRegResult.(listFacetsResult).Facets
	if len(afterRegFacets) != initialCount+1 {
		t.Fatalf("expected %d facets after register, got %d", initialCount+1, len(afterRegFacets))
	}

	found := false
	for _, f := range afterRegFacets {
		if f.Name == "domain" {
			found = true
			break
		}
	}
	if !found {
		t.Error("registered facet 'domain' not found in list_facets output")
	}

	// Unregister it.
	unregParams, _ := json.Marshal(unregisterFacetArgs{Name: "domain"})
	if _, err := unregTool.Handler(context.Background(), unregParams); err != nil {
		t.Fatalf("unregister_facet error: %v", err)
	}

	// list_facets should be back to original count.
	afterUnregResult, err := listTool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("list_facets error: %v", err)
	}
	afterUnregFacets := afterUnregResult.(listFacetsResult).Facets
	if len(afterUnregFacets) != initialCount {
		t.Fatalf("expected %d facets after unregister, got %d", initialCount, len(afterUnregFacets))
	}

	for _, f := range afterUnregFacets {
		if f.Name == "domain" {
			t.Error("unregistered facet 'domain' still in list_facets output")
		}
	}
}
