package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// math_eval tests
// ---------------------------------------------------------------------------

func TestMathEval_BasicArithmetic(t *testing.T) {
	tool := &MathEvalTool{}

	cases := []struct {
		name       string
		expr       string
		wantResult float64
	}{
		{"addition", "2 + 3", 5},
		{"subtraction", "10 - 4", 6},
		{"multiplication", "6 * 7", 42},
		{"division", "15 / 3", 5},
		{"modulo", "10 % 3", 1},
		{"parentheses", "(2 + 3) * 4", 20},
		{"nested_parens", "((1 + 2) * (3 + 4))", 21},
		{"negative", "-5 + 3", -2},
		{"decimal", "1.5 * 2", 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"expression":%q}`, tc.expr)
			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var out map[string]any
			if err := json.Unmarshal([]byte(result), &out); err != nil {
				t.Fatalf("failed to parse output: %v", err)
			}
			got, ok := out["result"].(float64)
			if !ok {
				t.Fatalf("result is not a number: %v", out["result"])
			}
			if got != tc.wantResult {
				t.Errorf("expected %v, got %v", tc.wantResult, got)
			}
		})
	}
}

func TestMathEval_Functions(t *testing.T) {
	tool := &MathEvalTool{}

	cases := []struct {
		name       string
		expr       string
		wantResult float64
	}{
		{"sqrt", "sqrt(16)", 4},
		{"abs", "abs(-7)", 7},
		{"floor", "floor(3.9)", 3},
		{"ceil", "ceil(3.1)", 4},
		{"round", "round(3.5)", 4},
		{"pow", "pow(2, 10)", 1024},
		{"min", "min(3, 7)", 3},
		{"max", "max(3, 7)", 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"expression":%q}`, tc.expr)
			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var out map[string]any
			if err := json.Unmarshal([]byte(result), &out); err != nil {
				t.Fatalf("failed to parse output: %v", err)
			}
			got := out["result"].(float64)
			if got != tc.wantResult {
				t.Errorf("expected %v, got %v", tc.wantResult, got)
			}
		})
	}
}

func TestMathEval_DivisionByZero(t *testing.T) {
	tool := &MathEvalTool{}
	input := `{"expression":"10 / 0"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected division by zero error, got nil")
	}
	if !strings.Contains(err.Error(), "division by zero") {
		t.Errorf("expected 'division by zero' error, got: %v", err)
	}
}

func TestMathEval_ModuloByZero(t *testing.T) {
	tool := &MathEvalTool{}
	input := `{"expression":"10 % 0"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected modulo by zero error, got nil")
	}
	if !strings.Contains(err.Error(), "modulo by zero") {
		t.Errorf("expected 'modulo by zero' error, got: %v", err)
	}
}

func TestMathEval_EmptyExpression(t *testing.T) {
	tool := &MathEvalTool{}
	input := `{"expression":""}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty expression, got nil")
	}
	if !strings.Contains(err.Error(), "expression is required") {
		t.Errorf("expected 'expression is required' error, got: %v", err)
	}
}

func TestMathEval_InvalidJSON(t *testing.T) {
	tool := &MathEvalTool{}
	_, err := tool.Execute(context.Background(), "not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid input") {
		t.Errorf("expected 'invalid input' error, got: %v", err)
	}
}

func TestMathEval_InvalidExpression(t *testing.T) {
	tool := &MathEvalTool{}
	input := `{"expression":"2 + * 3"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invalid expression, got nil")
	}
}

func TestMathEval_MissingClosingParen(t *testing.T) {
	tool := &MathEvalTool{}
	input := `{"expression":"(2 + 3"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing closing parenthesis, got nil")
	}
	if !strings.Contains(err.Error(), "parenthesis") {
		t.Errorf("expected parenthesis error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// shell_exec tests
// ---------------------------------------------------------------------------

func TestShellExec_Success(t *testing.T) {
	tool := &ShellExecTool{}
	input := `{"command":"echo hello"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out shellExecOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if out.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", out.ExitCode)
	}
	if strings.TrimSpace(out.Stdout) != "hello" {
		t.Errorf("expected stdout 'hello', got %q", out.Stdout)
	}
}

func TestShellExec_NonZeroExit(t *testing.T) {
	tool := &ShellExecTool{}
	input := `{"command":"false"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out shellExecOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if out.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", out.ExitCode)
	}
}

func TestShellExec_DeniedCommand(t *testing.T) {
	tool := &ShellExecTool{}

	cases := []struct {
		name    string
		command string
	}{
		{"rm", `{"command":"rm -rf /"}`},
		{"chmod", `{"command":"chmod 777 /etc/passwd"}`},
		{"kill", `{"command":"kill -9 1"}`},
		{"full_path_rm", `{"command":"/usr/bin/rm foo"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), tc.command)
			if err == nil {
				t.Fatal("expected denied command error, got nil")
			}
			if !strings.Contains(err.Error(), "not allowed") {
				t.Errorf("expected 'not allowed' error, got: %v", err)
			}
		})
	}
}

func TestShellExec_EmptyCommand(t *testing.T) {
	tool := &ShellExecTool{}
	input := `{"command":""}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty command, got nil")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("expected 'command is required' error, got: %v", err)
	}
}

func TestShellExec_InvalidJSON(t *testing.T) {
	tool := &ShellExecTool{}
	_, err := tool.Execute(context.Background(), "not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestShellExec_Stderr(t *testing.T) {
	tool := &ShellExecTool{}
	input := `{"command":"ls /definitely-missing-shell-exec-path"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out shellExecOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if !strings.Contains(out.Stderr, "definitely-missing-shell-exec-path") {
		t.Errorf("expected stderr to contain the missing path, got %q", out.Stderr)
	}
}

func TestShellExec_Timeout(t *testing.T) {
	tool := &ShellExecTool{}
	// Use "tail -f /dev/null" instead of "sleep" (removed from allowlist, v9.0 L-10).
	input := `{"command":"tail -f /dev/null","timeout_seconds":1}`

	start := time.Now()
	result, err := tool.Execute(context.Background(), input)
	elapsed := time.Since(start)

	// The command must not run for the full 60 seconds.
	if elapsed > 10*time.Second {
		t.Fatalf("timeout did not fire; elapsed %v", elapsed)
	}

	if err != nil {
		// Tool may return a "timed out" error directly.
		if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "signal") {
			t.Errorf("expected timeout-related error, got: %v", err)
		}
		return
	}
	// Or it may return a result with non-zero exit code (killed process).
	var out shellExecOutput
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if out.ExitCode == 0 {
		t.Error("expected non-zero exit code from timed-out command")
	}
}

func TestShellExec_RejectsShellWrappers(t *testing.T) {
	tool := &ShellExecTool{}

	cases := []string{
		`{"command":"sh -c 'echo hello'"}`,
		`{"command":"bash -c 'echo hello'"}`,
		`{"command":"python3 -c 'print(1)'"}`,
	}

	for _, input := range cases {
		_, err := tool.Execute(context.Background(), input)
		if err == nil {
			t.Fatalf("expected error for %s", input)
		}
		if !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("expected 'not allowed' error for %s, got %v", input, err)
		}
	}
}

// ---------------------------------------------------------------------------
// regex_match tests
// ---------------------------------------------------------------------------

func TestRegexMatch_SimpleMatch(t *testing.T) {
	tool := &RegexMatchTool{}
	input := `{"pattern":"\\d+","text":"abc 123 def 456"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	matchCount := int(out["match_count"].(float64))
	if matchCount != 2 {
		t.Errorf("expected 2 matches, got %d", matchCount)
	}

	matches := out["matches"].([]any)
	first := matches[0].(map[string]any)
	if first["full_match"] != "123" {
		t.Errorf("expected first match '123', got %q", first["full_match"])
	}
}

func TestRegexMatch_CaptureGroups(t *testing.T) {
	tool := &RegexMatchTool{}
	input := `{"pattern":"(\\w+)@(\\w+\\.\\w+)","text":"Contact alice@example.com or bob@test.org"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	matchCount := int(out["match_count"].(float64))
	if matchCount != 2 {
		t.Errorf("expected 2 matches, got %d", matchCount)
	}

	matches := out["matches"].([]any)
	first := matches[0].(map[string]any)
	groups := first["groups"].([]any)
	if groups[0] != "alice" {
		t.Errorf("expected group 0 'alice', got %q", groups[0])
	}
	if groups[1] != "example.com" {
		t.Errorf("expected group 1 'example.com', got %q", groups[1])
	}
}

func TestRegexMatch_NoMatch(t *testing.T) {
	tool := &RegexMatchTool{}
	input := `{"pattern":"\\d+","text":"no numbers here"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	matchCount := int(out["match_count"].(float64))
	if matchCount != 0 {
		t.Errorf("expected 0 matches, got %d", matchCount)
	}
}

func TestRegexMatch_InvalidPattern(t *testing.T) {
	tool := &RegexMatchTool{}
	input := `{"pattern":"[invalid","text":"test"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invalid regex pattern, got nil")
	}
	if !strings.Contains(err.Error(), "invalid pattern") {
		t.Errorf("expected 'invalid pattern' error, got: %v", err)
	}
}

func TestRegexMatch_MissingPattern(t *testing.T) {
	tool := &RegexMatchTool{}
	input := `{"pattern":"","text":"test"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty pattern, got nil")
	}
	if !strings.Contains(err.Error(), "pattern is required") {
		t.Errorf("expected 'pattern is required' error, got: %v", err)
	}
}

func TestRegexMatch_MissingText(t *testing.T) {
	tool := &RegexMatchTool{}
	input := `{"pattern":"\\d+","text":""}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	if !strings.Contains(err.Error(), "text is required") {
		t.Errorf("expected 'text is required' error, got: %v", err)
	}
}

func TestRegexMatch_MaxMatches(t *testing.T) {
	tool := &RegexMatchTool{}
	// Text with many numbers but limit to 2 matches.
	input := `{"pattern":"\\d+","text":"1 2 3 4 5 6 7 8 9 10","max_matches":2}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	matchCount := int(out["match_count"].(float64))
	if matchCount != 2 {
		t.Errorf("expected 2 matches, got %d", matchCount)
	}
}

// ---------------------------------------------------------------------------
// file_read tests
// ---------------------------------------------------------------------------

func TestFileRead_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	// Create a test file.
	content := "hello from file_read test"
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	tool := &FileReadTool{}
	input := fmt.Sprintf(`{"path":"test.txt"}`)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if out["content"] != content {
		t.Errorf("expected content %q, got %q", content, out["content"])
	}
	if int(out["size"].(float64)) != len(content) {
		t.Errorf("expected size %d, got %v", len(content), out["size"])
	}
}

func TestFileRead_Base64(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	binaryContent := []byte{0x00, 0x01, 0xFF, 0xFE, 0x42}
	testFile := filepath.Join(tmpDir, "binary.bin")
	if err := os.WriteFile(testFile, binaryContent, 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	tool := &FileReadTool{}
	input := `{"path":"binary.bin","encoding":"base64"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	// base64 of {0x00, 0x01, 0xFF, 0xFE, 0x42} = "AAH//kI="
	if out["content"] != "AAH//kI=" {
		t.Errorf("expected base64 'AAH//kI=', got %q", out["content"])
	}
}

func TestFileRead_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileReadTool{}
	input := `{"path":"nonexistent.txt"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for file not found, got nil")
	}
	// With symlink resolution, nonexistent files are caught at the
	// EvalSymlinks stage ("cannot resolve path") rather than os.Open
	// ("cannot open file"). Both are acceptable.
	if !strings.Contains(err.Error(), "cannot open file") &&
		!strings.Contains(err.Error(), "cannot resolve path") {
		t.Errorf("expected file-not-found error, got: %v", err)
	}
}

func TestFileRead_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileReadTool{}
	input := `{"path":"../../etc/passwd"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected path traversal error, got: %v", err)
	}
}

func TestFileRead_EmptyPath(t *testing.T) {
	tool := &FileReadTool{}
	input := `{"path":""}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected 'path is required' error, got: %v", err)
	}
}

func TestFileRead_AbsolutePathOutsideSandbox(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileReadTool{}
	input := `{"path":"/etc/passwd"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for absolute path outside sandbox, got nil")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("expected 'outside allowed directory' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// file_write tests
// ---------------------------------------------------------------------------

func TestFileWrite_Overwrite(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileWriteTool{}
	input := `{"path":"output.txt","content":"hello world"}`

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if int(out["bytes_written"].(float64)) != 11 {
		t.Errorf("expected 11 bytes written, got %v", out["bytes_written"])
	}
	if out["mode"] != "overwrite" {
		t.Errorf("expected mode 'overwrite', got %q", out["mode"])
	}

	// Verify file content on disk.
	data, err := os.ReadFile(filepath.Join(tmpDir, "output.txt"))
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected file content 'hello world', got %q", string(data))
	}
}

func TestFileWrite_Append(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileWriteTool{}

	// Write initial content.
	input1 := `{"path":"append.txt","content":"first "}`
	if _, err := tool.Execute(context.Background(), input1); err != nil {
		t.Fatalf("unexpected error on first write: %v", err)
	}

	// Append more content.
	input2 := `{"path":"append.txt","content":"second","mode":"append"}`
	if _, err := tool.Execute(context.Background(), input2); err != nil {
		t.Fatalf("unexpected error on append: %v", err)
	}

	// Verify combined content.
	data, err := os.ReadFile(filepath.Join(tmpDir, "append.txt"))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "first second" {
		t.Errorf("expected 'first second', got %q", string(data))
	}
}

func TestFileWrite_OverwriteReplacesContent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileWriteTool{}

	// Write initial content.
	input1 := `{"path":"replace.txt","content":"original content"}`
	if _, err := tool.Execute(context.Background(), input1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Overwrite with new content.
	input2 := `{"path":"replace.txt","content":"new"}`
	if _, err := tool.Execute(context.Background(), input2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "replace.txt"))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "new" {
		t.Errorf("expected 'new', got %q", string(data))
	}
}

func TestFileWrite_CreatesSubdirectories(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileWriteTool{}
	input := `{"path":"sub/dir/file.txt","content":"nested"}`

	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "sub", "dir", "file.txt"))
	if err != nil {
		t.Fatalf("failed to read nested file: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestFileWrite_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileWriteTool{}
	input := `{"path":"../../etc/evil","content":"bad"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected path traversal error, got: %v", err)
	}
}

func TestFileWrite_EmptyPath(t *testing.T) {
	tool := &FileWriteTool{}
	input := `{"path":"","content":"data"}`

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected 'path is required' error, got: %v", err)
	}
}

func TestFileWrite_ContentTooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	tool := &FileWriteTool{}
	bigContent := strings.Repeat("x", fileWriteMaxSize+1)
	input := fmt.Sprintf(`{"path":"big.txt","content":%q}`, bigContent)

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for content too large, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("expected 'exceeds maximum size' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// file_read + file_write round-trip test
// ---------------------------------------------------------------------------

func TestFileReadWrite_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", tmpDir)

	writeTool := &FileWriteTool{}
	readTool := &FileReadTool{}

	content := "round-trip test content\nwith multiple lines\n"
	writeInput := fmt.Sprintf(`{"path":"roundtrip.txt","content":%q}`, content)

	_, err := writeTool.Execute(context.Background(), writeInput)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	readInput := `{"path":"roundtrip.txt"}`
	result, err := readTool.Execute(context.Background(), readInput)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("failed to parse read output: %v", err)
	}

	if out["content"] != content {
		t.Errorf("round-trip content mismatch: expected %q, got %q", content, out["content"])
	}
}
