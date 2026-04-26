package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/NexixAI/nexixai-agentos/agentorchestrator"
	"github.com/NexixAI/nexixai-agentos/mcp"
	"github.com/NexixAI/nexixai-agentos/mcp/facets"
)

// classifyPromptArgs are the arguments for classify_prompt.
type classifyPromptArgs struct {
	Prompt string `json:"prompt"`
}

// classifyPromptResult is the structured result of classify_prompt.
type classifyPromptResult struct {
	Facets map[string]string `json:"facets"`
}

// listFacetsEntry is a single facet in the list_facets response.
type listFacetsEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Values      []string `json:"values"`
	Dynamic     bool     `json:"dynamic"`
}

// listFacetsResult is the structured result of list_facets.
type listFacetsResult struct {
	Facets []listFacetsEntry `json:"facets"`
}

// classifyPromptSchema is the JSON Schema for classify_prompt.
var classifyPromptSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "prompt": {
      "type": "string",
      "description": "The user prompt to classify across facet dimensions."
    }
  },
  "required": ["prompt"]
}`)

// registerFacetArgs are the arguments for register_facet.
type registerFacetArgs struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Values      []string `json:"values"`
}

// unregisterFacetArgs are the arguments for unregister_facet.
type unregisterFacetArgs struct {
	Name string `json:"name"`
}

// registerFacetResult is the structured result of register_facet.
type registerFacetResult struct {
	Registered string `json:"registered"`
}

// unregisterFacetResult is the structured result of unregister_facet.
type unregisterFacetResult struct {
	Unregistered string `json:"unregistered"`
}

// listFacetsSchema is the JSON Schema for list_facets.
var listFacetsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`)

// registerFacetSchema is the JSON Schema for register_facet.
var registerFacetSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "Unique name for the facet dimension."
    },
    "description": {
      "type": "string",
      "description": "Human-readable description of the facet."
    },
    "values": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Suggested values for the facet dimension."
    }
  },
  "required": ["name", "description", "values"]
}`)

// unregisterFacetSchema is the JSON Schema for unregister_facet.
var unregisterFacetSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "Name of the facet to remove."
    }
  },
  "required": ["name"]
}`)

// RegisterFacetTools registers classify_prompt, list_facets, register_facet,
// and unregister_facet on the given MCP tool registry. facetReg is the
// FacetRegistry to query and mutate. classifierURL is the URL for the binary
// prompt classifier endpoint.
func RegisterFacetTools(registry *mcp.ToolRegistry, facetReg *facets.FacetRegistry, classifierURL string) error {
	tools := []mcp.Tool{
		{
			Name:         "classify_prompt",
			Description:  "Classify a prompt across registered facet dimensions (task, complexity, thinking).",
			InputSchema:  classifyPromptSchema,
			Handler:      classifyPromptHandler(facetReg, classifierURL),
			MinClearance: mcp.ClearanceInternal, // T1
			Static:       true,
		},
		{
			Name:         "list_facets",
			Description:  "List all registered facet dimensions with descriptions and suggested values.",
			InputSchema:  listFacetsSchema,
			Handler:      listFacetsHandler(facetReg),
			MinClearance: mcp.ClearancePublic, // T0
			Static:       false,               // Dynamic — reflects current registry state.
		},
		{
			Name:         "register_facet",
			Description:  "Register a new facet dimension in the facet registry.",
			InputSchema:  registerFacetSchema,
			Handler:      registerFacetHandler(facetReg),
			MinClearance: mcp.ClearanceSuperAdmin, // T4
			Static:       false,
		},
		{
			Name:         "unregister_facet",
			Description:  "Remove a facet dimension from the facet registry.",
			InputSchema:  unregisterFacetSchema,
			Handler:      unregisterFacetHandler(facetReg),
			MinClearance: mcp.ClearanceSuperAdmin, // T4
			Static:       false,
		},
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return fmt.Errorf("register %s: %w", t.Name, err)
		}
	}
	return nil
}

// classifyPromptHandler returns a ToolHandler that classifies a prompt across
// facet dimensions by calling the existing binary classifier and applying
// keyword heuristics.
func classifyPromptHandler(facetReg *facets.FacetRegistry, classifierURL string) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var args classifyPromptArgs
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.Prompt) == "" {
			return nil, fmt.Errorf("prompt must not be empty")
		}

		// Call the existing binary classifier.
		cfg := agentorchestrator.RouterConfig{
			Enabled:       classifierURL != "",
			ClassifierURL: classifierURL,
		}
		decision := agentorchestrator.Classify(ctx, cfg, args.Prompt)

		if decision.Fallback {
			slog.Warn("classify_prompt: classifier fallback, using defaults",
				"error", decision.Error,
			)
		}

		result := classifyPromptResult{
			Facets: map[string]string{
				"task":       inferTask(decision.Label, args.Prompt),
				"complexity": inferComplexity(args.Prompt),
				"thinking":   inferThinking(decision.Label),
			},
		}

		slog.Info("classify_prompt",
			"label", decision.Label,
			"task", result.Facets["task"],
			"complexity", result.Facets["complexity"],
			"thinking", result.Facets["thinking"],
		)

		return result, nil
	}
}

// inferTask maps the classifier label + keyword heuristics to a task facet.
func inferTask(label agentorchestrator.ClassifierLabel, prompt string) string {
	lower := strings.ToLower(prompt)

	// Classifier said reasoning — that's authoritative.
	if label == agentorchestrator.LabelReasoning {
		return "reasoning"
	}

	// Keyword heuristics for the "general" label.
	// Words in this list are checked via containsWord to avoid false positives
	// (e.g. "api" matching inside "capital").
	codeKeywords := []string{
		"func", "function", "def", "class", "import",
		"code", "implement", "refactor", "debug", "compile",
		"program", "script", "api", "endpoint", "http",
		"package", "module", "library",
	}
	for _, kw := range codeKeywords {
		if containsWord(lower, kw) {
			return "code"
		}
	}

	creativeKeywords := []string{
		"write a story", "write a poem", "creative", "imagine",
		"fiction", "compose", "brainstorm",
	}
	for _, kw := range creativeKeywords {
		if strings.Contains(lower, kw) {
			return "creative"
		}
	}

	analysisKeywords := []string{
		"analyze", "analyse", "compare", "evaluate", "assess",
		"trade-off", "tradeoff", "pros and cons", "advantages",
	}
	for _, kw := range analysisKeywords {
		if strings.Contains(lower, kw) {
			return "analysis"
		}
	}

	// Default: factual.
	return "factual"
}

// inferComplexity estimates complexity from prompt length and structure.
func inferComplexity(prompt string) string {
	length := len(prompt)
	questionMarks := strings.Count(prompt, "?")
	newlines := strings.Count(prompt, "\n")

	// Multiple questions or structured multi-line input → high.
	if questionMarks >= 3 || newlines >= 5 || length > 500 {
		return "high"
	}

	// Moderate length or more than one question → medium.
	if length > 100 || questionMarks >= 2 || newlines >= 2 {
		return "medium"
	}

	return "low"
}

// inferThinking maps the classifier label to a thinking recommendation.
func inferThinking(label agentorchestrator.ClassifierLabel) string {
	if label == agentorchestrator.LabelReasoning {
		return "true"
	}
	return "false"
}

// containsWord checks whether word appears in text as a whole word (bounded by
// spaces, punctuation, or string boundaries). This avoids false positives like
// "api" matching inside "capital".
func containsWord(text, word string) bool {
	idx := 0
	for {
		i := strings.Index(text[idx:], word)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(word)

		leftOK := start == 0 || !isWordChar(text[start-1])
		rightOK := end == len(text) || !isWordChar(text[end])

		if leftOK && rightOK {
			return true
		}
		idx = start + 1
	}
}

// isWordChar returns true for characters that are part of a "word" (letters,
// digits, underscore). Used by containsWord for boundary detection.
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// registerFacetHandler returns a ToolHandler that registers a new facet
// dimension in the FacetRegistry.
func registerFacetHandler(facetReg *facets.FacetRegistry) mcp.ToolHandler {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var args registerFacetArgs
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.Name) == "" {
			return nil, fmt.Errorf("name must not be empty")
		}
		if strings.TrimSpace(args.Description) == "" {
			return nil, fmt.Errorf("description must not be empty")
		}
		if len(args.Values) == 0 {
			return nil, fmt.Errorf("values must not be empty")
		}

		f := facets.Facet{
			Name:        args.Name,
			Description: args.Description,
			Values:      args.Values,
			Dynamic:     true,
		}
		if err := facetReg.Register(f); err != nil {
			return nil, err
		}

		slog.Info("register_facet", "facet", args.Name)
		return registerFacetResult{Registered: args.Name}, nil
	}
}

// unregisterFacetHandler returns a ToolHandler that removes a facet dimension
// from the FacetRegistry.
func unregisterFacetHandler(facetReg *facets.FacetRegistry) mcp.ToolHandler {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var args unregisterFacetArgs
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.Name) == "" {
			return nil, fmt.Errorf("name must not be empty")
		}

		if err := facetReg.Unregister(args.Name); err != nil {
			return nil, err
		}

		slog.Info("unregister_facet", "facet", args.Name)
		return unregisterFacetResult{Unregistered: args.Name}, nil
	}
}

// listFacetsHandler returns a ToolHandler that lists all registered facets.
func listFacetsHandler(facetReg *facets.FacetRegistry) mcp.ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		all := facetReg.List()

		entries := make([]listFacetsEntry, len(all))
		for i, f := range all {
			entries[i] = listFacetsEntry{
				Name:        f.Name,
				Description: f.Description,
				Values:      f.Values,
				Dynamic:     f.Dynamic,
			}
		}

		return listFacetsResult{Facets: entries}, nil
	}
}
