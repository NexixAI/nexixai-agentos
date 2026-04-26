package federation

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TraceFederationForward starts a span for a federation forwarding operation.
// The caller must call span.End() when the operation completes.
// Records target peer; latency is captured automatically by the span.
func TraceFederationForward(ctx context.Context, peer string) (context.Context, trace.Span) {
	tracer := otel.Tracer("agentos")
	ctx, span := tracer.Start(ctx, "federation.forward",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("federation.peer", peer),
		),
	)
	return ctx, span
}
