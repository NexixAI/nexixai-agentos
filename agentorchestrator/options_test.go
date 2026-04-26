package agentorchestrator

import (
	"testing"
)

// Package-local tests for the v11.3 ServerOption API. These exercise only
// option application through newStorageLayer's unexported surface, avoiding
// the heavier Server construction path that requires storage + audit wiring.

func TestWithServerDefaultModel_SetsField(t *testing.T) {
	var opts serverOptions
	WithServerDefaultModel("Qwen/Qwen3.6-35B-A3B")(&opts)
	if opts.defaultModel == nil {
		t.Fatal("expected defaultModel to be set, got nil")
	}
	if *opts.defaultModel != "Qwen/Qwen3.6-35B-A3B" {
		t.Errorf("got %q, want Qwen/Qwen3.6-35B-A3B", *opts.defaultModel)
	}
}

func TestServerOptions_NoOptsLeavesDefaultNil(t *testing.T) {
	var opts serverOptions
	for _, o := range []ServerOption{} {
		o(&opts)
	}
	if opts.defaultModel != nil {
		t.Errorf("expected nil when no options applied, got %v", opts.defaultModel)
	}
}

func TestServerOptions_LastOptionWins(t *testing.T) {
	var opts serverOptions
	WithServerDefaultModel("first")(&opts)
	WithServerDefaultModel("second")(&opts)
	if opts.defaultModel == nil || *opts.defaultModel != "second" {
		t.Errorf("expected last-option-wins semantics, got %v", opts.defaultModel)
	}
}

// TestWithServerDefaultModel_EmptyString documents that passing an empty
// string is still an explicit opt-in: downstream effectiveDefaultModel becomes
// "". Callers are responsible for avoiding this — use a nil option (don't call
// the function) to inherit the env-derived default.
func TestWithServerDefaultModel_EmptyString(t *testing.T) {
	var opts serverOptions
	WithServerDefaultModel("")(&opts)
	if opts.defaultModel == nil {
		t.Fatal("empty-string override should still set defaultModel (non-nil pointer)")
	}
	if *opts.defaultModel != "" {
		t.Errorf("got %q, want empty string", *opts.defaultModel)
	}
}
