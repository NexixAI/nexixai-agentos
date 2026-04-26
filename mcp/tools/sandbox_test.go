package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/sandbox"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- mock sandbox executor ---

type mockExecutor struct {
	result *sandbox.ExecResult
	err    error
	// Captured inputs for assertions.
	lastLanguage string
	lastCode     string
	lastTimeout  time.Duration
	// Captured inputs for ExecuteShell.
	lastCommand      string
	lastShellTimeout time.Duration
	shellResult      *sandbox.ExecResult
	shellErr         error
}

func (m *mockExecutor) Execute(_ context.Context, language, code string, timeout time.Duration) (*sandbox.ExecResult, error) {
	m.lastLanguage = language
	m.lastCode = code
	m.lastTimeout = timeout
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func (m *mockExecutor) ExecuteShell(_ context.Context, command string, timeout time.Duration) (*sandbox.ExecResult, error) {
	m.lastCommand = command
	m.lastShellTimeout = timeout
	if m.shellErr != nil {
		return nil, m.shellErr
	}
	if m.shellResult != nil {
		return m.shellResult, nil
	}
	// Fall back to the shared result if shellResult not set.
	if m.result != nil {
		return m.result, nil
	}
	return nil, m.err
}

func TestExecuteCode_Success(t *testing.T) {
	executor := &mockExecutor{
		result: &sandbox.ExecResult{
			Stdout:   "hello world\n",
			Stderr:   "",
			ExitCode: 0,
			TimedOut: false,
			Duration: 150 * time.Millisecond,
		},
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	if tool == nil {
		t.Fatal("execute_code not registered")
	}

	params, _ := json.Marshal(map[string]any{
		"language": "python",
		"code":     "print('hello world')",
	})

	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(executeCodeOutput)
	if !ok {
		t.Fatalf("expected executeCodeOutput, got %T", result)
	}
	if out.Stdout != "hello world\n" {
		t.Errorf("stdout = %q, want %q", out.Stdout, "hello world\n")
	}
	if out.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", out.ExitCode)
	}
	if out.TimedOut {
		t.Error("expected timed_out=false")
	}
	if out.DurationMs != 150 {
		t.Errorf("duration_ms = %d, want 150", out.DurationMs)
	}

	// Verify executor received correct inputs.
	if executor.lastLanguage != "python" {
		t.Errorf("executor.language = %q, want %q", executor.lastLanguage, "python")
	}
	if executor.lastCode != "print('hello world')" {
		t.Errorf("executor.code = %q, want %q", executor.lastCode, "print('hello world')")
	}
	if executor.lastTimeout != 30*time.Second {
		t.Errorf("executor.timeout = %v, want %v", executor.lastTimeout, 30*time.Second)
	}
}

func TestExecuteCode_MissingLanguage(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	params, _ := json.Marshal(map[string]any{
		"code": "print('hello')",
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing language, got nil")
	}
}

func TestExecuteCode_MissingCode(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	params, _ := json.Marshal(map[string]any{
		"language": "python",
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing code, got nil")
	}
}

func TestExecuteCode_TimeoutTooLow(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	params, _ := json.Marshal(map[string]any{
		"language":        "python",
		"code":            "print('hello')",
		"timeout_seconds": 0,
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for timeout_seconds=0, got nil")
	}
}

func TestExecuteCode_TimeoutTooHigh(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	params, _ := json.Marshal(map[string]any{
		"language":        "python",
		"code":            "print('hello')",
		"timeout_seconds": 301,
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for timeout_seconds=301, got nil")
	}
}

func TestExecuteCode_CustomTimeout(t *testing.T) {
	executor := &mockExecutor{
		result: &sandbox.ExecResult{
			Stdout:   "ok",
			Duration: 100 * time.Millisecond,
		},
	}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	params, _ := json.Marshal(map[string]any{
		"language":        "bash",
		"code":            "echo ok",
		"timeout_seconds": 60,
	})

	_, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if executor.lastTimeout != 60*time.Second {
		t.Errorf("executor.timeout = %v, want %v", executor.lastTimeout, 60*time.Second)
	}
}

func TestExecuteCode_ExecutorError(t *testing.T) {
	executor := &mockExecutor{
		err: errors.New("docker not available"),
	}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	params, _ := json.Marshal(map[string]any{
		"language": "python",
		"code":     "print('hello')",
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error from executor, got nil")
	}
}

func TestExecuteCode_ClearanceTier(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	if tool == nil {
		t.Fatal("execute_code not registered")
	}
	if tool.MinClearance != mcp.ClearanceExecute {
		t.Errorf("MinClearance = %d, want %d (ClearanceExecute)", tool.MinClearance, mcp.ClearanceExecute)
	}
}

func TestExecuteCode_TimedOutResult(t *testing.T) {
	executor := &mockExecutor{
		result: &sandbox.ExecResult{
			Stdout:   "",
			Stderr:   "",
			ExitCode: -1,
			TimedOut: true,
			Duration: 30 * time.Second,
		},
	}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	params, _ := json.Marshal(map[string]any{
		"language": "python",
		"code":     "import time; time.sleep(60)",
	})

	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(executeCodeOutput)
	if !ok {
		t.Fatalf("expected executeCodeOutput, got %T", result)
	}
	if !out.TimedOut {
		t.Error("expected timed_out=true")
	}
	if out.ExitCode != -1 {
		t.Errorf("exit_code = %d, want -1", out.ExitCode)
	}
}

func TestExecuteCode_InvalidJSON(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_code")
	_, err := tool.Handler(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// --- execute_shell tests ---

func TestExecuteShell_Success(t *testing.T) {
	executor := &mockExecutor{
		shellResult: &sandbox.ExecResult{
			Stdout:   "file1.txt\nfile2.txt\n",
			Stderr:   "",
			ExitCode: 0,
			TimedOut: false,
			Duration: 200 * time.Millisecond,
		},
	}

	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_shell")
	if tool == nil {
		t.Fatal("execute_shell not registered")
	}

	params, _ := json.Marshal(map[string]any{
		"command": "ls -la /tmp",
	})

	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(executeShellOutput)
	if !ok {
		t.Fatalf("expected executeShellOutput, got %T", result)
	}
	if out.Stdout != "file1.txt\nfile2.txt\n" {
		t.Errorf("stdout = %q, want %q", out.Stdout, "file1.txt\nfile2.txt\n")
	}
	if out.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", out.ExitCode)
	}
	if out.TimedOut {
		t.Error("expected timed_out=false")
	}
	if out.DurationMs != 200 {
		t.Errorf("duration_ms = %d, want 200", out.DurationMs)
	}

	// Verify executor received correct inputs.
	if executor.lastCommand != "ls -la /tmp" {
		t.Errorf("executor.command = %q, want %q", executor.lastCommand, "ls -la /tmp")
	}
	if executor.lastShellTimeout != 30*time.Second {
		t.Errorf("executor.timeout = %v, want %v", executor.lastShellTimeout, 30*time.Second)
	}
}

func TestExecuteShell_MissingCommand(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_shell")
	params, _ := json.Marshal(map[string]any{})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing command, got nil")
	}
}

func TestExecuteShell_TimeoutValidation(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_shell")

	// timeout_seconds = 0 should fail
	params, _ := json.Marshal(map[string]any{
		"command":         "echo hello",
		"timeout_seconds": 0,
	})
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for timeout_seconds=0, got nil")
	}

	// timeout_seconds = 301 should fail
	params, _ = json.Marshal(map[string]any{
		"command":         "echo hello",
		"timeout_seconds": 301,
	})
	_, err = tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for timeout_seconds=301, got nil")
	}
}

func TestExecuteShell_ClearanceTier(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_shell")
	if tool == nil {
		t.Fatal("execute_shell not registered")
	}
	if tool.MinClearance != mcp.ClearanceAdmin {
		t.Errorf("MinClearance = %d, want %d (ClearanceAdmin)", tool.MinClearance, mcp.ClearanceAdmin)
	}
}

func TestExecuteShell_CustomTimeout(t *testing.T) {
	executor := &mockExecutor{
		shellResult: &sandbox.ExecResult{
			Stdout:   "ok",
			Duration: 50 * time.Millisecond,
		},
	}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_shell")
	params, _ := json.Marshal(map[string]any{
		"command":         "echo ok",
		"timeout_seconds": 120,
	})

	_, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if executor.lastShellTimeout != 120*time.Second {
		t.Errorf("executor.timeout = %v, want %v", executor.lastShellTimeout, 120*time.Second)
	}
}

func TestExecuteShell_ExecutorError(t *testing.T) {
	executor := &mockExecutor{
		shellErr: errors.New("docker not available"),
	}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_shell")
	params, _ := json.Marshal(map[string]any{
		"command": "echo hello",
	})

	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error from executor, got nil")
	}
}

func TestExecuteShell_InvalidJSON(t *testing.T) {
	executor := &mockExecutor{}
	registry := mcp.NewToolRegistry()
	if err := RegisterSandboxTools(registry, executor); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("execute_shell")
	_, err := tool.Handler(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// Verify mockExecutor satisfies the interface at compile time.
var _ sandbox.Executor = (*mockExecutor)(nil)
