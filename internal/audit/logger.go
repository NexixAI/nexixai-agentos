package audit

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Time          string         `json:"time"`
	TenantID      string         `json:"tenant_id"`
	PrincipalID   string         `json:"principal_id,omitempty"`
	Action        string         `json:"action"`
	Resource      string         `json:"resource"`
	Outcome       string         `json:"outcome"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	RequestID     string         `json:"request_id,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type Logger interface {
	Log(e Entry)
	Close() error
}

type jsonLineLogger struct {
	mu        sync.Mutex
	w         io.Writer
	file      *os.File
	closed    bool
	closeOnce sync.Once
}

func (l *jsonLineLogger) Log(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		slog.Error("failed to marshal audit entry", "error", err)
		return
	}
	if _, err := l.w.Write(append(b, '\n')); err != nil {
		slog.Error("failed to write audit entry", "error", err)
	}
	if f, ok := l.w.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			slog.Error("failed to flush audit log", "error", err)
		}
	}
}

func (l *jsonLineLogger) Close() error {
	var err error
	l.closeOnce.Do(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.closed = true
		if f, ok := l.w.(interface{ Flush() error }); ok {
			if flushErr := f.Flush(); flushErr != nil {
				err = flushErr
			}
		}
		if l.file != nil {
			if closeErr := l.file.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
	})
	return err
}

// NewFromEnv creates an audit logger.
//
// AGENTOS_AUDIT_SINK:
//   - "stdout"
//   - "stderr"
//   - "file:/path/to/audit.log" (default; falls back to data/audit/{service}.audit.log)
func NewFromEnv() Logger {
	sink := strings.TrimSpace(os.Getenv("AGENTOS_AUDIT_SINK"))
	if sink == "" {
		svc := strings.TrimSpace(os.Getenv("AGENTOS_SERVICE"))
		name := "audit.log"
		if svc != "" {
			name = svc + ".audit.log"
		}
		sink = "file:" + filepath.Join("data", "audit", name)
	}
	if sink == "" || sink == "stdout" {
		return &jsonLineLogger{w: os.Stdout}
	}
	if sink == "stderr" {
		return &jsonLineLogger{w: os.Stderr}
	}
	if strings.HasPrefix(sink, "file:") {
		p := strings.TrimPrefix(sink, "file:")
		if err := os.MkdirAll(dir(p), 0o755); err != nil {
			slog.Error("failed to create audit log directory", "path", dir(p), "error", err)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return &jsonLineLogger{w: os.Stdout}
		}
		return &jsonLineLogger{w: f, file: f}
	}
	return &jsonLineLogger{w: os.Stdout}
}

func dir(p string) string {
	if p == "" {
		return "."
	}
	return filepath.Dir(p)
}
