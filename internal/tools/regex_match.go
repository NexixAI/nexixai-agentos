package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

const regexMaxMatches = 100

type RegexMatchTool struct{}

func (t *RegexMatchTool) Name() string { return "regex_match" }
func (t *RegexMatchTool) Description() string {
	return "Apply a regular expression pattern to input text. Returns all matches with groups and positions."
}
func (t *RegexMatchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":     map[string]any{"type": "string", "description": "Regular expression pattern (Go/RE2 syntax)"},
			"text":        map[string]any{"type": "string", "description": "Text to search"},
			"max_matches": map[string]any{"type": "integer", "description": "Maximum matches to return (default 100)"},
		},
		"required": []string{"pattern", "text"},
	}
}

type regexMatchInput struct {
	Pattern    string `json:"pattern"`
	Text       string `json:"text"`
	MaxMatches int    `json:"max_matches"`
}

type regexMatchResult struct {
	FullMatch string   `json:"full_match"`
	Groups    []string `json:"groups,omitempty"`
	Start     int      `json:"start"`
	End       int      `json:"end"`
}

func (t *RegexMatchTool) Execute(ctx context.Context, input string) (string, error) {
	var in regexMatchInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if in.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if in.MaxMatches <= 0 {
		in.MaxMatches = regexMaxMatches
	}

	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %w", err)
	}

	allIndices := re.FindAllStringSubmatchIndex(in.Text, in.MaxMatches)
	names := re.SubexpNames()

	var results []regexMatchResult
	for _, idx := range allIndices {
		if len(idx) < 2 {
			continue
		}
		match := regexMatchResult{
			FullMatch: in.Text[idx[0]:idx[1]],
			Start:     idx[0],
			End:       idx[1],
		}
		// Capture groups (skip full match at index 0).
		for i := 1; i < len(names) && i*2+1 < len(idx); i++ {
			if idx[i*2] >= 0 {
				match.Groups = append(match.Groups, in.Text[idx[i*2]:idx[i*2+1]])
			} else {
				match.Groups = append(match.Groups, "")
			}
		}
		results = append(results, match)
	}

	out := map[string]any{
		"pattern":     in.Pattern,
		"match_count": len(results),
		"matches":     results,
	}
	data, _ := json.Marshal(out) //nolint:errcheck // marshal of known struct
	return string(data), nil
}
