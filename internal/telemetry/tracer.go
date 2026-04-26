// Package telemetry provides OpenTelemetry tracing setup for AgentOS.
package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// TracerConfig holds configuration for the OpenTelemetry tracer.
type TracerConfig struct {
	Endpoint    string
	ServiceName string
	SampleRate  float64
}

// LoadTracerConfigFromEnv reads tracer configuration from environment variables.
func LoadTracerConfigFromEnv() TracerConfig {
	cfg := TracerConfig{
		Endpoint:    os.Getenv("AGENTOS_OTEL_ENDPOINT"),
		ServiceName: os.Getenv("AGENTOS_OTEL_SERVICE_NAME"),
	}

	if cfg.ServiceName == "" {
		cfg.ServiceName = "agentos"
	}

	cfg.SampleRate = 0.1
	if raw := os.Getenv("AGENTOS_OTEL_SAMPLE_RATE"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			cfg.SampleRate = parsed
		} else {
			slog.Warn("invalid AGENTOS_OTEL_SAMPLE_RATE, using default", "raw", raw, "default", 0.1)
		}
	}

	return cfg
}

// InitTracer initializes the OpenTelemetry tracer. If Endpoint is empty,
// a no-op tracer is used (zero overhead). Returns a shutdown function that
// must be called on application exit.
func InitTracer(cfg TracerConfig) (shutdown func(context.Context) error, err error) {
	if cfg.Endpoint == "" {
		slog.Info("otel tracing disabled (no endpoint configured)")
		return func(context.Context) error { return nil }, nil
	}

	ctx := context.Background()

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion("0.0.1-dev"),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRate)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	slog.Info("otel tracing enabled",
		"endpoint", cfg.Endpoint,
		"service", cfg.ServiceName,
		"sample_rate", cfg.SampleRate,
	)

	return tp.Shutdown, nil
}
