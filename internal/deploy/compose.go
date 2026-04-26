package deploy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type ComposeRunner struct {
	ComposeFile string
	ProjectName string
	Stdout      func(string)
	Stderr      func(string)
}

func (c ComposeRunner) out(s string) {
	if c.Stdout != nil {
		c.Stdout(s)
	}
}
func (c ComposeRunner) err(s string) {
	if c.Stderr != nil {
		c.Stderr(s)
	}
}

func (c ComposeRunner) composeCmd() ([]string, error) {
	// Prefer: docker compose
	if _, err := exec.LookPath("docker"); err == nil {
		// We'll run `docker compose ...`
		return []string{"docker", "compose"}, nil
	}
	// Fallback: docker-compose
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return []string{"docker-compose"}, nil
	}
	return nil, errors.New("docker compose not found (need Docker Desktop / docker engine)")
}

func (c ComposeRunner) baseArgs() []string {
	args := []string{}
	if c.ProjectName != "" {
		args = append(args, "-p", c.ProjectName)
	}
	if c.ComposeFile != "" {
		args = append(args, "-f", c.ComposeFile)
	}
	return args
}

func (c ComposeRunner) Run(ctx context.Context, subArgs ...string) error {
	prefix, err := c.composeCmd()
	if err != nil {
		return err
	}
	args := append([]string{}, prefix...)
	args = append(args, c.baseArgs()...)
	args = append(args, subArgs...)

	c.out(fmt.Sprintf("$ %s", strings.Join(args, " ")))

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)

	go func() {
		scan := bufio.NewScanner(stdout)
		for scan.Scan() {
			c.out(scan.Text())
		}
	}()
	go func() {
		scan := bufio.NewScanner(stderr)
		for scan.Scan() {
			c.err(scan.Text())
		}
	}()
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (c ComposeRunner) Up(build bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	args := []string{"up", "-d"}
	if build {
		args = append(args, "--build")
	}
	return c.Run(ctx, args...)
}

func (c ComposeRunner) Down(volumes bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	args := []string{"down"}
	if volumes {
		args = append(args, "-v")
	}
	return c.Run(ctx, args...)
}

func (c ComposeRunner) Ps() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return c.Run(ctx, "ps")
}

func (c ComposeRunner) Logs(follow bool) error {
	ctx := context.Background()
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	return c.Run(ctx, args...)
}

func EnsureReportsDir(root string) (string, error) {
	ts := time.Now().UTC().Format("20060102-150405Z")
	dir := fmt.Sprintf("%s/reports/%s", strings.TrimRight(root, "/"), ts)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
