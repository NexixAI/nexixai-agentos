package tools

import (
	"context"
	"testing"
)

func TestTraceToolExec_NoOp(t *testing.T) {
	ctx, span := TraceToolExec(context.Background(), "shell_exec")
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	if span == nil {
		t.Fatal("span should not be nil")
	}
	span.End()
}

func TestTraceToolExec_MultipleTools(t *testing.T) {
	toolNames := []string{"file_read", "http_fetch", "json_extract", "shell_exec"}
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			ctx, span := TraceToolExec(context.Background(), name)
			if ctx == nil {
				t.Fatal("context should not be nil")
			}
			span.End()
		})
	}
}

func TestRecordToolResult_Success(t *testing.T) {
	_, span := TraceToolExec(context.Background(), "file_read")
	// Should not panic.
	RecordToolResult(span, true, "")
	span.End()
}

func TestRecordToolResult_Failure(t *testing.T) {
	_, span := TraceToolExec(context.Background(), "http_fetch")
	// Should not panic.
	RecordToolResult(span, false, "connection refused")
	span.End()
}
