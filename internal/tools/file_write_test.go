package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileWriteTool_SymlinkEscapeRejected(t *testing.T) {
	dir := setupSandbox(t)

	// Create a directory outside sandbox and symlink into sandbox.
	outsideDir := t.TempDir()
	link := filepath.Join(dir, "escape-dir")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	tool := &FileWriteTool{}
	_, err := tool.Execute(context.Background(), `{"path": "escape-dir/secret.txt", "content": "pwned"}`)
	if err == nil {
		t.Fatal("expected FileWriteTool to reject symlink escape, got nil")
	}

	// Verify the file was NOT created outside sandbox.
	escaped := filepath.Join(outsideDir, "secret.txt")
	if _, statErr := os.Stat(escaped); statErr == nil {
		t.Fatalf("file was created outside sandbox at %s", escaped)
	}
}

func TestFileWriteTool_SymlinkFileEscapeRejected(t *testing.T) {
	dir := setupSandbox(t)

	// Create a symlink to a file outside sandbox.
	outsideFile := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(outsideFile, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "escape-file")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	tool := &FileWriteTool{}
	_, err := tool.Execute(context.Background(), `{"path": "escape-file", "content": "pwned"}`)
	if err == nil {
		t.Fatal("expected FileWriteTool to reject symlink file escape, got nil")
	}

	// Verify the outside file was NOT modified.
	data, readErr := os.ReadFile(outsideFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "original" {
		t.Fatalf("outside file was modified: got %q", string(data))
	}
}

func TestFileWriteTool_NormalWriteAllowed(t *testing.T) {
	dir := setupSandbox(t)

	tool := &FileWriteTool{}
	_, err := tool.Execute(context.Background(), `{"path": "newfile.txt", "content": "hello"}`)
	if err != nil {
		t.Fatalf("FileWriteTool rejected normal write: %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(data))
	}
}

func TestFileWriteTool_DotDotTraversalRejected(t *testing.T) {
	setupSandbox(t)

	tool := &FileWriteTool{}
	_, err := tool.Execute(context.Background(), `{"path": "../../../tmp/evil.txt", "content": "pwned"}`)
	if err == nil {
		t.Fatal("expected FileWriteTool to reject path traversal, got nil")
	}
}
