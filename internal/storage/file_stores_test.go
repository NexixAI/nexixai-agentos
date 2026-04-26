package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// ---------------------------------------------------------------------------
// FileRunStore tests
// ---------------------------------------------------------------------------

func TestFileRunStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	run := makeRun("tenant1", "run1")
	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok, err := store.Get(ctx, "tenant1", "run1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected run to be found")
	}
	if got.RunID != "run1" || got.TenantID != "tenant1" {
		t.Fatalf("unexpected run: %+v", got)
	}
}

func TestFileRunStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	got, ok, err := store.Get(ctx, "tenant1", "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
	if got.RunID != "" {
		t.Fatalf("expected zero value Run, got %+v", got)
	}
}

func TestFileRunStore_GetEmptyArgs(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	_, ok, err := store.Get(ctx, "", "run1")
	if err != nil {
		t.Fatalf("Get with empty tenantID: %v", err)
	}
	if ok {
		t.Fatal("expected not found for empty tenantID")
	}

	_, ok, err = store.Get(ctx, "tenant1", "")
	if err != nil {
		t.Fatalf("Get with empty runID: %v", err)
	}
	if ok {
		t.Fatal("expected not found for empty runID")
	}
}

func TestFileRunStore_CreateDuplicate(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	run := makeRun("tenant1", "run1")
	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, run); !errors.Is(err, ErrRunExists) {
		t.Fatalf("expected ErrRunExists, got %v", err)
	}
}

func TestFileRunStore_CreateInvalid(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	if err := store.Create(ctx, types.Run{}); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("expected ErrInvalidRun for empty run, got %v", err)
	}
	if err := store.Create(ctx, types.Run{TenantID: "t"}); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("expected ErrInvalidRun for missing RunID, got %v", err)
	}
}

func TestFileRunStore_SaveUpdatesExisting(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	run := makeRun("tenant1", "run1")
	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create: %v", err)
	}

	run.Status = "completed"
	run.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	if err := store.Save(ctx, run); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, _ := store.Get(ctx, "tenant1", "run1")
	if !ok {
		t.Fatal("run not found after Save")
	}
	if got.Status != "completed" {
		t.Fatalf("expected status=completed, got %s", got.Status)
	}
}

func TestFileRunStore_List(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	// Create runs for two tenants
	for _, id := range []string{"run_a", "run_b", "run_c"} {
		r := makeRun("tenant1", id)
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	other := makeRun("tenant2", "run_other")
	if err := store.Create(ctx, other); err != nil {
		t.Fatalf("Create other: %v", err)
	}

	runs, err := store.List(ctx, "tenant1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs for tenant1, got %d", len(runs))
	}
	for _, r := range runs {
		if r.TenantID != "tenant1" {
			t.Fatalf("tenant isolation violated: got %s", r.TenantID)
		}
	}
}

func TestFileRunStore_ListEmpty(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	runs, err := store.List(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected empty list, got %d", len(runs))
	}
}

func TestFileRunStore_ListEmptyTenant(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	runs, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if runs != nil {
		t.Fatalf("expected nil for empty tenant, got %v", runs)
	}
}

func TestFileRunStore_Persistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.json")

	store, err := NewFileRunStore(path)
	if err != nil {
		t.Fatalf("NewFileRunStore: %v", err)
	}

	run := makeRun("tenant1", "run1")
	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Reload from same file
	store2, err := NewFileRunStore(path)
	if err != nil {
		t.Fatalf("NewFileRunStore reload: %v", err)
	}
	got, ok, _ := store2.Get(ctx, "tenant1", "run1")
	if !ok {
		t.Fatal("run not found after reload")
	}
	if got.AgentID != "agt_test" {
		t.Fatalf("unexpected AgentID after reload: %s", got.AgentID)
	}
}

func TestFileRunStore_GetByIdempotencyKeyNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestRunStore(t)

	_, ok, err := store.GetByIdempotencyKey(ctx, "tenant1", "no-such-key")
	if err != nil {
		t.Fatalf("GetByIdempotencyKey: %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

// ---------------------------------------------------------------------------
// FileKVStore tests
// ---------------------------------------------------------------------------

func TestFileKVStore_SetAndGet(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	if err := store.Set(ctx, "t1", "a1", "mykey", "myvalue"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, ok, err := store.Get(ctx, "t1", "a1", "mykey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if val != "myvalue" {
		t.Fatalf("expected myvalue, got %s", val)
	}
}

func TestFileKVStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	val, ok, err := store.Get(ctx, "t1", "a1", "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
	if val != "" {
		t.Fatalf("expected empty string, got %s", val)
	}
}

func TestFileKVStore_SetOverwrite(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	if err := store.Set(ctx, "t1", "a1", "k", "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := store.Set(ctx, "t1", "a1", "k", "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	val, ok, _ := store.Get(ctx, "t1", "a1", "k")
	if !ok || val != "v2" {
		t.Fatalf("expected v2, got ok=%v val=%s", ok, val)
	}
}

func TestFileKVStore_Delete(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	if err := store.Set(ctx, "t1", "a1", "k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Delete(ctx, "t1", "a1", "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok, err := store.Get(ctx, "t1", "a1", "k")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestFileKVStore_DeleteNonexistent(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	// Deleting a key that doesn't exist should not error
	if err := store.Delete(ctx, "t1", "a1", "nope"); err != nil {
		t.Fatalf("Delete nonexistent: %v", err)
	}
}

func TestFileKVStore_ListKeys(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	for _, k := range []string{"charlie", "alpha", "bravo"} {
		if err := store.Set(ctx, "t1", "a1", k, "val"); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}

	keys, err := store.ListKeys(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	// ListKeys returns sorted keys
	if keys[0] != "alpha" || keys[1] != "bravo" || keys[2] != "charlie" {
		t.Fatalf("expected sorted keys, got %v", keys)
	}
}

func TestFileKVStore_ListKeysEmpty(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	keys, err := store.ListKeys(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected empty list, got %v", keys)
	}
}

func TestFileKVStore_ListKeysEmptyArgs(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	keys, err := store.ListKeys(ctx, "", "a1")
	if err != nil {
		t.Fatalf("ListKeys empty tenant: %v", err)
	}
	if keys != nil {
		t.Fatalf("expected nil for empty tenant, got %v", keys)
	}
}

func TestFileKVStore_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	if err := store.Set(ctx, "t1", "a1", "k", "v1"); err != nil {
		t.Fatalf("Set t1: %v", err)
	}
	if err := store.Set(ctx, "t2", "a1", "k", "v2"); err != nil {
		t.Fatalf("Set t2: %v", err)
	}

	val, ok, _ := store.Get(ctx, "t1", "a1", "k")
	if !ok || val != "v1" {
		t.Fatalf("expected v1 for t1, got %s", val)
	}
	val, ok, _ = store.Get(ctx, "t2", "a1", "k")
	if !ok || val != "v2" {
		t.Fatalf("expected v2 for t2, got %s", val)
	}
}

func TestFileKVStore_InvalidKey(t *testing.T) {
	ctx := context.Background()
	store := newTestKVStore(t)

	err := store.Set(ctx, "t1", "a1", "bad key!", "v")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestFileKVStore_MaxKeysEnforced(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileKVStore(dir, 0, 2) // max 2 keys
	if err != nil {
		t.Fatalf("NewFileKVStore: %v", err)
	}

	if err := store.Set(ctx, "t1", "a1", "k1", "v"); err != nil {
		t.Fatalf("Set k1: %v", err)
	}
	if err := store.Set(ctx, "t1", "a1", "k2", "v"); err != nil {
		t.Fatalf("Set k2: %v", err)
	}
	// Third key should fail
	if err := store.Set(ctx, "t1", "a1", "k3", "v"); !errors.Is(err, types.ErrMaxKeysExceeded) {
		t.Fatalf("expected ErrMaxKeysExceeded, got %v", err)
	}
	// Updating existing key should succeed
	if err := store.Set(ctx, "t1", "a1", "k1", "updated"); err != nil {
		t.Fatalf("update existing key: %v", err)
	}
}

func TestFileKVStore_ValueTooLarge(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileKVStore(dir, 10, 0) // max 10 bytes
	if err != nil {
		t.Fatalf("NewFileKVStore: %v", err)
	}

	err = store.Set(ctx, "t1", "a1", "k", strings.Repeat("x", 11))
	if err == nil {
		t.Fatal("expected error for value too large")
	}
}

// ---------------------------------------------------------------------------
// FileMemoryStore tests
// ---------------------------------------------------------------------------

func TestFileMemoryStore_AppendAndGetRecent(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	msgs := []types.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	if err := store.Append(ctx, "t1", "a1", msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := store.GetRecent(ctx, "t1", "a1", 10, 0)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Fatalf("unexpected first message: %+v", got[0])
	}
}

func TestFileMemoryStore_GetRecentEmpty(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	got, err := store.GetRecent(ctx, "t1", "a1", 10, 0)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty memory, got %v", got)
	}
}

func TestFileMemoryStore_GetRecentEmptyArgs(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	got, err := store.GetRecent(ctx, "", "a1", 10, 0)
	if err != nil {
		t.Fatalf("GetRecent empty tenant: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty tenant, got %v", got)
	}
}

func TestFileMemoryStore_AppendAccumulates(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	batch1 := []types.ChatMessage{{Role: "user", Content: "first"}}
	batch2 := []types.ChatMessage{{Role: "assistant", Content: "second"}}

	if err := store.Append(ctx, "t1", "a1", batch1); err != nil {
		t.Fatalf("Append batch1: %v", err)
	}
	if err := store.Append(ctx, "t1", "a1", batch2); err != nil {
		t.Fatalf("Append batch2: %v", err)
	}

	got, err := store.GetRecent(ctx, "t1", "a1", 10, 0)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "first" || got[1].Content != "second" {
		t.Fatalf("messages out of order: %+v", got)
	}
}

func TestFileMemoryStore_GetRecentMaxMessages(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	var msgs []types.ChatMessage
	for i := 0; i < 10; i++ {
		msgs = append(msgs, types.ChatMessage{Role: "user", Content: strings.Repeat("x", 40)})
	}
	if err := store.Append(ctx, "t1", "a1", msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := store.GetRecent(ctx, "t1", "a1", 3, 0)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
}

func TestFileMemoryStore_GetRecentMaxTokens(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	// Each message has 10 words -> EstimateTokens = int(10*1.3) = 13 tokens
	sentence := "the quick brown fox jumps over the lazy dog today"
	var msgs []types.ChatMessage
	for i := 0; i < 5; i++ {
		msgs = append(msgs, types.ChatMessage{Role: "user", Content: sentence})
	}
	if err := store.Append(ctx, "t1", "a1", msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// 13 tokens per message, maxTokens=30 should yield ~2 messages
	got, err := store.GetRecent(ctx, "t1", "a1", 0, 30)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(got) < 1 || len(got) > 3 {
		t.Fatalf("expected 1-3 messages for maxTokens=30, got %d", len(got))
	}
}

func TestFileMemoryStore_Clear(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	msgs := []types.ChatMessage{{Role: "user", Content: "hello"}}
	if err := store.Append(ctx, "t1", "a1", msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := store.Clear(ctx, "t1", "a1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	got, err := store.GetRecent(ctx, "t1", "a1", 10, 0)
	if err != nil {
		t.Fatalf("GetRecent after Clear: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after clear, got %v", got)
	}
}

func TestFileMemoryStore_ClearNonexistent(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	// Clearing nonexistent memory should not error
	if err := store.Clear(ctx, "t1", "a1"); err != nil {
		t.Fatalf("Clear nonexistent: %v", err)
	}
}

func TestFileMemoryStore_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	store := newTestMemoryStore(t)

	msgs1 := []types.ChatMessage{{Role: "user", Content: "tenant1"}}
	msgs2 := []types.ChatMessage{{Role: "user", Content: "tenant2"}}

	if err := store.Append(ctx, "t1", "a1", msgs1); err != nil {
		t.Fatalf("Append t1: %v", err)
	}
	if err := store.Append(ctx, "t2", "a1", msgs2); err != nil {
		t.Fatalf("Append t2: %v", err)
	}

	got1, _ := store.GetRecent(ctx, "t1", "a1", 10, 0)
	got2, _ := store.GetRecent(ctx, "t2", "a1", 10, 0)

	if len(got1) != 1 || got1[0].Content != "tenant1" {
		t.Fatalf("tenant isolation violated for t1: %+v", got1)
	}
	if len(got2) != 1 || got2[0].Content != "tenant2" {
		t.Fatalf("tenant isolation violated for t2: %+v", got2)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestRunStore(t *testing.T) FullRunStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runs.json")
	store, err := NewFileRunStore(path)
	if err != nil {
		t.Fatalf("NewFileRunStore: %v", err)
	}
	return store
}

func newTestKVStore(t *testing.T) KVStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewFileKVStore(dir, 0, 0)
	if err != nil {
		t.Fatalf("NewFileKVStore: %v", err)
	}
	return store
}

func newTestMemoryStore(t *testing.T) MemoryStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewFileMemoryStore(dir)
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}
	return store
}

func makeRun(tenantID, runID string) types.Run {
	return types.Run{
		TenantID:  tenantID,
		AgentID:   "agt_test",
		RunID:     runID,
		Status:    "queued",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		EventsURL: "/v1/runs/" + runID + "/events",
	}
}
