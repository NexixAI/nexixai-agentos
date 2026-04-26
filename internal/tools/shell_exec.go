package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/shlex"
)

const (
	shellMaxOutput      = 64 * 1024 // 64KB max output
	shellDefaultTimeout = 30 * time.Second
)

// defaultDeniedCommands lists commands that are never allowed.
var defaultDeniedCommands = map[string]bool{
	"rm": true, "rmdir": true, "mkfs": true, "dd": true,
	"shutdown": true, "reboot": true, "halt": true, "poweroff": true,
	"chmod": true, "chown": true, "mount": true, "umount": true,
	"kill": true, "killall": true, "pkill": true,
}

// shellExecAllowedCommands constrains shell_exec to read-only and diagnostic
// binaries. Stateful or interpreter-style commands are intentionally excluded.
var shellExecAllowedCommands = map[string]bool{
	"basename": true,
	"cat":      true,
	"cut":      true,
	"date":     true,
	"df":       true,
	"dirname":  true,
	"du":       true,
	"echo":     true,
	"false":    true,
	"file":     true,
	"free":     true,
	"grep":     true,
	"head":     true,
	"id":       true,
	"ls":       true,
	"printf":   true,
	"ps":       true,
	"pwd":      true,
	"readlink": true,
	"realpath": true,
	"rg":       true,
	"sort":     true,
	"ss":       true,
	"stat":     true,
	"tail":     true,
	"tr":       true,
	"true":     true,
	"uname":    true,
	"uniq":     true,
	"uptime":   true,
	"wc":       true,
	"whereis":  true,
	"which":    true,
	"whoami":   true,
}

type ShellExecTool struct{}

func (t *ShellExecTool) Name() string { return "shell_exec" }
func (t *ShellExecTool) Description() string {
	return "Execute a limited diagnostic command and return its stdout/stderr. Shell interpretation and destructive commands are blocked."
}
func (t *ShellExecTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":         map[string]any{"type": "string", "description": "A command plus arguments. Shell operators and interpreters are not supported."},
			"timeout_seconds": map[string]any{"type": "integer", "description": "Timeout in seconds (default 30, max 120)"},
		},
		"required": []string{"command"},
	}
}

type shellExecInput struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type shellExecOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func (t *ShellExecTool) Execute(ctx context.Context, input string) (string, error) {
	var in shellExecInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	parts, err := shlex.Split(in.Command)
	if err != nil {
		return "", fmt.Errorf("invalid command: %w", err)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}
	baseName := filepath.Base(parts[0])
	if defaultDeniedCommands[baseName] {
		return "", fmt.Errorf("command %q is not allowed", baseName)
	}
	if !shellExecAllowedCommands[baseName] {
		return "", fmt.Errorf("command %q is not allowed", baseName)
	}

	timeout := shellDefaultTimeout
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
		if timeout > 120*time.Second {
			timeout = 120 * time.Second
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after %v", timeout)
		} else {
			return "", fmt.Errorf("command failed: %w", err)
		}
	}

	out := shellExecOutput{
		ExitCode: exitCode,
		Stdout:   truncateStr(stdout.String(), shellMaxOutput),
		Stderr:   truncateStr(stderr.String(), shellMaxOutput),
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("error encoding output: %w", err)
	}
	return string(data), nil
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "... [truncated]"
}
