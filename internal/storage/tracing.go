package storage

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TraceStorageOp starts a span for a storage operation.
// The caller must call span.End() when the operation completes.
// Records operation type (get/put/list/delete) and table name.
func TraceStorageOp(ctx context.Context, op, table string) (context.Context, trace.Span) {
	tracer := otel.Tracer("agentos")
	ctx, span := tracer.Start(ctx, "storage."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("storage.operation", op),
			attribute.String("storage.table", table),
		),
	)
	return ctx, span
}
