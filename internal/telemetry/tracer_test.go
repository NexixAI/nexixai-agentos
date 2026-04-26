package telemetry

import (
	"context"
	"os"
	"testing"
)

func TestInitTracer_NoOpWhenEndpointEmpty(t *testing.T) {
	cfg := TracerConfig{
		Endpoint:    "",
		ServiceName: "test-service",
		SampleRate:  1.0,
	}

	shutdown, err := InitTracer(cfg)
	if err != nil {
		t.Fatalf("InitTracer returned error for no-op config: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown function should not be nil")
	}

	// Calling shutdown on no-op should succeed.
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("no-op shutdown returned error: %v", err)
	}
}

func TestLoadTracerConfigFromEnv_Defaults(t *testing.T) {
	// Clear env vars to test defaults.
	os.Unsetenv("AGENTOS_OTEL_ENDPOINT")
	os.Unsetenv("AGENTOS_OTEL_SERVICE_NAME")
	os.Unsetenv("AGENTOS_OTEL_SAMPLE_RATE")

	cfg := LoadTracerConfigFromEnv()

	if cfg.Endpoint != "" {
		t.Errorf("expected empty endpoint, got %q", cfg.Endpoint)
	}
	if cfg.ServiceName != "agentos" {
		t.Errorf("expected service name 'agentos', got %q", cfg.ServiceName)
	}
	if cfg.SampleRate != 0.1 {
		t.Errorf("expected sample rate 0.1, got %f", cfg.SampleRate)
	}
}

func TestLoadTracerConfigFromEnv_CustomValues(t *testing.T) {
	t.Setenv("AGENTOS_OTEL_ENDPOINT", "http://collector:4318")
	t.Setenv("AGENTOS_OTEL_SERVICE_NAME", "my-service")
	t.Setenv("AGENTOS_OTEL_SAMPLE_RATE", "0.5")

	cfg := LoadTracerConfigFromEnv()

	if cfg.Endpoint != "http://collector:4318" {
		t.Errorf("expected endpoint 'http://collector:4318', got %q", cfg.Endpoint)
	}
	if cfg.ServiceName != "my-service" {
		t.Errorf("expected service name 'my-service', got %q", cfg.ServiceName)
	}
	if cfg.SampleRate != 0.5 {
		t.Errorf("expected sample rate 0.5, got %f", cfg.SampleRate)
	}
}

func TestLoadTracerConfigFromEnv_InvalidSampleRate(t *testing.T) {
	t.Setenv("AGENTOS_OTEL_SAMPLE_RATE", "not-a-number")

	cfg := LoadTracerConfigFromEnv()

	if cfg.SampleRate != 0.1 {
		t.Errorf("expected default sample rate 0.1 for invalid input, got %f", cfg.SampleRate)
	}
}
