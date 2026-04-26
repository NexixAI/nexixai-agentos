package secrets

import (
	"fmt"
	"os"
	"strings"
)

// Provider loads a secret by name from an external system (e.g., cloud secret manager).
type Provider func(name string) (string, error)

// Loader resolves secrets from env vars, *_FILE paths, then an optional external provider.
// It intentionally never logs secret values; callers can provide a logger for audit/debug messages.
type Loader struct {
	External Provider
	Logger   func(msg string)
}

// NewLoader returns a Loader with optional configuration.
func NewLoader(opts ...Option) *Loader {
	l := &Loader{}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Option configures a Loader.
type Option func(*Loader)

// WithExternal sets an external provider hook.
func WithExternal(p Provider) Option {
	return func(l *Loader) { l.External = p }
}

// WithLogger sets an optional logger for operational messages (no secret values are logged).
func WithLogger(fn func(string)) Option {
	return func(l *Loader) { l.Logger = fn }
}

// Load returns a secret value from env, *_FILE, or the external provider.
// Missing secrets return an empty string and nil error so callers can decide strictness.
func (l *Loader) Load(name string) (string, error) {
	key := strings.TrimSpace(name)
	if key == "" {
		return "", fmt.Errorf("secret name required")
	}
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		l.log("loaded " + key + " from env")
		return v, nil
	}
	if path := strings.TrimSpace(os.Getenv(key + "_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s_FILE read failed: %w", key, err)
		}
		l.log("loaded " + key + " from file")
		return strings.TrimSpace(string(b)), nil
	}
	if l.External != nil {
		v, err := l.External(key)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v != "" {
			l.log("loaded " + key + " from external provider")
		} else {
			l.log("secret " + key + " not found in external provider")
		}
		return v, nil
	}
	l.log("secret " + key + " not set")
	return "", nil
}

// Require returns an error if Load resolves an empty value.
func (l *Loader) Require(name string) (string, error) {
	v, err := l.Load(name)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("secret %s is required but missing", strings.TrimSpace(name))
	}
	return v, nil
}

func (l *Loader) log(msg string) {
	if l == nil || l.Logger == nil {
		return
	}
	l.Logger(msg)
}
