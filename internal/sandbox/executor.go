// Package sandbox provides ephemeral Docker container execution for
// untrusted code. Containers are created with strict resource limits
// (no network, read-only filesystem, memory/CPU caps) and removed after use.
package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Executor defines the interface for sandboxed code execution.
// Implementations must be safe for concurrent use.
type Executor interface {
	Execute(ctx context.Context, language, code string, timeout time.Duration) (*ExecResult, error)
	ExecuteShell(ctx context.Context, command string, timeout time.Duration) (*ExecResult, error)
}

// ExecResult holds the output of a sandboxed code execution.
type ExecResult struct {
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	TimedOut bool          `json:"timed_out"`
	Duration time.Duration `json:"duration"`
}

// SupportedLanguages maps language names to their Docker image and entrypoint command.
// SupportedLanguages maps language names to their Docker image and command.
// Exported for test inspection only — do not modify at runtime (H-7).
var SupportedLanguages = map[string]langConfig{
	"python":     {Image: "python:3.12-slim", Cmd: []string{"python3", "-c"}},
	"javascript": {Image: "node:22-slim", Cmd: []string{"node", "-e"}},
	"go":         {Image: "golang:1.22-alpine", Cmd: []string{"sh", "-c", "cat > /tmp/main.go && go run /tmp/main.go"}},
	"bash":       {Image: "alpine:latest", Cmd: []string{"sh", "-c"}},
}

func init() {
	// Validate image names at startup to prevent injection via modified map.
	for lang, cfg := range SupportedLanguages {
		for _, c := range cfg.Image {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == ':' || c == '.' || c == '-' || c == '_' || c == '/') {
				panic(fmt.Sprintf("sandbox: invalid character %q in image name for language %q", string(c), lang))
			}
		}
	}
}

// langConfig describes the Docker image and command for a supported language.
type langConfig struct {
	Image string
	Cmd   []string
}

// DockerExecutor implements Executor using the Docker Engine API over a unix socket.
type DockerExecutor struct {
	client   *http.Client
	baseURL  string
	socketPath string
}

// NewDockerExecutor creates a DockerExecutor that communicates with the Docker
// daemon via the given unix socket path (typically /var/run/docker.sock).
func NewDockerExecutor(socketPath string) *DockerExecutor {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}

	return &DockerExecutor{
		client: &http.Client{
			Transport: transport,
			// No global timeout — we enforce per-request timeouts via context.
		},
		baseURL:    "http://docker",
		socketPath: socketPath,
	}
}

// Execute runs the given code in an ephemeral Docker container for the
// specified language. The container is created with strict security constraints:
//   - --network none (no network access)
//   - --memory 512m (memory limit)
//   - --cpus 1 (CPU limit)
//   - --read-only (read-only root filesystem, /tmp is writable via tmpfs)
//
// The container is always removed after execution completes.
func (d *DockerExecutor) Execute(ctx context.Context, language, code string, timeout time.Duration) (*ExecResult, error) {
	if code == "" {
		return nil, fmt.Errorf("sandbox: code must not be empty")
	}

	lang, ok := SupportedLanguages[language]
	if !ok {
		return nil, fmt.Errorf("sandbox: unsupported language %q", language)
	}

	// Build the container command. For Go, code is passed via stdin to a
	// shell pipeline. For other languages, code is the last argument.
	cmd := buildCommand(lang, code)

	containerID, err := d.createContainer(ctx, lang.Image, cmd, code, language)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create container: %w", err)
	}

	return d.executeInContainer(ctx, containerID, timeout)
}

// buildCommand constructs the command array for the container.
// For Go, code is written to a file via a shell pipeline.
// For other languages, code is passed as the final argument.
func buildCommand(lang langConfig, code string) []string {
	if len(lang.Cmd) == 3 && lang.Cmd[0] == "sh" && lang.Cmd[1] == "-c" {
		// Shell pipeline (Go, bash): embed code in the shell command.
		if strings.Contains(lang.Cmd[2], "go run") {
			// Go: write code to file, then run it.
			return []string{"sh", "-c", fmt.Sprintf("cat > /tmp/main.go << 'GOEOF'\n%s\nGOEOF\ngo run /tmp/main.go", code)}
		}
		// Bash: run code directly via sh -c.
		return []string{"sh", "-c", code}
	}
	// Python, JavaScript: pass code as -c / -e argument.
	return append(lang.Cmd, code)
}

// ExecuteShell runs an arbitrary shell command in an ephemeral Docker container
// with the same security constraints as Execute (no network, memory/CPU limits,
// read-only root filesystem). The command is passed to bash -c.
func (d *DockerExecutor) ExecuteShell(ctx context.Context, command string, timeout time.Duration) (*ExecResult, error) {
	if command == "" {
		return nil, fmt.Errorf("sandbox: command must not be empty")
	}

	const shellImage = "ubuntu:24.04"
	cmd := []string{"bash", "-c", command}

	containerID, err := d.createContainer(ctx, shellImage, cmd, "", "shell")
	if err != nil {
		return nil, fmt.Errorf("sandbox: create container: %w", err)
	}

	return d.executeInContainer(ctx, containerID, timeout)
}

// executeInContainer runs the start-wait-logs-remove lifecycle for an already-created
// container. The container is always removed after execution, even on error paths.
func (d *DockerExecutor) executeInContainer(ctx context.Context, containerID string, timeout time.Duration) (*ExecResult, error) {
	defer func() {
		if rmErr := d.removeContainer(context.Background(), containerID); rmErr != nil {
			slog.Warn("sandbox: failed to remove container",
				"container_id", containerID,
				"error", rmErr,
			)
		}
	}()

	start := time.Now()

	if err := d.startContainer(ctx, containerID); err != nil {
		return nil, fmt.Errorf("sandbox: start container: %w", err)
	}

	// Wait for container to finish or timeout.
	timedOut, exitCode, err := d.waitContainer(ctx, containerID, timeout)
	duration := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("sandbox: wait container: %w", err)
	}

	// If timed out, kill the container.
	if timedOut {
		if killErr := d.killContainer(context.Background(), containerID); killErr != nil {
			slog.Warn("sandbox: failed to kill timed-out container",
				"container_id", containerID,
				"error", killErr,
			)
		}
	}

	stdout, stderr, err := d.getLogs(context.Background(), containerID)
	if err != nil {
		return nil, fmt.Errorf("sandbox: get logs: %w", err)
	}

	return &ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		TimedOut: timedOut,
		Duration: duration,
	}, nil
}

// --- Docker Engine API calls ---

// containerCreateBody is the JSON body for POST /containers/create.
type containerCreateBody struct {
	Image           string            `json:"Image"`
	Cmd             []string          `json:"Cmd"`
	WorkingDir      string            `json:"WorkingDir"`
	NetworkDisabled bool              `json:"NetworkDisabled"`
	Labels          map[string]string `json:"Labels,omitempty"`
	HostConfig      hostConfig        `json:"HostConfig"`
	// Stdin is not opened — code is embedded in the command.
	OpenStdin bool `json:"OpenStdin"`
	StdinOnce bool `json:"StdinOnce"`
	Tty       bool `json:"Tty"`
}

type hostConfig struct {
	Memory     int64    `json:"Memory"`
	NanoCPUs   int64    `json:"NanoCpus"`
	ReadonlyRootfs bool `json:"ReadonlyRootfs"`
	Tmpfs      map[string]string `json:"Tmpfs"`
	NetworkMode string  `json:"NetworkMode"`
}

type createContainerResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// containerNamePrefix is used to name sandbox containers so monitoring tools
// (e.g. the sentinel agent) can identify them as ephemeral and skip alerting.
const containerNamePrefix = "agentos-sandbox-"

// generateContainerName returns a unique container name with the sandbox prefix.
func generateContainerName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return containerNamePrefix + hex.EncodeToString(b[:])
}

func (d *DockerExecutor) createContainer(ctx context.Context, image string, cmd []string, _ string, _ string) (string, error) {
	body := containerCreateBody{
		Image:           image,
		Cmd:             cmd,
		WorkingDir:      "/sandbox",
		NetworkDisabled: true,
		Labels: map[string]string{
			"nexixai.role": "sandbox",
		},
		HostConfig: hostConfig{
			Memory:         512 * 1024 * 1024, // 512 MiB
			NanoCPUs:       1_000_000_000,      // 1 CPU
			ReadonlyRootfs: true,
			Tmpfs: map[string]string{
				"/tmp":     "size=64m",
				"/sandbox": "size=64m",
			},
			NetworkMode: "none",
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal create body: %w", err)
	}

	name := generateContainerName()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.baseURL+"/v1.43/containers/create?name="+name, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("docker API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var result createContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	slog.Debug("sandbox: container created",
		"container_id", result.ID,
		"image", image,
	)

	return result.ID, nil
}

func (d *DockerExecutor) startContainer(ctx context.Context, containerID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.baseURL+"/v1.43/containers/"+containerID+"/start", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker API call: %w", err)
	}
	defer resp.Body.Close()

	// 204 = started, 304 = already started.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// waitContainerResponse is the JSON returned by POST /containers/{id}/wait.
type waitContainerResponse struct {
	StatusCode int `json:"StatusCode"`
}

func (d *DockerExecutor) waitContainer(ctx context.Context, containerID string, timeout time.Duration) (timedOut bool, exitCode int, err error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(waitCtx, http.MethodPost,
		d.baseURL+"/v1.43/containers/"+containerID+"/wait", nil)
	if err != nil {
		return false, -1, fmt.Errorf("build request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		// Check if the error is due to timeout.
		if waitCtx.Err() != nil {
			return true, -1, nil
		}
		return false, -1, fmt.Errorf("docker API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return false, -1, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var result waitContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, -1, fmt.Errorf("decode response: %w", err)
	}

	return false, result.StatusCode, nil
}

func (d *DockerExecutor) killContainer(ctx context.Context, containerID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.baseURL+"/v1.43/containers/"+containerID+"/kill", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (d *DockerExecutor) removeContainer(ctx context.Context, containerID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		d.baseURL+"/v1.43/containers/"+containerID+"?force=true", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	slog.Debug("sandbox: container removed", "container_id", containerID)
	return nil
}

// getLogs retrieves stdout and stderr from the container.
// Docker multiplexed stream format: each frame has an 8-byte header.
// Byte 0: stream type (1=stdout, 2=stderr), bytes 4-7: frame size (big-endian).
func (d *DockerExecutor) getLogs(ctx context.Context, containerID string) (stdout, stderr string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.baseURL+"/v1.43/containers/"+containerID+"/logs?stdout=true&stderr=true", nil)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("docker API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	// Read the multiplexed stream.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read logs: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	demuxDockerStream(raw, &stdoutBuf, &stderrBuf)

	return stdoutBuf.String(), stderrBuf.String(), nil
}

// demuxDockerStream parses the Docker multiplexed log stream format.
// Each frame: [stream_type(1)][0(3)][size(4, big-endian)][payload(size)].
func demuxDockerStream(data []byte, stdout, stderr *bytes.Buffer) {
	for len(data) >= 8 {
		streamType := data[0]
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		data = data[8:]

		if size > len(data) {
			size = len(data)
		}

		switch streamType {
		case 1:
			stdout.Write(data[:size])
		case 2:
			stderr.Write(data[:size])
		}
		data = data[size:]
	}
}
