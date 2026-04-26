// Package logging provides structured logging built on log/slog with
// HTTP middleware that propagates request_id and tenant_id through context.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// contextKey is an unexported type used for context keys to avoid collisions.
type contextKey int

const (
	requestIDKey contextKey = iota
	tenantIDKey
)

// Logger is the package-level logger set by InitLogger.
var Logger *slog.Logger

// InitLogger creates a *slog.Logger writing to os.Stdout.
// When format is "json" it uses slog.NewJSONHandler; otherwise
// slog.NewTextHandler. The returned logger is also stored in the
// package-level Logger variable.
func InitLogger(format string) *slog.Logger {
	return initLogger(format, os.Stdout)
}

// initLogger is the internal constructor that accepts an io.Writer,
// making it easy to test without capturing os.Stdout.
func initLogger(format string, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}
	Logger = slog.New(handler)
	return Logger
}

// NewRequestID returns a UUID-v4-formatted random identifier.
func NewRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	// Set version (4) and variant (RFC 4122) bits.
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(buf[0:4]),
		hex.EncodeToString(buf[4:6]),
		hex.EncodeToString(buf[6:8]),
		hex.EncodeToString(buf[8:10]),
		hex.EncodeToString(buf[10:16]),
	)
}

// WithRequestID stores a request ID in the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID extracts the request ID from the context, or returns "".
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// WithTenantID stores a tenant ID in the context.
func WithTenantID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, tenantIDKey, id)
}

// TenantID extracts the tenant ID from the context, or returns "".
func TenantID(ctx context.Context) string {
	v, _ := ctx.Value(tenantIDKey).(string)
	return v
}

// FromContext returns a logger enriched with request_id and tenant_id
// from ctx. If the package-level Logger has not been initialised it
// falls back to slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	l := Logger
	if l == nil {
		l = slog.Default()
	}
	if rid := RequestID(ctx); rid != "" {
		l = l.With("request_id", rid)
	}
	if tid := TenantID(ctx); tid != "" {
		l = l.With("tenant_id", tid)
	}
	return l
}
