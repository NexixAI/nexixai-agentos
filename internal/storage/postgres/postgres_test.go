package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestMarshalNullable_Nil(t *testing.T) {
	b, err := marshalNullable(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != nil {
		t.Fatalf("expected nil bytes for nil input, got %q", string(b))
	}
}

func TestMarshalNullable_NonNil(t *testing.T) {
	out := &types.RunOutput{Type: "text", Text: "hello"}
	b, err := marshalNullable(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b == nil {
		t.Fatal("expected non-nil bytes for non-nil input")
	}
	var decoded types.RunOutput
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Text != "hello" {
		t.Fatalf("expected text=hello, got %q", decoded.Text)
	}
}

func TestUnmarshalRunJSON_EmptyFields(t *testing.T) {
	var r types.Run
	err := unmarshalRunJSON(&r, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Output != nil {
		t.Fatal("expected nil Output for nil JSON")
	}
	if r.Error != nil {
		t.Fatal("expected nil Error for nil JSON")
	}
}

func TestUnmarshalRunJSON_WithData(t *testing.T) {
	optJSON := []byte(`{"priority":"high","timeout_ms":5000,"max_steps":10}`)
	outJSON := []byte(`{"type":"text","text":"result"}`)
	errJSON := []byte(`{"code":"E001","message":"something failed"}`)

	var r types.Run
	if err := unmarshalRunJSON(&r, optJSON, outJSON, errJSON); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.RunOptions.Priority != "high" {
		t.Fatalf("expected priority=high, got %q", r.RunOptions.Priority)
	}
	if r.RunOptions.TimeoutMs != 5000 {
		t.Fatalf("expected timeout_ms=5000, got %d", r.RunOptions.TimeoutMs)
	}
	if r.Output == nil || r.Output.Text != "result" {
		t.Fatal("output not decoded correctly")
	}
	if r.Error == nil || r.Error.Code != "E001" {
		t.Fatal("error not decoded correctly")
	}
}

func TestAuditEventJSON(t *testing.T) {
	e := AuditEvent{
		TenantID:  "t1",
		Action:    "run.created",
		Actor:     "user@example.com",
		Detail:    map[string]any{"run_id": "r1"},
		Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded AuditEvent
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.TenantID != "t1" {
		t.Fatalf("expected tenant_id=t1, got %q", decoded.TenantID)
	}
	if decoded.Action != "run.created" {
		t.Fatalf("expected action=run.created, got %q", decoded.Action)
	}
}

func TestMigrateSQL_NotEmpty(t *testing.T) {
	// Verify that Migrate is callable (compile check).
	// Actual execution requires a database; see integration tests.
	_ = Migrate
}

// NOTE: Integration tests that require a running PostgreSQL instance should be
// placed in a separate file with a //go:build integration constraint at the top.
// Run with: go test -tags integration -run TestIntegration ./internal/storage/postgres/
