package modelpolicy

import (
	"context"
	"testing"
)

func TestTraceProviderCall_NoOp(t *testing.T) {
	// With no tracer configured, should use no-op and not panic.
	ctx, span := TraceProviderCall(context.Background(), "anthropic", "claude-3-opus")
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	if span == nil {
		t.Fatal("span should not be nil")
	}
	// Should not panic.
	span.End()
}

func TestTraceProviderCall_Attributes(t *testing.T) {
	ctx, span := TraceProviderCall(context.Background(), "openai", "gpt-4")
	defer span.End()

	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	if !span.SpanContext().IsValid() {
		// No-op spans have invalid span context, which is expected.
		t.Log("span context is invalid (expected with no-op tracer)")
	}
}

func TestRecordTokenUsage_NoOp(t *testing.T) {
	_, span := TraceProviderCall(context.Background(), "anthropic", "claude-3-opus")
	defer span.End()

	// Should not panic with no-op span.
	RecordTokenUsage(span, 100, 200)
}
