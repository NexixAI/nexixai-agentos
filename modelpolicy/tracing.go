package modelpolicy

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TraceProviderCall starts a span for a model provider call.
// The caller must call span.End() when the call completes.
// Token counts and latency can be recorded as span events on completion.
func TraceProviderCall(ctx context.Context, provider, model string) (context.Context, trace.Span) {
	tracer := otel.Tracer("agentos")
	ctx, span := tracer.Start(ctx, "provider.call",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("provider", provider),
			attribute.String("model", model),
		),
	)
	return ctx, span
}

// RecordTokenUsage records token counts as a span event.
func RecordTokenUsage(span trace.Span, inputTokens, outputTokens int) {
	span.AddEvent("token_usage",
		trace.WithAttributes(
			attribute.Int("input_tokens", inputTokens),
			attribute.Int("output_tokens", outputTokens),
		),
	)
}
