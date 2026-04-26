package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- mock AgentMemoryStore ---

type mockMemoryStore struct {
	mu      sync.Mutex
	entries map[string]*postgres.MemoryEntry // keyed by "agentID|tenantID|key"
	err     error
}

func newMockMemoryStore() *mockMemoryStore {
	return &mockMemoryStore{
		entries: make(map[string]*postgres.MemoryEntry),
	}
}

func memKey(agentID, tenantID, key string) string {
	return agentID + "|" + tenantID + "|" + key
}

func (m *mockMemoryStore) Store(_ context.Context, agentID, tenantID, key, value string, ttl time.Duration, metadata map[string]string) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	var expiresAt *time.Time
	if ttl > 0 {
		t := now.Add(ttl)
		expiresAt = &t
	}

	m.entries[memKey(agentID, tenantID, key)] = &postgres.MemoryEntry{
		Key:       key,
		Value:     value,
		Metadata:  metadata,
		CreatedAt: now,
		ExpiresAt: expiresAt,
		SizeBytes: int64(len(value)),
	}
	return nil
}

func (m *mockMemoryStore) Recall(_ context.Context, agentID, tenantID, key string) (*postgres.MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[memKey(agentID, tenantID, key)]
	if !ok {
		return nil, nil
	}
	if entry.ExpiresAt != nil && time.Now().UTC().After(*entry.ExpiresAt) {
		return nil, nil
	}
	return entry, nil
}

func (m *mockMemoryStore) Search(_ context.Context, agentID, tenantID, query string, limit int) ([]postgres.MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := agentID + "|" + tenantID + "|"
	var results []postgres.MemoryEntry
	for k, entry := range m.entries {
		if len(results) >= limit {
			break
		}
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		results = append(results, *entry)
	}
	return results, nil
}

func (m *mockMemoryStore) Delete(_ context.Context, agentID, tenantID, key string) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, memKey(agentID, tenantID, key))
	return nil
}

func (m *mockMemoryStore) GetUsage(_ context.Context, agentID, tenantID string) (int64, error) {
	if m.err != nil {
		return 0, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := agentID + "|" + tenantID + "|"
	var total int64
	for k, entry := range m.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			total += entry.SizeBytes
		}
	}
	return total, nil
}

func (m *mockMemoryStore) GetThread(_ context.Context, agentID, tenantID, threadID string, limit int) ([]postgres.MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	if threadID == "" {
		return nil, fmt.Errorf("agentmemory: thread_id must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if limit <= 0 {
		limit = 50
	}

	threadPrefix := agentID + "|" + tenantID + "|thread:" + threadID + ":"
	var results []postgres.MemoryEntry
	for k, entry := range m.entries {
		if len(results) >= limit {
			break
		}
		if len(k) >= len(threadPrefix) && k[:len(threadPrefix)] == threadPrefix {
			results = append(results, *entry)
		}
	}
	return results, nil
}

func (m *mockMemoryStore) SemanticSearch(_ context.Context, agentID, tenantID, query string, limit int) ([]postgres.MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	if query == "" {
		return nil, fmt.Errorf("agentmemory: query must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if limit <= 0 {
		limit = 10
	}

	prefix := agentID + "|" + tenantID + "|"
	var results []postgres.MemoryEntry
	for k, entry := range m.entries {
		if len(results) >= limit {
			break
		}
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		// Mock fuzzy: match if key or value contains any 3-char trigram of query.
		if mockFuzzyMatch(entry.Key, query) || mockFuzzyMatch(entry.Value, query) {
			results = append(results, *entry)
		}
	}
	return results, nil
}

// mockFuzzyMatch simulates trigram matching for tests.
func mockFuzzyMatch(target, query string) bool {
	if len(query) < 3 {
		return mockContains(target, query)
	}
	for i := 0; i <= len(query)-3; i++ {
		if mockContains(target, query[i:i+3]) {
			return true
		}
	}
	return false
}

func mockContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// getEntry returns an entry from the mock store for test assertions.
func (m *mockMemoryStore) getEntry(agentID, tenantID, key string) *postgres.MemoryEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[memKey(agentID, tenantID, key)]
}

// --- Test helpers ---

func authCtx(agentID, tenantID string) context.Context {
	return mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  agentID,
		TenantID: tenantID,
	})
}

// --- memory_store tests ---

func TestMemoryStore_WritesViaMockStore(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_store")
	if tool == nil {
		t.Fatal("memory_store not registered")
	}

	ctx := authCtx("agent-1", "tenant-1")
	params, _ := json.Marshal(memoryStoreInput{
		Key:      "greeting",
		Value:    "hello world",
		Metadata: map[string]string{"source": "test"},
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(memoryStoreOutput)
	if !ok {
		t.Fatalf("expected memoryStoreOutput, got %T", result)
	}
	if !out.Stored {
		t.Error("expected stored=true")
	}
	if out.Key != "greeting" {
		t.Errorf("key = %q, want %q", out.Key, "greeting")
	}
	if out.SizeBytes != int64(len("hello world")) {
		t.Errorf("size_bytes = %d, want %d", out.SizeBytes, len("hello world"))
	}

	// Verify the mock store received the entry.
	entry := store.getEntry("agent-1", "tenant-1", "greeting")
	if entry == nil {
		t.Fatal("entry not found in mock store")
	}
	if entry.Value != "hello world" {
		t.Errorf("stored value = %q, want %q", entry.Value, "hello world")
	}
}

func TestMemoryStore_MissingKey_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_store")
	ctx := authCtx("agent-1", "tenant-1")

	params, _ := json.Marshal(memoryStoreInput{
		Key:   "",
		Value: "hello",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
}

func TestMemoryStore_MissingValue_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_store")
	ctx := authCtx("agent-1", "tenant-1")

	params, _ := json.Marshal(memoryStoreInput{
		Key:   "greeting",
		Value: "",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing value, got nil")
	}
}

func TestMemoryStore_MissingAuthContext_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_store")

	params, _ := json.Marshal(memoryStoreInput{
		Key:   "greeting",
		Value: "hello",
	})

	// No auth context.
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
}

func TestMemoryStore_WithTTL(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_store")
	ctx := authCtx("agent-1", "tenant-1")

	ttl := 3600
	params, _ := json.Marshal(memoryStoreInput{
		Key:        "ephemeral",
		Value:      "temp data",
		TTLSeconds: &ttl,
	})

	_, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := store.getEntry("agent-1", "tenant-1", "ephemeral")
	if entry == nil {
		t.Fatal("entry not found")
	}
	if entry.ExpiresAt == nil {
		t.Error("expected non-nil ExpiresAt for TTL entry")
	}
}

// --- memory_recall tests ---

func TestMemoryRecall_ByKey(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	// Pre-populate via mock.
	ctx := authCtx("agent-1", "tenant-1")
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "config", "db=postgres", 0, nil); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_recall")
	if tool == nil {
		t.Fatal("memory_recall not registered")
	}

	key := "config"
	params, _ := json.Marshal(memoryRecallInput{Key: &key})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(memoryRecallOutput)
	if !ok {
		t.Fatalf("expected memoryRecallOutput, got %T", result)
	}
	if out.Count != 1 {
		t.Errorf("count = %d, want 1", out.Count)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(out.Entries))
	}
	if out.Entries[0].Value != "db=postgres" {
		t.Errorf("value = %q, want %q", out.Entries[0].Value, "db=postgres")
	}
}

func TestMemoryRecall_ByKey_NotFound(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_recall")
	ctx := authCtx("agent-1", "tenant-1")

	key := "nonexistent"
	params, _ := json.Marshal(memoryRecallInput{Key: &key})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(memoryRecallOutput)
	if !ok {
		t.Fatalf("expected memoryRecallOutput, got %T", result)
	}
	if out.Count != 0 {
		t.Errorf("count = %d, want 0", out.Count)
	}
}

func TestMemoryRecall_BySearchQuery(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	// Pre-populate.
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "db-config", "postgres://...", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "api-key", "sk-123", 0, nil); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_recall")
	ctx := authCtx("agent-1", "tenant-1")

	query := "config"
	params, _ := json.Marshal(memoryRecallInput{Query: &query})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(memoryRecallOutput)
	if !ok {
		t.Fatalf("expected memoryRecallOutput, got %T", result)
	}
	if out.Count < 1 {
		t.Errorf("expected at least 1 search result, got %d", out.Count)
	}
}

func TestMemoryRecall_NeitherKeyNorQuery_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_recall")
	ctx := authCtx("agent-1", "tenant-1")

	// Neither key nor query.
	params, _ := json.Marshal(memoryRecallInput{})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for neither key nor query, got nil")
	}
}

func TestMemoryRecall_MissingAuthContext_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_recall")

	key := "anything"
	params, _ := json.Marshal(memoryRecallInput{Key: &key})

	// No auth context.
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
}

func TestMemoryTools_ClearanceTier(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		toolName string
		want     mcp.ClearanceTier
	}{
		{"memory_store", "memory_store", mcp.ClearanceInternal},
		{"memory_recall", "memory_recall", mcp.ClearanceInternal},
		{"get_thread", "get_thread", mcp.ClearanceInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool := registry.Get(tc.toolName)
			if tool == nil {
				t.Fatalf("%s not registered", tc.toolName)
			}
			if tool.MinClearance != tc.want {
				t.Errorf("MinClearance = %d, want %d", tool.MinClearance, tc.want)
			}
		})
	}
}

func TestMemoryStore_StoreError_PropagatesError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()
	store.err = fmt.Errorf("database connection lost")

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_store")
	ctx := authCtx("agent-1", "tenant-1")

	params, _ := json.Marshal(memoryStoreInput{
		Key:   "test",
		Value: "data",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error from store, got nil")
	}
}

func TestMemoryRecall_RecallError_PropagatesError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()
	store.err = fmt.Errorf("database connection lost")

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_recall")
	ctx := authCtx("agent-1", "tenant-1")

	key := "test"
	params, _ := json.Marshal(memoryRecallInput{Key: &key})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error from store, got nil")
	}
}

// --- get_thread tests ---

func TestGetThread_ReturnsThreadMessages(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	// Pre-populate thread messages.
	ctx := authCtx("agent-1", "tenant-1")
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "thread:conv1:001", "Hello", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "thread:conv1:002", "World", 0, nil); err != nil {
		t.Fatal(err)
	}
	// Non-thread entry should not appear.
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "config", "value", 0, nil); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_thread")
	if tool == nil {
		t.Fatal("get_thread not registered")
	}

	params, _ := json.Marshal(getThreadInput{ThreadID: "conv1"})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(getThreadOutput)
	if !ok {
		t.Fatalf("expected getThreadOutput, got %T", result)
	}
	if out.ThreadID != "conv1" {
		t.Errorf("thread_id = %q, want %q", out.ThreadID, "conv1")
	}
	if out.Count != 2 {
		t.Errorf("count = %d, want 2", out.Count)
	}
}

func TestGetThread_EmptyThreadID_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_thread")
	ctx := authCtx("agent-1", "tenant-1")

	params, _ := json.Marshal(getThreadInput{ThreadID: ""})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for empty thread_id, got nil")
	}
}

func TestGetThread_MissingAuthContext_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_thread")

	params, _ := json.Marshal(getThreadInput{ThreadID: "conv1"})

	// No auth context.
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
}

func TestGetThread_ClearanceTier(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("get_thread")
	if tool == nil {
		t.Fatal("get_thread not registered")
	}
	if tool.MinClearance != mcp.ClearanceInternal {
		t.Errorf("MinClearance = %d, want %d (ClearanceInternal)", tool.MinClearance, mcp.ClearanceInternal)
	}
}

// --- memory_recall semantic tests ---

func TestMemoryRecall_SemanticSearch(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockMemoryStore()

	if err := RegisterMemoryTools(registry, store); err != nil {
		t.Fatal(err)
	}

	// Pre-populate.
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "database-config", "postgres conn string", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(context.Background(), "agent-1", "tenant-1", "databse-cnfig", "typo postgres entry", 0, nil); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("memory_recall")
	ctx := authCtx("agent-1", "tenant-1")

	query := "database"
	semantic := true
	params, _ := json.Marshal(memoryRecallInput{Query: &query, Semantic: &semantic})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(memoryRecallOutput)
	if !ok {
		t.Fatalf("expected memoryRecallOutput, got %T", result)
	}
	// Both entries should match via fuzzy/trigram matching.
	if out.Count < 2 {
		t.Errorf("expected at least 2 semantic results, got %d", out.Count)
	}
}
