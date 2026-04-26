package tools

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TraceToolExec starts a span for tool execution.
// The caller must call span.End() when execution completes.
// Records tool name; the caller should set success/failure status.
func TraceToolExec(ctx context.Context, toolName string) (context.Context, trace.Span) {
	tracer := otel.Tracer("agentos")
	ctx, span := tracer.Start(ctx, "tool.exec",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("tool.name", toolName),
		),
	)
	return ctx, span
}

// RecordToolResult records the success or failure of a tool execution.
func RecordToolResult(span trace.Span, success bool, errMsg string) {
	span.SetAttributes(attribute.Bool("tool.success", success))
	if !success && errMsg != "" {
		span.AddEvent("tool.error",
			trace.WithAttributes(attribute.String("error.message", errMsg)),
		)
	}
}
