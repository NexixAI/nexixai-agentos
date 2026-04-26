package sandbox

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestBuildCommand_Python(t *testing.T) {
	lang := SupportedLanguages["python"]
	cmd := buildCommand(lang, `print("hello")`)

	if len(cmd) != 3 {
		t.Fatalf("expected 3 elements, got %d: %v", len(cmd), cmd)
	}
	if cmd[0] != "python3" || cmd[1] != "-c" {
		t.Errorf("expected [python3, -c, ...], got %v", cmd[:2])
	}
	if cmd[2] != `print("hello")` {
		t.Errorf("expected code as last arg, got %q", cmd[2])
	}
}

func TestBuildCommand_JavaScript(t *testing.T) {
	lang := SupportedLanguages["javascript"]
	cmd := buildCommand(lang, `console.log("hello")`)

	if len(cmd) != 3 {
		t.Fatalf("expected 3 elements, got %d: %v", len(cmd), cmd)
	}
	if cmd[0] != "node" || cmd[1] != "-e" {
		t.Errorf("expected [node, -e, ...], got %v", cmd[:2])
	}
}

func TestBuildCommand_Go(t *testing.T) {
	lang := SupportedLanguages["go"]
	code := `package main
import "fmt"
func main() { fmt.Println("hello") }`

	cmd := buildCommand(lang, code)
	if len(cmd) != 3 {
		t.Fatalf("expected 3 elements, got %d: %v", len(cmd), cmd)
	}
	if cmd[0] != "sh" || cmd[1] != "-c" {
		t.Errorf("expected [sh, -c, ...], got %v", cmd[:2])
	}
	if !bytes.Contains([]byte(cmd[2]), []byte("go run /tmp/main.go")) {
		t.Errorf("expected go run pipeline, got %q", cmd[2])
	}
}

func TestBuildCommand_Bash(t *testing.T) {
	lang := SupportedLanguages["bash"]
	cmd := buildCommand(lang, "echo hello")

	if len(cmd) != 3 {
		t.Fatalf("expected 3 elements, got %d: %v", len(cmd), cmd)
	}
	if cmd[0] != "sh" || cmd[1] != "-c" {
		t.Errorf("expected [sh, -c, ...], got %v", cmd[:2])
	}
	if cmd[2] != "echo hello" {
		t.Errorf("expected code as shell command, got %q", cmd[2])
	}
}

func TestSupportedLanguages_AllMapped(t *testing.T) {
	expected := map[string]string{
		"python":     "python:3.12-slim",
		"javascript": "node:22-slim",
		"go":         "golang:1.22-alpine",
		"bash":       "alpine:latest",
	}

	for lang, wantImage := range expected {
		cfg, ok := SupportedLanguages[lang]
		if !ok {
			t.Errorf("language %q not in SupportedLanguages", lang)
			continue
		}
		if cfg.Image != wantImage {
			t.Errorf("SupportedLanguages[%q].Image = %q, want %q", lang, cfg.Image, wantImage)
		}
		if len(cfg.Cmd) == 0 {
			t.Errorf("SupportedLanguages[%q].Cmd is empty", lang)
		}
	}
}

func TestExecute_EmptyCode(t *testing.T) {
	exec := NewDockerExecutor("/var/run/docker.sock")
	_, err := exec.Execute(context.Background(), "python", "", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for empty code, got nil")
	}
}

func TestExecute_UnsupportedLanguage(t *testing.T) {
	exec := NewDockerExecutor("/var/run/docker.sock")
	_, err := exec.Execute(context.Background(), "ruby", "puts 'hello'", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for unsupported language, got nil")
	}
}

func TestDemuxDockerStream(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		wantStdout string
		wantStderr string
	}{
		{
			name:       "empty stream",
			data:       nil,
			wantStdout: "",
			wantStderr: "",
		},
		{
			name: "stdout only",
			data: func() []byte {
				msg := []byte("hello\n")
				header := []byte{1, 0, 0, 0, 0, 0, 0, byte(len(msg))}
				return append(header, msg...)
			}(),
			wantStdout: "hello\n",
			wantStderr: "",
		},
		{
			name: "stderr only",
			data: func() []byte {
				msg := []byte("error\n")
				header := []byte{2, 0, 0, 0, 0, 0, 0, byte(len(msg))}
				return append(header, msg...)
			}(),
			wantStdout: "",
			wantStderr: "error\n",
		},
		{
			name: "mixed stdout and stderr",
			data: func() []byte {
				out := []byte("out\n")
				err := []byte("err\n")
				hOut := []byte{1, 0, 0, 0, 0, 0, 0, byte(len(out))}
				hErr := []byte{2, 0, 0, 0, 0, 0, 0, byte(len(err))}
				var buf []byte
				buf = append(buf, hOut...)
				buf = append(buf, out...)
				buf = append(buf, hErr...)
				buf = append(buf, err...)
				return buf
			}(),
			wantStdout: "out\n",
			wantStderr: "err\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			demuxDockerStream(tt.data, &stdout, &stderr)
			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestNewDockerExecutor(t *testing.T) {
	exec := NewDockerExecutor("/var/run/docker.sock")
	if exec == nil {
		t.Fatal("NewDockerExecutor returned nil")
	}
	if exec.socketPath != "/var/run/docker.sock" {
		t.Errorf("socketPath = %q, want /var/run/docker.sock", exec.socketPath)
	}
	if exec.client == nil {
		t.Error("client is nil")
	}
}

func TestExecuteShell_EmptyCommand(t *testing.T) {
	exec := NewDockerExecutor("/var/run/docker.sock")
	_, err := exec.ExecuteShell(context.Background(), "", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for empty command, got nil")
	}
}

func TestExecuteShell_CommandBuilding(t *testing.T) {
	// ExecuteShell should use bash -c with the provided command.
	// We can't test the full Docker flow without a daemon, but we can
	// verify that an empty command is rejected and a non-empty command
	// attempts to create a container (which will fail without Docker).
	exec := NewDockerExecutor("/var/run/docker.sock")

	// Non-empty command should attempt Docker API call (and fail without daemon).
	_, err := exec.ExecuteShell(context.Background(), "echo hello", 10*time.Second)
	if err == nil {
		// If Docker is somehow available, that's fine too.
		return
	}
	// The error should be from the container creation step, not from validation.
	if err.Error() == "sandbox: command must not be empty" {
		t.Error("non-empty command should not produce empty-command error")
	}
}

func TestGenerateContainerName(t *testing.T) {
	name := generateContainerName()
	if !strings.HasPrefix(name, containerNamePrefix) {
		t.Errorf("container name %q does not have prefix %q", name, containerNamePrefix)
	}
	// prefix (16 chars) + 12 hex chars = 28 chars
	if len(name) != len(containerNamePrefix)+12 {
		t.Errorf("container name %q has unexpected length %d", name, len(name))
	}

	// Names should be unique.
	name2 := generateContainerName()
	if name == name2 {
		t.Errorf("two generated names are identical: %q", name)
	}
}

// Verify DockerExecutor implements the Executor interface at compile time.
var _ Executor = (*DockerExecutor)(nil)
