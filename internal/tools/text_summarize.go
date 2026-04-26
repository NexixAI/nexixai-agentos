package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// TextTruncateTool truncates text to a maximum character count.
type TextTruncateTool struct{}

func (t *TextTruncateTool) Name() string { return "text_truncate" }

func (t *TextTruncateTool) Description() string {
	return "Truncate text to a maximum number of characters, appending a truncation marker if needed."
}

// TextSummarizeTool is a deprecated alias for TextTruncateTool.
// Kept for backward compatibility with existing agent configs.
type TextSummarizeTool = TextTruncateTool

func (t *TextTruncateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":      map[string]any{"type": "string", "description": "The text to truncate"},
			"max_chars": map[string]any{"type": "integer", "description": "Maximum number of characters", "default": 1000},
		},
		"required": []string{"text"},
	}
}

type textSummarizeInput struct {
	Text     string `json:"text"`
	MaxChars int    `json:"max_chars"`
}

func (t *TextTruncateTool) Execute(_ context.Context, input string) (string, error) {
	var in textSummarizeInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if in.MaxChars <= 0 {
		in.MaxChars = 1000
	}

	if len(in.Text) <= in.MaxChars {
		return in.Text, nil
	}

	return in.Text[:in.MaxChars] + "... [truncated]", nil
}
