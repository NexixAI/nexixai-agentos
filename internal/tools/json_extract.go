package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// JSONExtractTool extracts values from JSON using dot-path notation.
type JSONExtractTool struct{}

func (t *JSONExtractTool) Name() string { return "json_extract" }

func (t *JSONExtractTool) Description() string {
	return "Extract a value from a JSON string using a dot-separated path (e.g. 'field.subfield.0')."
}

func (t *JSONExtractTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"json": map[string]any{"type": "string", "description": "The JSON string to extract from"},
			"path": map[string]any{"type": "string", "description": "Dot-separated path (e.g. 'data.items.0.name')"},
		},
		"required": []string{"json", "path"},
	}
}

type jsonExtractInput struct {
	JSON string `json:"json"`
	Path string `json:"path"`
}

func (t *JSONExtractTool) Execute(_ context.Context, input string) (string, error) {
	var in jsonExtractInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if in.JSON == "" {
		return "", fmt.Errorf("json is required")
	}
	if in.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	var data any
	if err := json.Unmarshal([]byte(in.JSON), &data); err != nil {
		return "", fmt.Errorf("invalid json: %w", err)
	}

	parts := strings.Split(in.Path, ".")
	current := data

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return "", fmt.Errorf("path not found: key %q does not exist", part)
			}
			current = val
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil {
				return "", fmt.Errorf("path not found: %q is not a valid array index", part)
			}
			if idx < 0 || idx >= len(v) {
				return "", fmt.Errorf("path not found: index %d out of range (length %d)", idx, len(v))
			}
			current = v[idx]
		default:
			return "", fmt.Errorf("path not found: cannot traverse into %T at %q", current, part)
		}
	}

	result, err := json.Marshal(current)
	if err != nil {
		return "", fmt.Errorf("error encoding result: %w", err)
	}
	return string(result), nil
}
