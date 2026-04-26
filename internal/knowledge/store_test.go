package knowledge

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- mock KnowledgeStore ---

type mockKnowledgeStore struct {
	mu   sync.Mutex
	docs map[string]Document // keyed by "tenantID|docID"
	err  error
}

func newMockKnowledgeStore() *mockKnowledgeStore {
	return &mockKnowledgeStore{
		docs: make(map[string]Document),
	}
}

func docKey(tenantID, docID string) string {
	return tenantID + "|" + docID
}

func (m *mockKnowledgeStore) Index(_ context.Context, doc Document) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc.CreatedAt = time.Now().UTC()
	m.docs[docKey(doc.TenantID, doc.ID)] = doc
	return nil
}

func (m *mockKnowledgeStore) Search(_ context.Context, tenantID, query string, limit int) ([]SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	if query == "" {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if limit <= 0 {
		limit = 10
	}

	var results []SearchResult
	for _, doc := range m.docs {
		if len(results) >= limit {
			break
		}
		if doc.TenantID != tenantID {
			continue
		}
		// Simple keyword match on title + content for mock.
		combined := doc.Title + " " + doc.Content
		if !strings.Contains(strings.ToLower(combined), strings.ToLower(query)) {
			continue
		}
		results = append(results, SearchResult{
			Document: doc,
			Rank:     0.5,
			Snippet:  "..." + query + "...",
		})
	}
	return results, nil
}

func (m *mockKnowledgeStore) Delete(_ context.Context, tenantID, docID string) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, docKey(tenantID, docID))
	return nil
}

// getDoc returns a document from the mock store for test assertions.
func (m *mockKnowledgeStore) getDoc(tenantID, docID string) (Document, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.docs[docKey(tenantID, docID)]
	return doc, ok
}

// --- Interface compliance ---

var _ KnowledgeStore = (*mockKnowledgeStore)(nil)
var _ KnowledgeStore = (*PostgresKnowledgeStore)(nil)

// --- Index tests ---

func TestIndex_RoundTrip(t *testing.T) {
	store := newMockKnowledgeStore()

	doc := Document{
		ID:       "doc-1",
		TenantID: "tenant-1",
		Title:    "Go Concurrency",
		Content:  "Goroutines and channels provide lightweight concurrency in Go.",
		Source:   "blog",
		Metadata: map[string]string{"author": "test"},
	}

	if err := store.Index(context.Background(), doc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := store.getDoc("tenant-1", "doc-1")
	if !ok {
		t.Fatal("document not found in store")
	}
	if got.Title != doc.Title {
		t.Errorf("title = %q, want %q", got.Title, doc.Title)
	}
	if got.Content != doc.Content {
		t.Errorf("content = %q, want %q", got.Content, doc.Content)
	}
	if got.Source != doc.Source {
		t.Errorf("source = %q, want %q", got.Source, doc.Source)
	}
	if got.Metadata["author"] != "test" {
		t.Errorf("metadata[author] = %q, want %q", got.Metadata["author"], "test")
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestSearch_ReturnsRankedResults(t *testing.T) {
	store := newMockKnowledgeStore()

	docs := []Document{
		{ID: "doc-1", TenantID: "tenant-1", Title: "Go Concurrency", Content: "Goroutines and channels."},
		{ID: "doc-2", TenantID: "tenant-1", Title: "Go Testing", Content: "Table-driven tests in Go."},
		{ID: "doc-3", TenantID: "tenant-1", Title: "Python Basics", Content: "Python is a scripting language."},
	}
	for _, doc := range docs {
		if err := store.Index(context.Background(), doc); err != nil {
			t.Fatalf("index failed: %v", err)
		}
	}

	results, err := store.Search(context.Background(), "tenant-1", "go", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	for _, r := range results {
		if r.Rank <= 0 {
			t.Errorf("expected positive rank, got %f", r.Rank)
		}
		if r.Snippet == "" {
			t.Error("expected non-empty snippet")
		}
	}
}

func TestSearch_TenantIsolation(t *testing.T) {
	store := newMockKnowledgeStore()

	// Index docs for two different tenants.
	if err := store.Index(context.Background(), Document{
		ID: "doc-1", TenantID: "tenant-A", Title: "Secret Plans", Content: "Top secret information for tenant A.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Index(context.Background(), Document{
		ID: "doc-2", TenantID: "tenant-B", Title: "Public Docs", Content: "Public information for tenant B.",
	}); err != nil {
		t.Fatal(err)
	}

	// Tenant A should only see their own document.
	resultsA, err := store.Search(context.Background(), "tenant-A", "secret", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resultsA) != 1 {
		t.Fatalf("tenant-A: expected 1 result, got %d", len(resultsA))
	}
	if resultsA[0].TenantID != "tenant-A" {
		t.Errorf("tenant-A: result tenant_id = %q, want %q", resultsA[0].TenantID, "tenant-A")
	}

	// Tenant B should NOT see tenant A's document.
	resultsB, err := store.Search(context.Background(), "tenant-B", "secret", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resultsB) != 0 {
		t.Fatalf("tenant-B: expected 0 results for tenant-A content, got %d", len(resultsB))
	}
}

func TestDelete_RemovesDocument(t *testing.T) {
	store := newMockKnowledgeStore()

	doc := Document{
		ID:       "doc-1",
		TenantID: "tenant-1",
		Title:    "Temporary Doc",
		Content:  "This will be deleted.",
	}

	if err := store.Index(context.Background(), doc); err != nil {
		t.Fatal(err)
	}

	// Verify it exists.
	if _, ok := store.getDoc("tenant-1", "doc-1"); !ok {
		t.Fatal("document should exist before delete")
	}

	if err := store.Delete(context.Background(), "tenant-1", "doc-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it's gone.
	if _, ok := store.getDoc("tenant-1", "doc-1"); ok {
		t.Error("document should not exist after delete")
	}
}

func TestSearch_EmptyQuery_ReturnsEmpty(t *testing.T) {
	store := newMockKnowledgeStore()

	// Index a document.
	if err := store.Index(context.Background(), Document{
		ID: "doc-1", TenantID: "tenant-1", Title: "Test", Content: "Some content.",
	}); err != nil {
		t.Fatal(err)
	}

	// Empty query should return nil (no results, no error).
	results, err := store.Search(context.Background(), "tenant-1", "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestIndex_SearchRoundTrip(t *testing.T) {
	store := newMockKnowledgeStore()

	doc := Document{
		ID:       "doc-1",
		TenantID: "tenant-1",
		Title:    "Kubernetes Architecture",
		Content:  "Kubernetes uses pods and services for container orchestration.",
		Source:   "wiki",
	}

	if err := store.Index(context.Background(), doc); err != nil {
		t.Fatal(err)
	}

	results, err := store.Search(context.Background(), "tenant-1", "kubernetes", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "doc-1" {
		t.Errorf("result id = %q, want %q", results[0].ID, "doc-1")
	}
	if results[0].Title != doc.Title {
		t.Errorf("result title = %q, want %q", results[0].Title, doc.Title)
	}
}
