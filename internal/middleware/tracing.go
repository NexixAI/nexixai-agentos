package middleware

import (
	"fmt"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// TracingMiddleware returns HTTP middleware that creates OpenTelemetry spans
// for each incoming request. When no tracer provider is configured, this
// adds zero overhead (no-op spans).
func TracingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tracer := otel.Tracer("agentos")
			propagator := otel.GetTextMapPropagator()

			// Extract trace context from incoming headers.
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPMethod(r.Method),
					semconv.HTTPRoute(r.URL.Path),
				),
			)
			defer span.End()

			// Set tenant_id attribute if available.
			ac := auth.FromRequest(r)
			if ac.TenantID != "" {
				span.SetAttributes(attribute.String("tenant_id", ac.TenantID))
			}

			// Inject trace context into response headers.
			propagator.Inject(ctx, propagation.HeaderCarrier(w.Header()))

			rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			span.SetAttributes(semconv.HTTPStatusCode(rec.statusCode))

			if rec.statusCode >= 500 {
				span.SetStatus(codes.Error, "server error")
				slog.DebugContext(ctx, "request completed with server error",
					"status", rec.statusCode, "path", r.URL.Path)
			}
		})
	}
}
