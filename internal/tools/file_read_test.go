package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupSandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENTOS_TOOL_FILE_BASE_DIR", dir)
	return dir
}

func TestSafePath_NormalFileAllowed(t *testing.T) {
	dir := setupSandbox(t)
	// Create a normal file inside sandbox.
	target := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(target, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := safePath("hello.txt", true)
	if err != nil {
		t.Fatalf("safePath rejected normal file: %v", err)
	}
	if got != target {
		t.Fatalf("expected %q, got %q", target, got)
	}
}

func TestSafePath_DotDotTraversalRejected(t *testing.T) {
	setupSandbox(t)

	_, err := safePath("../../../etc/passwd", true)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "outside allowed directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafePath_DotDotTraversalRejectedForWrites(t *testing.T) {
	setupSandbox(t)

	_, err := safePath("../../../etc/passwd", false)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "outside allowed directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafePath_SymlinkEscapeRejected(t *testing.T) {
	dir := setupSandbox(t)

	// Create a symlink inside sandbox pointing to /etc/hostname.
	link := filepath.Join(dir, "escape")
	if err := os.Symlink("/etc/hostname", link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	_, err := safePath("escape", true)
	if err == nil {
		t.Fatal("expected error for symlink escape, got nil")
	}
	if !strings.Contains(err.Error(), "resolves outside allowed directory via symlink") &&
		!strings.Contains(err.Error(), "cannot resolve path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafePath_SymlinkEscapeRejectedForWrites(t *testing.T) {
	dir := setupSandbox(t)

	// Create a directory outside sandbox and symlink to it.
	outsideDir := t.TempDir()
	link := filepath.Join(dir, "escape-dir")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	// Writing to a file inside a symlinked directory should be rejected.
	_, err := safePath("escape-dir/secret.txt", false)
	if err == nil {
		t.Fatal("expected error for symlink escape on write, got nil")
	}
	if !strings.Contains(err.Error(), "resolves outside allowed directory via symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafePath_SymlinkWithinSandboxAllowed(t *testing.T) {
	dir := setupSandbox(t)

	// Create a real file, then a symlink to it, both inside sandbox.
	target := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	got, err := safePath("link.txt", true)
	if err != nil {
		t.Fatalf("safePath rejected symlink within sandbox: %v", err)
	}
	// Should resolve to the real file path.
	if got != target {
		t.Fatalf("expected resolved path %q, got %q", target, got)
	}
}

func TestSafePath_AbsolutePathOutsideRejected(t *testing.T) {
	setupSandbox(t)

	_, err := safePath("/etc/passwd", true)
	if err == nil {
		t.Fatal("expected error for absolute path outside sandbox, got nil")
	}
}

func TestSafePath_WriteToNewFileAllowed(t *testing.T) {
	dir := setupSandbox(t)

	// A new file that doesn't exist yet should be allowed for writes.
	got, err := safePath("newfile.txt", false)
	if err != nil {
		t.Fatalf("safePath rejected write to new file: %v", err)
	}
	expected := filepath.Join(dir, "newfile.txt")
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFileReadTool_SymlinkEscapeRejected(t *testing.T) {
	dir := setupSandbox(t)

	// Create a symlink to /etc/hostname inside sandbox.
	link := filepath.Join(dir, "escape")
	if err := os.Symlink("/etc/hostname", link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	tool := &FileReadTool{}
	_, err := tool.Execute(context.Background(), `{"path": "escape"}`)
	if err == nil {
		t.Fatal("expected FileReadTool to reject symlink escape, got nil")
	}
}

func TestFileReadTool_NormalFileAllowed(t *testing.T) {
	dir := setupSandbox(t)
	target := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &FileReadTool{}
	result, err := tool.Execute(context.Background(), `{"path": "ok.txt"}`)
	if err != nil {
		t.Fatalf("FileReadTool rejected normal file: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected content 'hello' in result, got %s", result)
	}
}
