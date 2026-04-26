package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellExecTool_Echo(t *testing.T) {
	tool := &ShellExecTool{}
	result, err := tool.Execute(context.Background(), `{"command":"echo hello world"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out shellExecOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", out.ExitCode)
	}
	if strings.TrimSpace(out.Stdout) != "hello world" {
		t.Errorf("expected 'hello world', got %q", out.Stdout)
	}
}

func TestShellExecTool_DeniedCommand(t *testing.T) {
	tool := &ShellExecTool{}
	_, err := tool.Execute(context.Background(), `{"command":"rm -rf /"}`)
	if err == nil {
		t.Fatal("expected error for denied command")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected 'not allowed' error, got %v", err)
	}
}

func TestShellExecTool_NonZeroExit(t *testing.T) {
	tool := &ShellExecTool{}
	result, err := tool.Execute(context.Background(), `{"command":"false"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out shellExecOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", out.ExitCode)
	}
}

func TestFileReadTool(t *testing.T) {
	// Set up sandbox.
	dir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", dir)

	content := "hello file content"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &FileReadTool{}
	result, err := tool.Execute(context.Background(), `{"path":"test.txt"}`)
	if err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out["content"] != content {
		t.Errorf("expected %q, got %q", content, out["content"])
	}
}

func TestFileReadTool_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", dir)

	tool := &FileReadTool{}
	_, err := tool.Execute(context.Background(), `{"path":"../../etc/passwd"}`)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected traversal error, got %v", err)
	}
}

func TestFileWriteTool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", dir)

	tool := &FileWriteTool{}
	result, err := tool.Execute(context.Background(), `{"path":"output.txt","content":"hello write"}`)
	if err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}

	// Verify file was written.
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello write" {
		t.Errorf("expected 'hello write', got %q", string(data))
	}
}

func TestFileWriteTool_Append(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", dir)

	tool := &FileWriteTool{}
	tool.Execute(context.Background(), `{"path":"append.txt","content":"first"}`)
	tool.Execute(context.Background(), `{"path":"append.txt","content":" second","mode":"append"}`)

	data, err := os.ReadFile(filepath.Join(dir, "append.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first second" {
		t.Errorf("expected 'first second', got %q", string(data))
	}
}

func TestRegexMatchTool(t *testing.T) {
	tool := &RegexMatchTool{}
	result, err := tool.Execute(context.Background(), `{"pattern":"\\b(\\w+)@(\\w+\\.\\w+)\\b","text":"Contact: alice@example.com and bob@test.org"}`)
	if err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	count := int(out["match_count"].(float64))
	if count != 2 {
		t.Errorf("expected 2 matches, got %d", count)
	}
}

func TestRegexMatchTool_InvalidPattern(t *testing.T) {
	tool := &RegexMatchTool{}
	_, err := tool.Execute(context.Background(), `{"pattern":"[invalid","text":"test"}`)
	if err == nil {
		t.Fatal("expected error for invalid pattern")
	}
}

func TestMathEvalTool_Basic(t *testing.T) {
	tool := &MathEvalTool{}
	tests := []struct {
		expr   string
		expect float64
	}{
		{"2 + 3", 5},
		{"10 - 4", 6},
		{"3 * 7", 21},
		{"15 / 4", 3.75},
		{"(2 + 3) * 4", 20},
		{"-5 + 3", -2},
		{"sqrt(16)", 4},
		{"pow(2, 10)", 1024},
		{"abs(-42)", 42},
		{"pi", 3.141592653589793},
		{"min(3, 7)", 3},
		{"max(3, 7)", 7},
	}

	for _, tc := range tests {
		input, _ := json.Marshal(mathEvalInput{Expression: tc.expr})
		result, err := tool.Execute(context.Background(), string(input))
		if err != nil {
			t.Errorf("expr %q: %v", tc.expr, err)
			continue
		}
		var out map[string]any
		json.Unmarshal([]byte(result), &out)
		got := out["result"].(float64)
		if got != tc.expect {
			t.Errorf("expr %q: expected %v, got %v", tc.expr, tc.expect, got)
		}
	}
}

func TestMathEvalTool_DivisionByZero(t *testing.T) {
	tool := &MathEvalTool{}
	_, err := tool.Execute(context.Background(), `{"expression":"1/0"}`)
	if err == nil {
		t.Fatal("expected error for division by zero")
	}
}
