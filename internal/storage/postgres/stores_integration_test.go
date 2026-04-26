//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/storage/storageerr"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// ---------------------------------------------------------------------------
// AuditStore
// ---------------------------------------------------------------------------

func TestIntegration_AuditStore_StoreAndList(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewAuditStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()

	defer func() {
		db.ExecContext(ctx, "DELETE FROM audit_events WHERE tenant_id = $1", tenantID)
	}()

	t.Run("Store", func(t *testing.T) {
		event := AuditEvent{
			TenantID:  tenantID,
			Action:    "agent.created",
			Actor:     "user-1",
			Detail:    map[string]any{"agent_id": "a1"},
			Timestamp: time.Now().UTC(),
		}
		if err := store.Store(ctx, event); err != nil {
			t.Fatalf("Store: %v", err)
		}
	})

	t.Run("List_returns_stored_event", func(t *testing.T) {
		events, err := store.List(ctx, tenantID, 100)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(events) == 0 {
			t.Fatal("List: expected at least one event")
		}
		found := false
		for _, e := range events {
			if e.Action == "agent.created" && e.Actor == "user-1" {
				found = true
				if e.Detail["agent_id"] != "a1" {
					t.Errorf("Detail agent_id = %v, want a1", e.Detail["agent_id"])
				}
				break
			}
		}
		if !found {
			t.Error("List: did not find stored event")
		}
	})

	t.Run("List_respects_limit", func(t *testing.T) {
		// Store a second event.
		event2 := AuditEvent{
			TenantID:  tenantID,
			Action:    "agent.deleted",
			Actor:     "user-2",
			Timestamp: time.Now().UTC(),
		}
		if err := store.Store(ctx, event2); err != nil {
			t.Fatalf("Store second: %v", err)
		}

		events, err := store.List(ctx, tenantID, 1)
		if err != nil {
			t.Fatalf("List limit=1: %v", err)
		}
		if len(events) != 1 {
			t.Errorf("List limit=1: got %d events, want 1", len(events))
		}
	})

	t.Run("List_empty_tenantID_returns_nil", func(t *testing.T) {
		events, err := store.List(ctx, "", 10)
		if err != nil {
			t.Fatalf("List empty tenant: %v", err)
		}
		if events != nil {
			t.Errorf("expected nil for empty tenantID, got %v", events)
		}
	})

	t.Run("Store_zero_timestamp_defaults", func(t *testing.T) {
		event := AuditEvent{
			TenantID: tenantID,
			Action:   "agent.updated",
			Actor:    "user-3",
			// Timestamp intentionally zero.
		}
		if err := store.Store(ctx, event); err != nil {
			t.Fatalf("Store zero timestamp: %v", err)
		}
		events, err := store.List(ctx, tenantID, 100)
		if err != nil {
			t.Fatalf("List after zero ts: %v", err)
		}
		found := false
		for _, e := range events {
			if e.Action == "agent.updated" && e.Actor == "user-3" {
				found = true
				if e.Timestamp.IsZero() {
					t.Error("expected non-zero timestamp after store")
				}
				break
			}
		}
		if !found {
			t.Error("did not find event with zero timestamp default")
		}
	})
}

// ---------------------------------------------------------------------------
// UsageStore
// ---------------------------------------------------------------------------

func TestIntegration_UsageStore_RecordAndQuery(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewUsageStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()

	defer func() {
		db.ExecContext(ctx, "DELETE FROM usage_records WHERE tenant_id = $1", tenantID)
	}()

	baseTime := time.Date(2026, 3, 11, 14, 0, 0, 0, time.UTC)

	t.Run("Record", func(t *testing.T) {
		if err := store.Record(ctx, tenantID, 100, baseTime); err != nil {
			t.Fatalf("Record: %v", err)
		}
		if err := store.Record(ctx, tenantID, 250, baseTime.Add(15*time.Minute)); err != nil {
			t.Fatalf("Record second: %v", err)
		}
	})

	t.Run("HourlyUsage", func(t *testing.T) {
		total, err := store.HourlyUsage(ctx, tenantID, baseTime)
		if err != nil {
			t.Fatalf("HourlyUsage: %v", err)
		}
		if total != 350 {
			t.Errorf("HourlyUsage = %d, want 350", total)
		}
	})

	t.Run("HourlyUsage_empty_hour", func(t *testing.T) {
		total, err := store.HourlyUsage(ctx, tenantID, baseTime.Add(-24*time.Hour))
		if err != nil {
			t.Fatalf("HourlyUsage empty: %v", err)
		}
		if total != 0 {
			t.Errorf("HourlyUsage empty = %d, want 0", total)
		}
	})

	t.Run("DailyUsage", func(t *testing.T) {
		total, err := store.DailyUsage(ctx, tenantID, baseTime)
		if err != nil {
			t.Fatalf("DailyUsage: %v", err)
		}
		if total != 350 {
			t.Errorf("DailyUsage = %d, want 350", total)
		}
	})

	t.Run("DailyUsage_cross_hour", func(t *testing.T) {
		// Add a record in a different hour but same day.
		if err := store.Record(ctx, tenantID, 50, baseTime.Add(2*time.Hour)); err != nil {
			t.Fatalf("Record cross-hour: %v", err)
		}
		total, err := store.DailyUsage(ctx, tenantID, baseTime)
		if err != nil {
			t.Fatalf("DailyUsage cross-hour: %v", err)
		}
		if total != 400 {
			t.Errorf("DailyUsage cross-hour = %d, want 400", total)
		}

		// Hourly should still be 350 for the original hour.
		hourly, err := store.HourlyUsage(ctx, tenantID, baseTime)
		if err != nil {
			t.Fatalf("HourlyUsage after cross-hour: %v", err)
		}
		if hourly != 350 {
			t.Errorf("HourlyUsage after cross-hour = %d, want 350", hourly)
		}
	})
}

// ---------------------------------------------------------------------------
// MemoryStore
// ---------------------------------------------------------------------------

func TestIntegration_MemoryStore_AppendGetClear(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewMemoryStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()
	agentID := "agent_mem_integ"

	defer func() {
		db.ExecContext(ctx, "DELETE FROM conversation_memory WHERE tenant_id = $1", tenantID)
	}()

	t.Run("Append", func(t *testing.T) {
		msgs := []types.ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		}
		if err := store.Append(ctx, tenantID, agentID, msgs); err != nil {
			t.Fatalf("Append: %v", err)
		}
	})

	t.Run("GetRecent", func(t *testing.T) {
		msgs, err := store.GetRecent(ctx, tenantID, agentID, 10, 0)
		if err != nil {
			t.Fatalf("GetRecent: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("GetRecent: got %d messages, want 2", len(msgs))
		}
		// Should be in chronological order (user first).
		if msgs[0].Role != "user" {
			t.Errorf("first message role = %q, want user", msgs[0].Role)
		}
		if msgs[0].Content != "Hello" {
			t.Errorf("first message content = %q, want Hello", msgs[0].Content)
		}
		if msgs[1].Role != "assistant" {
			t.Errorf("second message role = %q, want assistant", msgs[1].Role)
		}
	})

	t.Run("GetRecent_limits_messages", func(t *testing.T) {
		msgs, err := store.GetRecent(ctx, tenantID, agentID, 1, 0)
		if err != nil {
			t.Fatalf("GetRecent limit=1: %v", err)
		}
		// With maxMessages=1, we get the most recent 1 message.
		if len(msgs) != 1 {
			t.Fatalf("GetRecent limit=1: got %d, want 1", len(msgs))
		}
		if msgs[0].Role != "assistant" {
			t.Errorf("limited message role = %q, want assistant", msgs[0].Role)
		}
	})

	t.Run("GetRecent_empty_tenant_returns_nil", func(t *testing.T) {
		msgs, err := store.GetRecent(ctx, "", agentID, 10, 0)
		if err != nil {
			t.Fatalf("GetRecent empty tenant: %v", err)
		}
		if msgs != nil {
			t.Errorf("expected nil for empty tenant, got %v", msgs)
		}
	})

	t.Run("Append_empty_messages_noop", func(t *testing.T) {
		if err := store.Append(ctx, tenantID, agentID, nil); err != nil {
			t.Fatalf("Append nil: %v", err)
		}
		if err := store.Append(ctx, tenantID, agentID, []types.ChatMessage{}); err != nil {
			t.Fatalf("Append empty: %v", err)
		}
	})

	t.Run("Append_requires_tenantID_and_agentID", func(t *testing.T) {
		msgs := []types.ChatMessage{{Role: "user", Content: "test"}}
		if err := store.Append(ctx, "", agentID, msgs); err == nil {
			t.Error("expected error for empty tenantID")
		}
		if err := store.Append(ctx, tenantID, "", msgs); err == nil {
			t.Error("expected error for empty agentID")
		}
	})

	t.Run("Clear", func(t *testing.T) {
		if err := store.Clear(ctx, tenantID, agentID); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		msgs, err := store.GetRecent(ctx, tenantID, agentID, 10, 0)
		if err != nil {
			t.Fatalf("GetRecent after Clear: %v", err)
		}
		if len(msgs) != 0 {
			t.Errorf("after Clear: got %d messages, want 0", len(msgs))
		}
	})

	t.Run("Clear_requires_tenantID_and_agentID", func(t *testing.T) {
		if err := store.Clear(ctx, "", agentID); err == nil {
			t.Error("expected error for empty tenantID")
		}
		if err := store.Clear(ctx, tenantID, ""); err == nil {
			t.Error("expected error for empty agentID")
		}
	})

	t.Run("Append_with_tool_calls", func(t *testing.T) {
		msgs := []types.ChatMessage{
			{
				Role:      "assistant",
				Content:   "calling tool",
				ToolCalls: []byte(`[{"id":"tc1","type":"function","function":{"name":"search"}}]`),
			},
			{
				Role:       "tool",
				Content:    "result from tool",
				ToolCallID: "tc1",
			},
		}
		if err := store.Append(ctx, tenantID, agentID, msgs); err != nil {
			t.Fatalf("Append with tool calls: %v", err)
		}
		got, err := store.GetRecent(ctx, tenantID, agentID, 10, 0)
		if err != nil {
			t.Fatalf("GetRecent after tool calls: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d messages, want 2", len(got))
		}
		if got[0].ToolCalls == nil {
			t.Error("expected ToolCalls to be preserved")
		}
		if got[1].ToolCallID != "tc1" {
			t.Errorf("ToolCallID = %q, want tc1", got[1].ToolCallID)
		}
	})
}

// ---------------------------------------------------------------------------
// AgentStore — additional edge cases beyond integration_test.go
// ---------------------------------------------------------------------------

func TestIntegration_AgentStore_EdgeCases(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewAgentStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()

	defer func() {
		db.ExecContext(ctx, "DELETE FROM agents WHERE tenant_id = $1", tenantID)
	}()

	t.Run("Get_not_found", func(t *testing.T) {
		_, found, err := store.Get(ctx, tenantID, "nonexistent")
		if err != nil {
			t.Fatalf("Get not found: %v", err)
		}
		if found {
			t.Error("expected not found")
		}
	})

	t.Run("Get_empty_params_returns_not_found", func(t *testing.T) {
		_, found, err := store.Get(ctx, "", "some-agent")
		if err != nil {
			t.Fatalf("Get empty tenant: %v", err)
		}
		if found {
			t.Error("expected not found for empty tenant")
		}

		_, found, err = store.Get(ctx, tenantID, "")
		if err != nil {
			t.Fatalf("Get empty agent: %v", err)
		}
		if found {
			t.Error("expected not found for empty agent")
		}
	})

	t.Run("List_empty_tenant_returns_nil", func(t *testing.T) {
		agents, err := store.List(ctx, "")
		if err != nil {
			t.Fatalf("List empty tenant: %v", err)
		}
		if agents != nil {
			t.Errorf("expected nil for empty tenant, got %v", agents)
		}
	})

	t.Run("Delete_nonexistent_returns_ErrAgentNotFound", func(t *testing.T) {
		err := store.Delete(ctx, tenantID, "does-not-exist")
		if err != storageerr.ErrAgentNotFound {
			t.Errorf("Delete nonexistent: got %v, want ErrAgentNotFound", err)
		}
	})

	t.Run("Save_upsert_creates_then_updates", func(t *testing.T) {
		now := time.Now().UTC().Format(time.RFC3339)
		agent := types.Agent{
			AgentID:     "agent-upsert",
			TenantID:    tenantID,
			Name:        "Original",
			Description: "test upsert",
			Version:     "1.0",
			Status:      "active",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		// Save (insert)
		if err := store.Save(ctx, agent); err != nil {
			t.Fatalf("Save insert: %v", err)
		}
		got, found, err := store.Get(ctx, tenantID, "agent-upsert")
		if err != nil {
			t.Fatalf("Get after upsert insert: %v", err)
		}
		if !found {
			t.Fatal("expected agent after upsert insert")
		}
		if got.Name != "Original" {
			t.Errorf("name = %q, want Original", got.Name)
		}

		// Save (update)
		agent.Name = "Updated"
		agent.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := store.Save(ctx, agent); err != nil {
			t.Fatalf("Save update: %v", err)
		}
		got, _, err = store.Get(ctx, tenantID, "agent-upsert")
		if err != nil {
			t.Fatalf("Get after upsert update: %v", err)
		}
		if got.Name != "Updated" {
			t.Errorf("name after update = %q, want Updated", got.Name)
		}
	})

	t.Run("Create_with_config", func(t *testing.T) {
		now := time.Now().UTC().Format(time.RFC3339)
		cfg := &types.AgentConfig{
			SystemPrompt: "You are a helpful assistant",
			ModelID:      "gpt-4",
			MaxSteps:     10,
		}
		agent := types.Agent{
			AgentID:   "agent-with-config",
			TenantID:  tenantID,
			Name:      "Configured Agent",
			Version:   "1.0",
			Status:    "active",
			Config:    cfg,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := store.Create(ctx, agent); err != nil {
			t.Fatalf("Create with config: %v", err)
		}
		got, found, err := store.Get(ctx, tenantID, "agent-with-config")
		if err != nil {
			t.Fatalf("Get with config: %v", err)
		}
		if !found {
			t.Fatal("agent with config not found")
		}
		if got.Config == nil {
			t.Fatal("expected non-nil config")
		}
		if got.Config.ModelID != "gpt-4" {
			t.Errorf("config model = %q, want gpt-4", got.Config.ModelID)
		}
		if got.Config.MaxSteps != 10 {
			t.Errorf("config max_steps = %d, want 10", got.Config.MaxSteps)
		}
	})
}

// ---------------------------------------------------------------------------
// KVStore — additional edge cases beyond integration_test.go
// ---------------------------------------------------------------------------

func TestIntegration_KVStore_EdgeCases(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewKVStoreFromDB(db, 65536, 1000)
	ctx := context.Background()
	tenantID := "test_" + t.Name()
	agentID := "agent_kv_edge"

	defer func() {
		db.ExecContext(ctx, "DELETE FROM kv_store WHERE tenant_id = $1", tenantID)
	}()

	t.Run("Get_not_found", func(t *testing.T) {
		_, found, err := store.Get(ctx, tenantID, agentID, "nonexistent")
		if err != nil {
			t.Fatalf("Get not found: %v", err)
		}
		if found {
			t.Error("expected not found")
		}
	})

	t.Run("Set_empty_tenantID_errors", func(t *testing.T) {
		if err := store.Set(ctx, "", agentID, "k", "v"); err == nil {
			t.Error("expected error for empty tenantID")
		}
	})

	t.Run("Set_empty_agentID_errors", func(t *testing.T) {
		if err := store.Set(ctx, tenantID, "", "k", "v"); err == nil {
			t.Error("expected error for empty agentID")
		}
	})

	t.Run("Set_invalid_key_errors", func(t *testing.T) {
		if err := store.Set(ctx, tenantID, agentID, "", "v"); err == nil {
			t.Error("expected error for empty key")
		}
		if err := store.Set(ctx, tenantID, agentID, "invalid key with spaces", "v"); err == nil {
			t.Error("expected error for key with spaces")
		}
	})

	t.Run("Get_invalid_key_errors", func(t *testing.T) {
		_, _, err := store.Get(ctx, tenantID, agentID, "")
		if err == nil {
			t.Error("expected error for empty key in Get")
		}
	})

	t.Run("Delete_nonexistent_no_error", func(t *testing.T) {
		// Delete of nonexistent key should not error (no rows affected is OK).
		if err := store.Delete(ctx, tenantID, agentID, "nope"); err != nil {
			t.Errorf("Delete nonexistent: %v", err)
		}
	})

	t.Run("ListKeys_empty_returns_nil", func(t *testing.T) {
		keys, err := store.ListKeys(ctx, "", agentID)
		if err != nil {
			t.Fatalf("ListKeys empty tenant: %v", err)
		}
		if keys != nil {
			t.Errorf("expected nil for empty tenant, got %v", keys)
		}
	})

	t.Run("Value_size_limit", func(t *testing.T) {
		smallStore := NewKVStoreFromDB(db, 10, 1000)
		err := smallStore.Set(ctx, tenantID, agentID, "bigval", "this value is way too long for limit of 10")
		if err == nil {
			t.Error("expected error for oversized value")
		}
	})
}

// ---------------------------------------------------------------------------
// RunStore — additional edge cases beyond integration_test.go
// ---------------------------------------------------------------------------

func TestIntegration_RunStore_EdgeCases(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewRunStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()

	defer func() {
		db.ExecContext(ctx, "DELETE FROM runs WHERE tenant_id = $1", tenantID)
	}()

	t.Run("Get_not_found", func(t *testing.T) {
		_, found, err := store.Get(ctx, tenantID, "nonexistent-run")
		if err != nil {
			t.Fatalf("Get not found: %v", err)
		}
		if found {
			t.Error("expected not found")
		}
	})

	t.Run("Get_empty_params_returns_not_found", func(t *testing.T) {
		_, found, err := store.Get(ctx, "", "some-run")
		if err != nil {
			t.Fatalf("Get empty tenant: %v", err)
		}
		if found {
			t.Error("expected not found for empty tenant")
		}

		_, found, err = store.Get(ctx, tenantID, "")
		if err != nil {
			t.Fatalf("Get empty runID: %v", err)
		}
		if found {
			t.Error("expected not found for empty runID")
		}
	})

	t.Run("List_empty_tenant_returns_nil", func(t *testing.T) {
		runs, err := store.List(ctx, "")
		if err != nil {
			t.Fatalf("List empty tenant: %v", err)
		}
		if runs != nil {
			t.Errorf("expected nil for empty tenant, got %v", runs)
		}
	})

	t.Run("GetByIdempotencyKey_not_found", func(t *testing.T) {
		_, found, err := store.GetByIdempotencyKey(ctx, tenantID, "no-such-key")
		if err != nil {
			t.Fatalf("GetByIdempotencyKey not found: %v", err)
		}
		if found {
			t.Error("expected not found for nonexistent idempotency key")
		}
	})

	t.Run("GetByIdempotencyKey_empty_params", func(t *testing.T) {
		_, found, err := store.GetByIdempotencyKey(ctx, "", "key")
		if err != nil {
			t.Fatalf("GetByIdempotencyKey empty tenant: %v", err)
		}
		if found {
			t.Error("expected not found for empty tenant")
		}

		_, found, err = store.GetByIdempotencyKey(ctx, tenantID, "")
		if err != nil {
			t.Fatalf("GetByIdempotencyKey empty key: %v", err)
		}
		if found {
			t.Error("expected not found for empty key")
		}
	})

	t.Run("Create_with_error_output", func(t *testing.T) {
		now := time.Now().UTC().Format(time.RFC3339)
		run := types.Run{
			TenantID:    tenantID,
			AgentID:     "agent-err",
			RunID:       "run-with-error",
			Status:      "failed",
			CreatedAt:   now,
			CompletedAt: now,
			EventsURL:   "/events/run-with-error",
			RunOptions:  types.RunOptions{Priority: "normal", TimeoutMs: 5000, MaxSteps: 5},
			Error:       &types.RunError{Code: "timeout", Message: "execution timed out"},
		}
		if err := store.Create(ctx, run); err != nil {
			t.Fatalf("Create with error: %v", err)
		}

		got, found, err := store.Get(ctx, tenantID, "run-with-error")
		if err != nil {
			t.Fatalf("Get with error: %v", err)
		}
		if !found {
			t.Fatal("run with error not found")
		}
		if got.Error == nil {
			t.Fatal("expected non-nil error on run")
		}
		if got.Error.Code != "timeout" {
			t.Errorf("error code = %q, want timeout", got.Error.Code)
		}
		if got.Error.Message != "execution timed out" {
			t.Errorf("error message = %q, want 'execution timed out'", got.Error.Message)
		}
	})

	t.Run("Create_and_GetByIdempotencyKey", func(t *testing.T) {
		now := time.Now().UTC().Format(time.RFC3339)
		run := types.Run{
			TenantID:       tenantID,
			AgentID:        "agent-idemp",
			RunID:          "run-idemp-1",
			Status:         "queued",
			CreatedAt:      now,
			EventsURL:      "/events/run-idemp-1",
			RunOptions:     types.RunOptions{Priority: "normal"},
			IdempotencyKey: "idemp-key-123",
		}
		if err := store.Create(ctx, run); err != nil {
			t.Fatalf("Create idempotent: %v", err)
		}

		got, found, err := store.GetByIdempotencyKey(ctx, tenantID, "idemp-key-123")
		if err != nil {
			t.Fatalf("GetByIdempotencyKey: %v", err)
		}
		if !found {
			t.Fatal("expected run found by idempotency key")
		}
		if got.RunID != "run-idemp-1" {
			t.Errorf("RunID = %q, want run-idemp-1", got.RunID)
		}
	})

	t.Run("ListByAgent_empty_params", func(t *testing.T) {
		runs, err := store.ListByAgent(ctx, "", "agent", 10, "")
		if err != nil {
			t.Fatalf("ListByAgent empty tenant: %v", err)
		}
		if runs != nil {
			t.Errorf("expected nil for empty tenant")
		}

		runs, err = store.ListByAgent(ctx, tenantID, "", 10, "")
		if err != nil {
			t.Fatalf("ListByAgent empty agent: %v", err)
		}
		if runs != nil {
			t.Errorf("expected nil for empty agent")
		}
	})

	t.Run("ListChildRuns_empty_params", func(t *testing.T) {
		runs, err := store.ListChildRuns(ctx, "", "parent")
		if err != nil {
			t.Fatalf("ListChildRuns empty tenant: %v", err)
		}
		if runs != nil {
			t.Errorf("expected nil for empty tenant")
		}

		runs, err = store.ListChildRuns(ctx, tenantID, "")
		if err != nil {
			t.Fatalf("ListChildRuns empty parent: %v", err)
		}
		if runs != nil {
			t.Errorf("expected nil for empty parent")
		}
	})
}
