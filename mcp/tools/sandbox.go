package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/sandbox"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- execute_code tool ---
// --- execute_shell tool ---

const (
	defaultTimeoutSeconds = 30
	maxTimeoutSeconds     = 300
)

type executeCodeInput struct {
	Language       string `json:"language"`
	Code           string `json:"code"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
}

type executeCodeOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	DurationMs int64  `json:"duration_ms"`
}

var executeCodeSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"language": {
			"type": "string",
			"description": "The programming language to execute. Supported: python, javascript, go, bash.",
			"enum": ["python", "javascript", "go", "bash"]
		},
		"code": {
			"type": "string",
			"description": "The source code to execute."
		},
		"timeout_seconds": {
			"type": "integer",
			"description": "Maximum execution time in seconds (default 30, max 300).",
			"minimum": 1,
			"maximum": 300
		}
	},
	"required": ["language", "code"],
	"additionalProperties": false
}`)

// --- execute_shell types and schema ---

type executeShellInput struct {
	Command        string `json:"command"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
}

type executeShellOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	DurationMs int64  `json:"duration_ms"`
}

var executeShellSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "The shell command to execute in a sandboxed bash container."
		},
		"timeout_seconds": {
			"type": "integer",
			"description": "Maximum execution time in seconds (default 30, max 300).",
			"minimum": 1,
			"maximum": 300
		}
	},
	"required": ["command"],
	"additionalProperties": false
}`)

// RegisterSandboxTools registers the execute_code and execute_shell tools with the given registry.
func RegisterSandboxTools(registry *mcp.ToolRegistry, executor sandbox.Executor) error {
	if err := registry.Register(mcp.Tool{
		Name:         "execute_code",
		Description:  "Execute code in an ephemeral sandboxed Docker container with no network access.",
		InputSchema:  executeCodeSchema,
		MinClearance: mcp.ClearanceExecute,
		Static:       true,
		Handler:      makeExecuteCodeHandler(executor),
	}); err != nil {
		return err
	}

	return registry.Register(mcp.Tool{
		Name:         "execute_shell",
		Description:  "Execute a shell command in an ephemeral sandboxed Docker container with no network access.",
		InputSchema:  executeShellSchema,
		MinClearance: mcp.ClearanceAdmin,
		Static:       true,
		Handler:      makeExecuteShellHandler(executor),
	})
}

// makeExecuteCodeHandler returns a ToolHandler that runs code via the sandbox executor.
func makeExecuteCodeHandler(executor sandbox.Executor) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input executeCodeInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.Language == "" {
			return nil, fmt.Errorf("language is required")
		}
		if input.Code == "" {
			return nil, fmt.Errorf("code is required")
		}

		timeoutSec := defaultTimeoutSeconds
		if input.TimeoutSeconds != nil {
			timeoutSec = *input.TimeoutSeconds
			if timeoutSec < 1 {
				return nil, fmt.Errorf("timeout_seconds must be at least 1")
			}
			if timeoutSec > maxTimeoutSeconds {
				return nil, fmt.Errorf("timeout_seconds must not exceed %d", maxTimeoutSeconds)
			}
		}

		timeout := time.Duration(timeoutSec) * time.Second

		slog.Info("sandbox: executing code",
			"language", input.Language,
			"timeout_seconds", timeoutSec,
			"code_length", len(input.Code),
		)

		result, err := executor.Execute(ctx, input.Language, input.Code, timeout)
		if err != nil {
			return nil, fmt.Errorf("execution failed: %w", err)
		}

		slog.Info("sandbox: execution complete",
			"language", input.Language,
			"exit_code", result.ExitCode,
			"timed_out", result.TimedOut,
			"duration_ms", result.Duration.Milliseconds(),
		)

		return executeCodeOutput{
			Stdout:     result.Stdout,
			Stderr:     result.Stderr,
			ExitCode:   result.ExitCode,
			TimedOut:   result.TimedOut,
			DurationMs: result.Duration.Milliseconds(),
		}, nil
	}
}

// makeExecuteShellHandler returns a ToolHandler that runs a shell command via the sandbox executor.
func makeExecuteShellHandler(executor sandbox.Executor) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input executeShellInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.Command == "" {
			return nil, fmt.Errorf("command is required")
		}

		timeoutSec := defaultTimeoutSeconds
		if input.TimeoutSeconds != nil {
			timeoutSec = *input.TimeoutSeconds
			if timeoutSec < 1 {
				return nil, fmt.Errorf("timeout_seconds must be at least 1")
			}
			if timeoutSec > maxTimeoutSeconds {
				return nil, fmt.Errorf("timeout_seconds must not exceed %d", maxTimeoutSeconds)
			}
		}

		timeout := time.Duration(timeoutSec) * time.Second

		slog.Info("sandbox: executing shell command",
			"timeout_seconds", timeoutSec,
			"command_length", len(input.Command),
		)

		result, err := executor.ExecuteShell(ctx, input.Command, timeout)
		if err != nil {
			return nil, fmt.Errorf("execution failed: %w", err)
		}

		slog.Info("sandbox: shell execution complete",
			"exit_code", result.ExitCode,
			"timed_out", result.TimedOut,
			"duration_ms", result.Duration.Milliseconds(),
		)

		return executeShellOutput{
			Stdout:     result.Stdout,
			Stderr:     result.Stderr,
			ExitCode:   result.ExitCode,
			TimedOut:   result.TimedOut,
			DurationMs: result.Duration.Milliseconds(),
		}, nil
	}
}
