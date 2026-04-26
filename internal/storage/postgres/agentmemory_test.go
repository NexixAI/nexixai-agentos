package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- mockDB layer ---
// We mock at the AgentMemoryStore interface level for unit tests.
// Integration tests against a real Postgres (testcontainers) would use
// the PostgresAgentMemoryStore directly; those live in a //go:build integration file.

// mockAgentMemoryStore implements AgentMemoryStore for unit testing.
type mockAgentMemoryStore struct {
	mu      sync.Mutex
	entries map[string]*MemoryEntry // keyed by "agentID|tenantID|key"
	usage   map[string]int64        // keyed by "agentID|tenantID"
	quota   int64
	err     error
}

func newMockAgentMemoryStore(quota int64) *mockAgentMemoryStore {
	if quota <= 0 {
		quota = DefaultMemoryQuotaBytes
	}
	return &mockAgentMemoryStore{
		entries: make(map[string]*MemoryEntry),
		usage:   make(map[string]int64),
		quota:   quota,
	}
}

func mockKey(agentID, tenantID, key string) string {
	return agentID + "|" + tenantID + "|" + key
}

func usageKey(agentID, tenantID string) string {
	return agentID + "|" + tenantID
}

func (m *mockAgentMemoryStore) Store(_ context.Context, agentID, tenantID, key, value string, ttl time.Duration, metadata map[string]string) error {
	if m.err != nil {
		return m.err
	}
	if agentID == "" {
		return fmt.Errorf("agentmemory: agent_id must not be empty")
	}
	if tenantID == "" {
		return fmt.Errorf("agentmemory: tenant_id must not be empty")
	}
	if key == "" {
		return fmt.Errorf("agentmemory: key must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sizeBytes := int64(len(value))
	mk := mockKey(agentID, tenantID, key)
	uk := usageKey(agentID, tenantID)

	// Subtract existing size for upsert.
	var existingSize int64
	if existing, ok := m.entries[mk]; ok {
		existingSize = existing.SizeBytes
	}

	netUsage := m.usage[uk] - existingSize + sizeBytes
	if netUsage > m.quota {
		return fmt.Errorf("agentmemory: quota exceeded (usage %d + new %d > quota %d)", m.usage[uk]-existingSize, sizeBytes, m.quota)
	}

	now := time.Now().UTC()
	var expiresAt *time.Time
	if ttl > 0 {
		t := now.Add(ttl)
		expiresAt = &t
	}

	m.entries[mk] = &MemoryEntry{
		Key:       key,
		Value:     value,
		Metadata:  metadata,
		CreatedAt: now,
		ExpiresAt: expiresAt,
		SizeBytes: sizeBytes,
	}
	m.usage[uk] = netUsage

	return nil
}

func (m *mockAgentMemoryStore) Recall(_ context.Context, agentID, tenantID, key string) (*MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	if agentID == "" || tenantID == "" || key == "" {
		return nil, fmt.Errorf("agentmemory: missing required fields")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mk := mockKey(agentID, tenantID, key)
	entry, ok := m.entries[mk]
	if !ok {
		return nil, nil
	}

	// Check TTL.
	if entry.ExpiresAt != nil && time.Now().UTC().After(*entry.ExpiresAt) {
		return nil, nil
	}

	return entry, nil
}

func (m *mockAgentMemoryStore) Search(_ context.Context, agentID, tenantID, query string, limit int) ([]MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	if agentID == "" || tenantID == "" || query == "" {
		return nil, fmt.Errorf("agentmemory: missing required fields")
	}
	if limit <= 0 {
		limit = 10
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := agentID + "|" + tenantID + "|"
	var results []MemoryEntry
	for k, entry := range m.entries {
		if len(results) >= limit {
			break
		}
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		// Check TTL.
		if entry.ExpiresAt != nil && time.Now().UTC().After(*entry.ExpiresAt) {
			continue
		}
		// Simple keyword match on key and value.
		if memContains(entry.Key, query) || memContains(entry.Value, query) {
			results = append(results, *entry)
		}
	}

	return results, nil
}

func (m *mockAgentMemoryStore) Delete(_ context.Context, agentID, tenantID, key string) error {
	if m.err != nil {
		return m.err
	}
	if agentID == "" || tenantID == "" || key == "" {
		return fmt.Errorf("agentmemory: missing required fields")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mk := mockKey(agentID, tenantID, key)
	if entry, ok := m.entries[mk]; ok {
		uk := usageKey(agentID, tenantID)
		m.usage[uk] -= entry.SizeBytes
		delete(m.entries, mk)
	}
	return nil
}

func (m *mockAgentMemoryStore) GetUsage(_ context.Context, agentID, tenantID string) (int64, error) {
	if m.err != nil {
		return 0, m.err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.usage[usageKey(agentID, tenantID)], nil
}

func (m *mockAgentMemoryStore) GetThread(_ context.Context, agentID, tenantID, threadID string, limit int) ([]MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	if agentID == "" || tenantID == "" || threadID == "" {
		return nil, fmt.Errorf("agentmemory: missing required fields")
	}
	if limit <= 0 {
		limit = 50
	}
	const maxLimit = 100
	if limit > maxLimit {
		limit = maxLimit
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := agentID + "|" + tenantID + "|thread:" + threadID + ":"
	var results []MemoryEntry
	for k, entry := range m.entries {
		if len(results) >= limit {
			break
		}
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		// Check TTL.
		if entry.ExpiresAt != nil && time.Now().UTC().After(*entry.ExpiresAt) {
			continue
		}
		results = append(results, *entry)
	}

	return results, nil
}

func (m *mockAgentMemoryStore) SemanticSearch(_ context.Context, agentID, tenantID, query string, limit int) ([]MemoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	if agentID == "" || tenantID == "" || query == "" {
		return nil, fmt.Errorf("agentmemory: missing required fields")
	}
	if limit <= 0 {
		limit = 10
	}
	const maxLimit = 100
	if limit > maxLimit {
		limit = maxLimit
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Mock fuzzy matching: use substring check (real impl uses pg_trgm similarity).
	prefix := agentID + "|" + tenantID + "|"
	var results []MemoryEntry
	for k, entry := range m.entries {
		if len(results) >= limit {
			break
		}
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		// Check TTL.
		if entry.ExpiresAt != nil && time.Now().UTC().After(*entry.ExpiresAt) {
			continue
		}
		// Fuzzy mock: match if key or value contains any 3-char substring of query.
		if memFuzzyMatch(entry.Key, query) || memFuzzyMatch(entry.Value, query) {
			results = append(results, *entry)
		}
	}

	return results, nil
}

// memContains is a case-insensitive substring check for the mock.
func memContains(s, substr string) bool {
	// Simple case-insensitive check.
	sl := memToLower(s)
	ql := memToLower(substr)
	for i := 0; i <= len(sl)-len(ql); i++ {
		if sl[i:i+len(ql)] == ql {
			return true
		}
	}
	return false
}

func memToLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// --- Tests ---

func TestStoreAndRecall_Roundtrip(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	meta := map[string]string{"source": "test"}
	err := store.Store(ctx, "agent-1", "tenant-1", "greeting", "hello world", 0, meta)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	entry, err := store.Recall(ctx, "agent-1", "tenant-1", "greeting")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if entry == nil {
		t.Fatal("Recall returned nil")
	}
	if entry.Key != "greeting" {
		t.Errorf("Key = %q, want %q", entry.Key, "greeting")
	}
	if entry.Value != "hello world" {
		t.Errorf("Value = %q, want %q", entry.Value, "hello world")
	}
	if entry.Metadata["source"] != "test" {
		t.Errorf("Metadata[source] = %q, want %q", entry.Metadata["source"], "test")
	}
	if entry.SizeBytes != int64(len("hello world")) {
		t.Errorf("SizeBytes = %d, want %d", entry.SizeBytes, len("hello world"))
	}
}

func TestRecall_UnknownKey_ReturnsNil(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	entry, err := store.Recall(ctx, "agent-1", "tenant-1", "nonexistent")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for unknown key, got %+v", entry)
	}
}

func TestRecall_ExpiredEntry_ReturnsNil(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	// Store with a very short TTL.
	err := store.Store(ctx, "agent-1", "tenant-1", "ephemeral", "temp data", 1*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Wait for expiry.
	time.Sleep(5 * time.Millisecond)

	entry, err := store.Recall(ctx, "agent-1", "tenant-1", "ephemeral")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for expired entry, got %+v", entry)
	}
}

func TestSearch_ReturnsMatchingEntries(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	if err := store.Store(ctx, "agent-1", "tenant-1", "api-key-config", "the API key config", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, "agent-1", "tenant-1", "db-config", "database connection string", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, "agent-1", "tenant-1", "notes", "some api notes here", 0, nil); err != nil {
		t.Fatal(err)
	}

	results, err := store.Search(ctx, "agent-1", "tenant-1", "api", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Should match "api-key-config" (key match) and "notes" (value match "some api notes").
	if len(results) < 2 {
		t.Errorf("expected at least 2 results, got %d", len(results))
	}
}

func TestQuota_RejectsOverLimit(t *testing.T) {
	// Quota of 50 bytes.
	store := newMockAgentMemoryStore(50)
	ctx := context.Background()

	// Store 30 bytes.
	err := store.Store(ctx, "agent-1", "tenant-1", "k1", "123456789012345678901234567890", 0, nil)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Attempt to store another 30 bytes — should exceed 50 byte quota.
	err = store.Store(ctx, "agent-1", "tenant-1", "k2", "123456789012345678901234567890", 0, nil)
	if err == nil {
		t.Fatal("expected quota error, got nil")
	}
}

func TestNamespaceIsolation(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	// Agent A stores data.
	if err := store.Store(ctx, "agent-a", "tenant-1", "secret", "agent-a-data", 0, nil); err != nil {
		t.Fatal(err)
	}

	// Agent B cannot read Agent A's data.
	entry, err := store.Recall(ctx, "agent-b", "tenant-1", "secret")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if entry != nil {
		t.Errorf("agent-b should not see agent-a's data, got %+v", entry)
	}

	// Same agent, different tenant cannot read.
	entry, err = store.Recall(ctx, "agent-a", "tenant-2", "secret")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if entry != nil {
		t.Errorf("tenant-2 should not see tenant-1's data, got %+v", entry)
	}
}

func TestDelete_RemovesEntry(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	if err := store.Store(ctx, "agent-1", "tenant-1", "to-delete", "bye", 0, nil); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, "agent-1", "tenant-1", "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	entry, err := store.Recall(ctx, "agent-1", "tenant-1", "to-delete")
	if err != nil {
		t.Fatalf("Recall after delete: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil after delete, got %+v", entry)
	}
}

func TestGetUsage_TracksBytes(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	if err := store.Store(ctx, "agent-1", "tenant-1", "k1", "hello", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, "agent-1", "tenant-1", "k2", "world!", 0, nil); err != nil {
		t.Fatal(err)
	}

	usage, err := store.GetUsage(ctx, "agent-1", "tenant-1")
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	expected := int64(len("hello") + len("world!"))
	if usage != expected {
		t.Errorf("usage = %d, want %d", usage, expected)
	}
}

// TestPostgresAgentMemoryStore_ImplementsInterface verifies the Postgres
// implementation satisfies the interface at compile time.
func TestPostgresAgentMemoryStore_ImplementsInterface(t *testing.T) {
	var _ AgentMemoryStore = (*PostgresAgentMemoryStore)(nil)
	var _ AgentMemoryStore = (*mockAgentMemoryStore)(nil)
}

// TestPostgresStore_EmptyInputValidation tests that the real Postgres store
// rejects empty required inputs before hitting the database.
func TestPostgresStore_EmptyInputValidation(t *testing.T) {
	// Use a nil *sql.DB — we're testing validation before any query.
	store := NewPostgresAgentMemoryStore((*sql.DB)(nil), 0)
	ctx := context.Background()

	tests := []struct {
		name    string
		fn      func() error
		wantErr string
	}{
		{
			name:    "Store empty agent_id",
			fn:      func() error { return store.Store(ctx, "", "t", "k", "v", 0, nil) },
			wantErr: "agent_id must not be empty",
		},
		{
			name:    "Store empty tenant_id",
			fn:      func() error { return store.Store(ctx, "a", "", "k", "v", 0, nil) },
			wantErr: "tenant_id must not be empty",
		},
		{
			name:    "Store empty key",
			fn:      func() error { return store.Store(ctx, "a", "t", "", "v", 0, nil) },
			wantErr: "key must not be empty",
		},
		{
			name: "Recall empty agent_id",
			fn: func() error {
				_, err := store.Recall(ctx, "", "t", "k")
				return err
			},
			wantErr: "agent_id must not be empty",
		},
		{
			name: "Search empty query",
			fn: func() error {
				_, err := store.Search(ctx, "a", "t", "", 10)
				return err
			},
			wantErr: "query must not be empty",
		},
		{
			name:    "Delete empty key",
			fn:      func() error { return store.Delete(ctx, "a", "t", "") },
			wantErr: "key must not be empty",
		},
		{
			name: "GetUsage empty tenant_id",
			fn: func() error {
				_, err := store.GetUsage(ctx, "a", "")
				return err
			},
			wantErr: "tenant_id must not be empty",
		},
		{
			name: "GetThread empty thread_id",
			fn: func() error {
				_, err := store.GetThread(ctx, "a", "t", "", 50)
				return err
			},
			wantErr: "thread_id must not be empty",
		},
		{
			name: "GetThread empty agent_id",
			fn: func() error {
				_, err := store.GetThread(ctx, "", "t", "conv1", 50)
				return err
			},
			wantErr: "agent_id must not be empty",
		},
		{
			name: "SemanticSearch empty query",
			fn: func() error {
				_, err := store.SemanticSearch(ctx, "a", "t", "", 10)
				return err
			},
			wantErr: "query must not be empty",
		},
		{
			name: "SemanticSearch empty agent_id",
			fn: func() error {
				_, err := store.SemanticSearch(ctx, "", "t", "test", 10)
				return err
			},
			wantErr: "agent_id must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !containsStr(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// containsStr is a simple substring check for test assertions.
func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestMockStoreStoreError verifies the mock propagates errors.
func TestMockStoreStoreError(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	store.err = errors.New("injected error")
	ctx := context.Background()

	err := store.Store(ctx, "agent-1", "tenant-1", "k", "v", 0, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// memFuzzyMatch simulates trigram-like matching: returns true if any 3-char
// trigram of the query appears in the target string (case-insensitive).
func memFuzzyMatch(target, query string) bool {
	tl := memToLower(target)
	ql := memToLower(query)
	if len(ql) < 3 {
		return memContains(tl, ql)
	}
	for i := 0; i <= len(ql)-3; i++ {
		trigram := ql[i : i+3]
		if memContains(tl, trigram) {
			return true
		}
	}
	return false
}

// --- GetThread tests ---

func TestGetThread_ReturnsThreadEntries(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	// Store thread messages.
	if err := store.Store(ctx, "agent-1", "tenant-1", "thread:conv1:001", "Hello", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, "agent-1", "tenant-1", "thread:conv1:002", "How are you?", 0, nil); err != nil {
		t.Fatal(err)
	}
	// Store a non-thread entry.
	if err := store.Store(ctx, "agent-1", "tenant-1", "config", "value", 0, nil); err != nil {
		t.Fatal(err)
	}
	// Store a different thread.
	if err := store.Store(ctx, "agent-1", "tenant-1", "thread:conv2:001", "Other thread", 0, nil); err != nil {
		t.Fatal(err)
	}

	results, err := store.GetThread(ctx, "agent-1", "tenant-1", "conv1", 50)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 thread entries, got %d", len(results))
	}
}

func TestGetThread_NamespaceIsolation(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	// Agent A stores thread data.
	if err := store.Store(ctx, "agent-a", "tenant-1", "thread:conv1:001", "agent-a msg", 0, nil); err != nil {
		t.Fatal(err)
	}

	// Agent B cannot see agent A's thread.
	results, err := store.GetThread(ctx, "agent-b", "tenant-1", "conv1", 50)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("agent-b should not see agent-a's thread, got %d entries", len(results))
	}

	// Same agent, different tenant cannot see.
	results, err = store.GetThread(ctx, "agent-a", "tenant-2", "conv1", 50)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("tenant-2 should not see tenant-1's thread, got %d entries", len(results))
	}
}

func TestGetThread_EmptyThreadID_ReturnsError(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	_, err := store.GetThread(ctx, "agent-1", "tenant-1", "", 50)
	if err == nil {
		t.Fatal("expected error for empty thread_id, got nil")
	}
}

// --- SemanticSearch tests ---

func TestSemanticSearch_ReturnsFuzzyMatches(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	if err := store.Store(ctx, "agent-1", "tenant-1", "database-config", "postgres connection string", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, "agent-1", "tenant-1", "api-endpoint", "https://example.com", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, "agent-1", "tenant-1", "databse-cnfig", "typo entry for postgres", 0, nil); err != nil {
		t.Fatal(err)
	}

	// "database" should match "database-config" exactly and "databse-cnfig" via trigrams.
	results, err := store.SemanticSearch(ctx, "agent-1", "tenant-1", "database", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}

	if len(results) < 2 {
		t.Errorf("expected at least 2 fuzzy matches, got %d", len(results))
	}
}

func TestSemanticSearch_NamespaceIsolation(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	if err := store.Store(ctx, "agent-a", "tenant-1", "database-config", "postgres", 0, nil); err != nil {
		t.Fatal(err)
	}

	// Agent B should not see agent A's data.
	results, err := store.SemanticSearch(ctx, "agent-b", "tenant-1", "database", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("agent-b should not see agent-a's data, got %d entries", len(results))
	}

	// Same agent, different tenant.
	results, err = store.SemanticSearch(ctx, "agent-a", "tenant-2", "database", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("tenant-2 should not see tenant-1's data, got %d entries", len(results))
	}
}

func TestSemanticSearch_EmptyQuery_ReturnsError(t *testing.T) {
	store := newMockAgentMemoryStore(0)
	ctx := context.Background()

	_, err := store.SemanticSearch(ctx, "agent-1", "tenant-1", "", 10)
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
}

// Ensure MemoryEntry JSON serialization works as expected.
func TestMemoryEntry_JSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	exp := now.Add(time.Hour)
	entry := MemoryEntry{
		Key:       "test-key",
		Value:     "test-value",
		Metadata:  map[string]string{"a": "b"},
		CreatedAt: now,
		ExpiresAt: &exp,
		SizeBytes: 10,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded MemoryEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Key != entry.Key {
		t.Errorf("Key = %q, want %q", decoded.Key, entry.Key)
	}
	if decoded.Value != entry.Value {
		t.Errorf("Value = %q, want %q", decoded.Value, entry.Value)
	}
}
