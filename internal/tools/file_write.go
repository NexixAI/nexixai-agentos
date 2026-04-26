package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const fileWriteMaxSize = 1024 * 1024 // 1MB

type FileWriteTool struct{}

func (t *FileWriteTool) Name() string { return "file_write" }
func (t *FileWriteTool) Description() string {
	return "Write content to a file in the sandbox directory. Supports overwrite and append modes. Limited to 1MB."
}
func (t *FileWriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path (relative to sandbox or absolute within sandbox)"},
			"content": map[string]any{"type": "string", "description": "Content to write"},
			"mode":    map[string]any{"type": "string", "description": "Write mode: 'overwrite' (default) or 'append'", "default": "overwrite"},
		},
		"required": []string{"path", "content"},
	}
}

type fileWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode"`
}

func (t *FileWriteTool) Execute(ctx context.Context, input string) (string, error) {
	var in fileWriteInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if len(in.Content) > fileWriteMaxSize {
		return "", fmt.Errorf("content exceeds maximum size of %d bytes", fileWriteMaxSize)
	}
	if in.Mode == "" {
		in.Mode = "overwrite"
	}

	abs, err := safePath(in.Path, false)
	if err != nil {
		return "", err
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("cannot create directory: %w", err)
	}

	var flag int
	switch in.Mode {
	case "append":
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	default: // "overwrite"
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}

	f, err := os.OpenFile(abs, flag, 0o644)
	if err != nil {
		return "", fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(in.Content)
	if err != nil {
		return "", fmt.Errorf("write error: %w", err)
	}

	out := map[string]any{
		"path":          in.Path,
		"bytes_written": n,
		"mode":          in.Mode,
	}
	result, _ := json.Marshal(out) //nolint:errcheck // marshal of known struct
	return string(result), nil
}
