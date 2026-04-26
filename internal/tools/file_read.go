package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const fileReadMaxSize = 1024 * 1024 // 1MB

// allowedBaseDir returns the directory file tools can access.
// Can be overridden via AGENTOS_TOOL_FILE_BASE_DIR env var.
func allowedBaseDir() string {
	if dir := os.Getenv("AGENTOS_TOOL_FILE_BASE_DIR"); dir != "" {
		return dir
	}
	return "/tmp/agentos-sandbox"
}

func safePath(requestedPath string, requireExists bool) (string, error) {
	base := allowedBaseDir()
	// Ensure base dir exists.
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("cannot create base directory: %w", err)
	}

	// Resolve the base directory itself through any symlinks so that
	// the prefix check is always performed against the canonical base.
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("cannot resolve base directory: %w", err)
	}

	// Clean and resolve the path.
	cleaned := filepath.Clean(requestedPath)
	var abs string
	if filepath.IsAbs(cleaned) {
		abs = cleaned
	} else {
		abs = filepath.Join(base, cleaned)
	}

	// Verify the cleaned path is under the base dir (pre-symlink check).
	if !strings.HasPrefix(abs, base) {
		return "", fmt.Errorf("path %q is outside allowed directory %q", requestedPath, base)
	}

	// Resolve symlinks to get the real path and re-verify the prefix.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if requireExists {
			// For reads, the file must exist — EvalSymlinks failing means
			// either the file doesn't exist or there's a broken symlink.
			return "", fmt.Errorf("cannot resolve path: %w", err)
		}
		// For writes, the file may not exist yet. Resolve the parent
		// directory instead and re-check.
		parentResolved, parentErr := filepath.EvalSymlinks(filepath.Dir(abs))
		if parentErr != nil {
			// Parent doesn't exist either — the write tool will create
			// it, but we still need to verify the cleaned path is safe.
			// The pre-symlink prefix check above already passed, so
			// this is acceptable for a non-existent parent.
			return abs, nil
		}
		resolvedViaParent := filepath.Join(parentResolved, filepath.Base(abs))
		if !strings.HasPrefix(resolvedViaParent, realBase) {
			return "", fmt.Errorf("path %q resolves outside allowed directory via symlink", requestedPath)
		}
		return resolvedViaParent, nil
	}

	if !strings.HasPrefix(resolved, realBase) {
		return "", fmt.Errorf("path %q resolves outside allowed directory via symlink", requestedPath)
	}
	return resolved, nil
}

type FileReadTool struct{}

func (t *FileReadTool) Name() string { return "file_read" }
func (t *FileReadTool) Description() string {
	return "Read a file from the sandbox directory. Returns text content or base64-encoded binary. Limited to 1MB."
}
func (t *FileReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":     map[string]any{"type": "string", "description": "File path (relative to sandbox or absolute within sandbox)"},
			"encoding": map[string]any{"type": "string", "description": "Output encoding: 'text' (default) or 'base64'", "default": "text"},
		},
		"required": []string{"path"},
	}
}

type fileReadInput struct {
	Path     string `json:"path"`
	Encoding string `json:"encoding"`
}

func (t *FileReadTool) Execute(ctx context.Context, input string) (string, error) {
	var in fileReadInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if in.Encoding == "" {
		in.Encoding = "text"
	}

	abs, err := safePath(in.Path, true)
	if err != nil {
		return "", err
	}

	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, fileReadMaxSize+1))
	if err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}
	if len(data) > fileReadMaxSize {
		data = data[:fileReadMaxSize]
	}

	var content string
	if in.Encoding == "base64" {
		content = base64.StdEncoding.EncodeToString(data)
	} else {
		content = string(data)
	}

	out := map[string]any{
		"path":    in.Path,
		"size":    len(data),
		"content": content,
	}
	result, _ := json.Marshal(out) //nolint:errcheck // marshal of known struct
	return string(result), nil
}
