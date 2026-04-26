package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestMigrate_AgentsRunsKVMemoryEvents(t *testing.T) {
	ctx := context.Background()

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := newTestStores(t, srcDir)
	dst := newTestStores(t, dstDir)

	// Seed source stores.
	seedTestData(t, ctx, src, srcDir)

	paths := MigratorFilePaths{
		AgentDir:  filepath.Join(srcDir, "agents"),
		RunPath:   filepath.Join(srcDir, "runs.json"),
		MemoryDir: filepath.Join(srcDir, "memory"),
		KVDir:     filepath.Join(srcDir, "kv"),
		EventDir:  filepath.Join(srcDir, "event_log"),
	}

	m := NewStorageMigrator(src.stores, dst.stores, paths, false)
	res, err := m.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if len(res.Errors) > 0 {
		t.Fatalf("migration errors: %v", res.Errors)
	}
	if res.Agents != 2 {
		t.Errorf("expected 2 agents migrated, got %d", res.Agents)
	}
	if res.Runs != 2 {
		t.Errorf("expected 2 runs migrated, got %d", res.Runs)
	}
	if res.Memory != 1 {
		t.Errorf("expected 1 memory set migrated, got %d", res.Memory)
	}
	if res.KV != 2 {
		t.Errorf("expected 2 kv pairs migrated, got %d", res.KV)
	}
	if res.Events != 2 {
		t.Errorf("expected 2 events migrated, got %d", res.Events)
	}

	// Verify data in target.
	agent, ok, err := dst.stores.Agents.Get(ctx, "t1", "agent1")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if !ok {
		t.Fatal("expected agent1 in target")
	}
	if agent.Name != "Agent One" {
		t.Errorf("expected agent name 'Agent One', got %q", agent.Name)
	}

	run, ok, err := dst.stores.Runs.Get(ctx, "t1", "run1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok {
		t.Fatal("expected run1 in target")
	}
	if run.AgentID != "agent1" {
		t.Errorf("expected run agent_id 'agent1', got %q", run.AgentID)
	}

	msgs, err := dst.stores.Mem.GetRecent(ctx, "t1", "agent1", 10, 0)
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 memory messages, got %d", len(msgs))
	}

	val, ok, err := dst.stores.KV.Get(ctx, "t1", "agent1", "key1")
	if err != nil {
		t.Fatalf("get kv: %v", err)
	}
	if !ok || val != "value1" {
		t.Errorf("expected kv key1=value1, got ok=%v val=%q", ok, val)
	}

	events, err := dst.stores.Events.QueryFromSequence(ctx, "t1", "run1", 0)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestMigrate_Idempotency(t *testing.T) {
	ctx := context.Background()

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := newTestStores(t, srcDir)
	dst := newTestStores(t, dstDir)

	seedTestData(t, ctx, src, srcDir)

	paths := MigratorFilePaths{
		AgentDir:  filepath.Join(srcDir, "agents"),
		RunPath:   filepath.Join(srcDir, "runs.json"),
		MemoryDir: filepath.Join(srcDir, "memory"),
		KVDir:     filepath.Join(srcDir, "kv"),
		EventDir:  filepath.Join(srcDir, "event_log"),
	}

	// First migration.
	m := NewStorageMigrator(src.stores, dst.stores, paths, false)
	res1, err := m.Migrate(ctx)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if len(res1.Errors) > 0 {
		t.Fatalf("first migration errors: %v", res1.Errors)
	}

	// Second migration — everything should be skipped.
	m2 := NewStorageMigrator(src.stores, dst.stores, paths, false)
	res2, err := m2.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(res2.Errors) > 0 {
		t.Fatalf("second migration errors: %v", res2.Errors)
	}

	if res2.Agents != 0 {
		t.Errorf("expected 0 agents on second migration, got %d", res2.Agents)
	}
	if res2.Runs != 0 {
		t.Errorf("expected 0 runs on second migration, got %d", res2.Runs)
	}
	if res2.Memory != 0 {
		t.Errorf("expected 0 memory on second migration, got %d", res2.Memory)
	}
	// KV keys individually skipped.
	if res2.KV != 0 {
		t.Errorf("expected 0 kv on second migration, got %d", res2.KV)
	}
	if res2.Events != 0 {
		t.Errorf("expected 0 events on second migration, got %d", res2.Events)
	}
	if res2.Skipped == 0 {
		t.Error("expected some skipped records on second migration")
	}

	// Verify data is not duplicated — same counts as after first migration.
	agents, err := dst.stores.Agents.List(ctx, "t1")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents after idempotent migration, got %d", len(agents))
	}
}

func TestMigrate_DryRun(t *testing.T) {
	ctx := context.Background()

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := newTestStores(t, srcDir)
	dst := newTestStores(t, dstDir)

	seedTestData(t, ctx, src, srcDir)

	paths := MigratorFilePaths{
		AgentDir:  filepath.Join(srcDir, "agents"),
		RunPath:   filepath.Join(srcDir, "runs.json"),
		MemoryDir: filepath.Join(srcDir, "memory"),
		KVDir:     filepath.Join(srcDir, "kv"),
		EventDir:  filepath.Join(srcDir, "event_log"),
	}

	m := NewStorageMigrator(src.stores, dst.stores, paths, true)
	res, err := m.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if len(res.Errors) > 0 {
		t.Fatalf("migration errors: %v", res.Errors)
	}

	// Dry-run should count what would be migrated.
	if res.Agents != 2 {
		t.Errorf("expected 2 agents in dry-run, got %d", res.Agents)
	}
	if res.Runs != 2 {
		t.Errorf("expected 2 runs in dry-run, got %d", res.Runs)
	}

	// But nothing should actually be written to target.
	agents, err := dst.stores.Agents.List(ctx, "t1")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents in target after dry-run, got %d", len(agents))
	}

	runs, err := dst.stores.Runs.List(ctx, "t1")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs in target after dry-run, got %d", len(runs))
	}
}

func TestMigrate_EmptySource(t *testing.T) {
	ctx := context.Background()

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create empty stores (no data seeded).
	src := newTestStores(t, srcDir)
	dst := newTestStores(t, dstDir)

	paths := MigratorFilePaths{
		AgentDir:  filepath.Join(srcDir, "agents"),
		RunPath:   filepath.Join(srcDir, "runs.json"),
		MemoryDir: filepath.Join(srcDir, "memory"),
		KVDir:     filepath.Join(srcDir, "kv"),
		EventDir:  filepath.Join(srcDir, "event_log"),
	}

	m := NewStorageMigrator(src.stores, dst.stores, paths, false)
	res, err := m.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if len(res.Errors) > 0 {
		t.Fatalf("migration errors: %v", res.Errors)
	}
	if res.Agents != 0 || res.Runs != 0 || res.Memory != 0 || res.KV != 0 || res.Events != 0 {
		t.Errorf("expected all zeros for empty source, got agents=%d runs=%d memory=%d kv=%d events=%d",
			res.Agents, res.Runs, res.Memory, res.KV, res.Events)
	}
}

func TestMigrate_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := newTestStores(t, srcDir)
	dst := newTestStores(t, dstDir)

	paths := MigratorFilePaths{
		AgentDir:  filepath.Join(srcDir, "agents"),
		RunPath:   filepath.Join(srcDir, "runs.json"),
		MemoryDir: filepath.Join(srcDir, "memory"),
		KVDir:     filepath.Join(srcDir, "kv"),
		EventDir:  filepath.Join(srcDir, "event_log"),
	}

	m := NewStorageMigrator(src.stores, dst.stores, paths, false)
	_, err := m.Migrate(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type testStoreSet struct {
	stores MigratorStores
}

func newTestStores(t *testing.T, baseDir string) testStoreSet {
	t.Helper()

	agentStore, err := NewFileAgentStore(filepath.Join(baseDir, "agents"))
	if err != nil {
		t.Fatalf("NewFileAgentStore: %v", err)
	}

	runStore, err := NewFileRunStore(filepath.Join(baseDir, "runs.json"))
	if err != nil {
		t.Fatalf("NewFileRunStore: %v", err)
	}

	memStore, err := NewFileMemoryStore(filepath.Join(baseDir, "memory"))
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}

	kvStore, err := NewFileKVStore(filepath.Join(baseDir, "kv"), 0, 0)
	if err != nil {
		t.Fatalf("NewFileKVStore: %v", err)
	}

	eventStore, err := NewFileEventLogStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileEventLogStore: %v", err)
	}

	return testStoreSet{
		stores: MigratorStores{
			Agents: agentStore,
			Runs:   runStore,
			Mem:    memStore,
			KV:     kvStore,
			Events: eventStore,
		},
	}
}

func seedTestData(t *testing.T, ctx context.Context, ts testStoreSet, baseDir string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339)

	// Agents.
	agent1 := types.Agent{
		TenantID:  "t1",
		AgentID:   "agent1",
		Name:      "Agent One",
		Version:   "1.0",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}
	agent2 := types.Agent{
		TenantID:  "t1",
		AgentID:   "agent2",
		Name:      "Agent Two",
		Version:   "1.0",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := ts.stores.Agents.Create(ctx, agent1); err != nil {
		t.Fatalf("create agent1: %v", err)
	}
	if err := ts.stores.Agents.Create(ctx, agent2); err != nil {
		t.Fatalf("create agent2: %v", err)
	}

	// Runs.
	run1 := types.Run{
		TenantID:  "t1",
		AgentID:   "agent1",
		RunID:     "run1",
		Status:    "completed",
		CreatedAt: now,
		EventsURL: "/v1/runs/run1/events",
	}
	run2 := types.Run{
		TenantID:  "t1",
		AgentID:   "agent2",
		RunID:     "run2",
		Status:    "completed",
		CreatedAt: now,
		EventsURL: "/v1/runs/run2/events",
	}
	if err := ts.stores.Runs.Create(ctx, run1); err != nil {
		t.Fatalf("create run1: %v", err)
	}
	if err := ts.stores.Runs.Create(ctx, run2); err != nil {
		t.Fatalf("create run2: %v", err)
	}

	// Memory.
	msgs := []types.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	if err := ts.stores.Mem.Append(ctx, "t1", "agent1", msgs); err != nil {
		t.Fatalf("append memory: %v", err)
	}

	// KV.
	if err := ts.stores.KV.Set(ctx, "t1", "agent1", "key1", "value1"); err != nil {
		t.Fatalf("set kv key1: %v", err)
	}
	if err := ts.stores.KV.Set(ctx, "t1", "agent1", "key2", "value2"); err != nil {
		t.Fatalf("set kv key2: %v", err)
	}

	// Events.
	event1 := types.EventEnvelope{
		Event: types.Event{
			EventID:  "evt1",
			Sequence: 1,
			Time:     now,
			Type:     "step.start",
			TenantID: "t1",
			AgentID:  "agent1",
			RunID:    "run1",
		},
	}
	event2 := types.EventEnvelope{
		Event: types.Event{
			EventID:  "evt2",
			Sequence: 2,
			Time:     now,
			Type:     "step.end",
			TenantID: "t1",
			AgentID:  "agent1",
			RunID:    "run1",
		},
	}
	if err := ts.stores.Events.Append(ctx, event1); err != nil {
		t.Fatalf("append event1: %v", err)
	}
	if err := ts.stores.Events.Append(ctx, event2); err != nil {
		t.Fatalf("append event2: %v", err)
	}
}
