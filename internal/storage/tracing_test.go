package storage

import (
	"context"
	"testing"
)

func TestTraceStorageOp_NoOp(t *testing.T) {
	// With no tracer configured, should use no-op and not panic.
	ctx, span := TraceStorageOp(context.Background(), "get", "agents")
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	if span == nil {
		t.Fatal("span should not be nil")
	}
	span.End()
}

func TestTraceStorageOp_Operations(t *testing.T) {
	ops := []struct {
		op    string
		table string
	}{
		{"get", "agents"},
		{"put", "runs"},
		{"list", "events"},
		{"delete", "memories"},
	}

	for _, tc := range ops {
		t.Run(tc.op+"_"+tc.table, func(t *testing.T) {
			ctx, span := TraceStorageOp(context.Background(), tc.op, tc.table)
			if ctx == nil {
				t.Fatal("context should not be nil")
			}
			span.End()
		})
	}
}
