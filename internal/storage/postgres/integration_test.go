//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/NexixAI/nexixai-agentos/internal/storage/storageerr"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("AGENTOS_DB_DSN")
	if dsn == "" {
		t.Skip("set AGENTOS_DB_DSN to run integration tests")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestIntegration_AgentStore_CRUD(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewAgentStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()
	agentID := "agent_integ_1"
	now := time.Now().UTC().Format(time.RFC3339)

	// Cleanup after test.
	defer func() {
		_ = store.Delete(ctx, tenantID, agentID)
	}()

	agent := types.Agent{
		AgentID:     agentID,
		TenantID:    tenantID,
		Name:        "Test Agent",
		Description: "integration test agent",
		Version:     "1.0",
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	t.Run("Create", func(t *testing.T) {
		if err := store.Create(ctx, agent); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})

	t.Run("Get", func(t *testing.T) {
		got, found, err := store.Get(ctx, tenantID, agentID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !found {
			t.Fatal("Get: expected agent to be found")
		}
		if got.Name != agent.Name {
			t.Errorf("Get name = %q, want %q", got.Name, agent.Name)
		}
		if got.TenantID != tenantID {
			t.Errorf("Get tenant_id = %q, want %q", got.TenantID, tenantID)
		}
	})

	t.Run("List", func(t *testing.T) {
		agents, err := store.List(ctx, tenantID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(agents) == 0 {
			t.Fatal("List: expected at least one agent")
		}
		found := false
		for _, a := range agents {
			if a.AgentID == agentID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("List: agent %q not found in results", agentID)
		}
	})

	t.Run("Update_via_Save", func(t *testing.T) {
		updated := agent
		updated.Name = "Updated Agent"
		updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := store.Save(ctx, updated); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, found, err := store.Get(ctx, tenantID, agentID)
		if err != nil {
			t.Fatalf("Get after Save: %v", err)
		}
		if !found {
			t.Fatal("Get after Save: not found")
		}
		if got.Name != "Updated Agent" {
			t.Errorf("name after Save = %q, want %q", got.Name, "Updated Agent")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := store.Delete(ctx, tenantID, agentID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, found, err := store.Get(ctx, tenantID, agentID)
		if err != nil {
			t.Fatalf("Get after Delete: %v", err)
		}
		if found {
			t.Error("expected agent to be deleted")
		}
	})

	t.Run("Delete_NotFound", func(t *testing.T) {
		err := store.Delete(ctx, tenantID, "nonexistent_agent")
		if err != storageerr.ErrAgentNotFound {
			t.Errorf("Delete nonexistent: got %v, want ErrAgentNotFound", err)
		}
	})
}

func TestIntegration_RunStore_CRUD(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewRunStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()
	agentID := "agent_run_integ"
	runID := "run_integ_1"
	now := time.Now().UTC().Format(time.RFC3339)

	// Cleanup after test.
	defer func() {
		db.ExecContext(ctx, "DELETE FROM runs WHERE tenant_id = $1", tenantID)
	}()

	run := types.Run{
		TenantID:  tenantID,
		AgentID:   agentID,
		RunID:     runID,
		Status:    "queued",
		CreatedAt: now,
		EventsURL: "/events/" + runID,
		RunOptions: types.RunOptions{
			Priority:  "normal",
			TimeoutMs: 30000,
			MaxSteps:  10,
		},
	}

	t.Run("Create", func(t *testing.T) {
		if err := store.Create(ctx, run); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})

	t.Run("Get", func(t *testing.T) {
		got, found, err := store.Get(ctx, tenantID, runID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !found {
			t.Fatal("Get: expected run to be found")
		}
		if got.Status != "queued" {
			t.Errorf("Get status = %q, want %q", got.Status, "queued")
		}
		if got.RunOptions.MaxSteps != 10 {
			t.Errorf("Get max_steps = %d, want 10", got.RunOptions.MaxSteps)
		}
	})

	t.Run("List", func(t *testing.T) {
		runs, err := store.List(ctx, tenantID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(runs) == 0 {
			t.Fatal("List: expected at least one run")
		}
		found := false
		for _, r := range runs {
			if r.RunID == runID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("List: run %q not in results", runID)
		}
	})

	t.Run("Save_updates", func(t *testing.T) {
		updated := run
		updated.Status = "completed"
		updated.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		updated.Output = &types.RunOutput{Type: "text", Text: "done"}
		if err := store.Save(ctx, updated); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, found, err := store.Get(ctx, tenantID, runID)
		if err != nil {
			t.Fatalf("Get after Save: %v", err)
		}
		if !found {
			t.Fatal("Get after Save: not found")
		}
		if got.Status != "completed" {
			t.Errorf("status after Save = %q, want %q", got.Status, "completed")
		}
		if got.Output == nil || got.Output.Text != "done" {
			t.Errorf("output after Save = %v, want text=done", got.Output)
		}
	})
}

func TestIntegration_KVStore_CRUD(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewKVStoreFromDB(db, 65536, 1000)
	ctx := context.Background()
	tenantID := "test_" + t.Name()
	agentID := "agent_kv_integ"

	// Cleanup after test.
	defer func() {
		db.ExecContext(ctx, "DELETE FROM kv_store WHERE tenant_id = $1", tenantID)
	}()

	t.Run("Set_and_Get", func(t *testing.T) {
		if err := store.Set(ctx, tenantID, agentID, "mykey", "myvalue"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		val, found, err := store.Get(ctx, tenantID, agentID, "mykey")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !found {
			t.Fatal("Get: key not found")
		}
		if val != "myvalue" {
			t.Errorf("Get = %q, want %q", val, "myvalue")
		}
	})

	t.Run("Set_overwrites", func(t *testing.T) {
		if err := store.Set(ctx, tenantID, agentID, "mykey", "updated"); err != nil {
			t.Fatalf("Set overwrite: %v", err)
		}
		val, found, err := store.Get(ctx, tenantID, agentID, "mykey")
		if err != nil {
			t.Fatalf("Get after overwrite: %v", err)
		}
		if !found {
			t.Fatal("key not found after overwrite")
		}
		if val != "updated" {
			t.Errorf("Get after overwrite = %q, want %q", val, "updated")
		}
	})

	t.Run("ListKeys", func(t *testing.T) {
		// Set a second key.
		if err := store.Set(ctx, tenantID, agentID, "anotherkey", "val2"); err != nil {
			t.Fatalf("Set second key: %v", err)
		}
		keys, err := store.ListKeys(ctx, tenantID, agentID)
		if err != nil {
			t.Fatalf("ListKeys: %v", err)
		}
		if len(keys) < 2 {
			t.Fatalf("ListKeys: got %d keys, want >= 2", len(keys))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := store.Delete(ctx, tenantID, agentID, "mykey"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, found, err := store.Get(ctx, tenantID, agentID, "mykey")
		if err != nil {
			t.Fatalf("Get after Delete: %v", err)
		}
		if found {
			t.Error("key still found after Delete")
		}
	})
}

func TestIntegration_EventLogStore_AppendAndGet(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewEventLogStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()
	agentID := "agent_evt_integ"
	runID := "run_evt_integ_1"

	// Cleanup after test.
	defer func() {
		db.ExecContext(ctx, "DELETE FROM event_log WHERE tenant_id = $1", tenantID)
	}()

	events := []types.EventEnvelope{
		{
			Event: types.Event{
				EventID:  "evt_1_" + t.Name(),
				Sequence: 1,
				Type:     "run.started",
				TenantID: tenantID,
				AgentID:  agentID,
				RunID:    runID,
				Payload:  map[string]any{"status": "started"},
			},
		},
		{
			Event: types.Event{
				EventID:  "evt_2_" + t.Name(),
				Sequence: 2,
				Type:     "run.step",
				TenantID: tenantID,
				AgentID:  agentID,
				RunID:    runID,
				Payload:  map[string]any{"step": float64(1)},
			},
		},
		{
			Event: types.Event{
				EventID:  "evt_3_" + t.Name(),
				Sequence: 3,
				Type:     "run.completed",
				TenantID: tenantID,
				AgentID:  agentID,
				RunID:    runID,
				Payload:  map[string]any{"status": "completed"},
			},
		},
	}

	t.Run("Append", func(t *testing.T) {
		for i, e := range events {
			if err := store.Append(ctx, e); err != nil {
				t.Fatalf("Append event %d: %v", i, err)
			}
		}
	})

	t.Run("QueryFromSequence_all", func(t *testing.T) {
		got, err := store.QueryFromSequence(ctx, tenantID, runID, 0)
		if err != nil {
			t.Fatalf("QueryFromSequence(0): %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("QueryFromSequence(0): got %d events, want 3", len(got))
		}
		if got[0].Event.Type != "run.started" {
			t.Errorf("first event type = %q, want %q", got[0].Event.Type, "run.started")
		}
	})

	t.Run("QueryFromSequence_after_1", func(t *testing.T) {
		got, err := store.QueryFromSequence(ctx, tenantID, runID, 1)
		if err != nil {
			t.Fatalf("QueryFromSequence(1): %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("QueryFromSequence(1): got %d events, want 2", len(got))
		}
		if got[0].Event.Sequence != 2 {
			t.Errorf("first event sequence = %d, want 2", got[0].Event.Sequence)
		}
	})
}
