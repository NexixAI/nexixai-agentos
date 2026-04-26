package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/knowledge"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- mock KnowledgeStore ---

type mockKnowledgeStore struct {
	mu   sync.Mutex
	docs map[string]knowledge.Document // keyed by "tenantID|docID"
	err  error
}

func newMockKnowledgeStore() *mockKnowledgeStore {
	return &mockKnowledgeStore{
		docs: make(map[string]knowledge.Document),
	}
}

func knowledgeDocKey(tenantID, docID string) string {
	return tenantID + "|" + docID
}

func (m *mockKnowledgeStore) Index(_ context.Context, doc knowledge.Document) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[knowledgeDocKey(doc.TenantID, doc.ID)] = doc
	return nil
}

func (m *mockKnowledgeStore) Search(_ context.Context, tenantID, query string, limit int) ([]knowledge.SearchResult, error) {
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

	var results []knowledge.SearchResult
	for _, doc := range m.docs {
		if len(results) >= limit {
			break
		}
		if doc.TenantID != tenantID {
			continue
		}
		combined := doc.Title + " " + doc.Content
		if !strings.Contains(strings.ToLower(combined), strings.ToLower(query)) {
			continue
		}
		results = append(results, knowledge.SearchResult{
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
	delete(m.docs, knowledgeDocKey(tenantID, docID))
	return nil
}

// getDoc returns a document from the mock for test assertions.
func (m *mockKnowledgeStore) getDoc(tenantID, docID string) (knowledge.Document, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.docs[knowledgeDocKey(tenantID, docID)]
	return doc, ok
}

// docCount returns the number of documents in the mock store.
func (m *mockKnowledgeStore) docCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.docs)
}

// --- Test helpers ---

func knowledgeAuthCtx(tenantID string) context.Context {
	return mcp.WithMCPAuth(context.Background(), mcp.MCPAuthContext{
		AgentID:  "agent-1",
		TenantID: tenantID,
	})
}

// --- index_document tests ---

func TestIndexDocument_WritesViaMockStore(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("index_document")
	if tool == nil {
		t.Fatal("index_document not registered")
	}

	ctx := knowledgeAuthCtx("tenant-1")
	params, _ := json.Marshal(indexDocumentInput{
		Title:    "Go Concurrency",
		Content:  "Goroutines and channels provide lightweight concurrency.",
		Source:   "blog",
		Metadata: map[string]string{"author": "test"},
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(indexDocumentOutput)
	if !ok {
		t.Fatalf("expected indexDocumentOutput, got %T", result)
	}
	if !out.Indexed {
		t.Error("expected indexed=true")
	}
	if out.DocID == "" {
		t.Error("expected non-empty doc_id")
	}

	// Verify the mock store received the document.
	if store.docCount() != 1 {
		t.Fatalf("expected 1 document in store, got %d", store.docCount())
	}

	doc, ok := store.getDoc("tenant-1", out.DocID)
	if !ok {
		t.Fatal("document not found in mock store")
	}
	if doc.Title != "Go Concurrency" {
		t.Errorf("title = %q, want %q", doc.Title, "Go Concurrency")
	}
	if doc.Content != "Goroutines and channels provide lightweight concurrency." {
		t.Errorf("content mismatch")
	}
	if doc.Source != "blog" {
		t.Errorf("source = %q, want %q", doc.Source, "blog")
	}
	if doc.TenantID != "tenant-1" {
		t.Errorf("tenant_id = %q, want %q", doc.TenantID, "tenant-1")
	}
}

func TestIndexDocument_MissingTitle_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("index_document")
	ctx := knowledgeAuthCtx("tenant-1")

	params, _ := json.Marshal(indexDocumentInput{
		Title:   "",
		Content: "some content",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing title, got nil")
	}
}

func TestIndexDocument_MissingContent_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("index_document")
	ctx := knowledgeAuthCtx("tenant-1")

	params, _ := json.Marshal(indexDocumentInput{
		Title:   "A Title",
		Content: "",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing content, got nil")
	}
}

func TestIndexDocument_MissingAuthContext_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("index_document")

	params, _ := json.Marshal(indexDocumentInput{
		Title:   "A Title",
		Content: "Some content",
	})

	// No auth context.
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
}

// --- search_knowledge tests ---

func TestSearchKnowledge_ReturnsResults(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	// Pre-populate via mock.
	ctx := knowledgeAuthCtx("tenant-1")
	if err := store.Index(context.Background(), knowledge.Document{
		ID: "doc-1", TenantID: "tenant-1", Title: "Go Concurrency", Content: "Goroutines are lightweight.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Index(context.Background(), knowledge.Document{
		ID: "doc-2", TenantID: "tenant-1", Title: "Go Testing", Content: "Table-driven tests in Go.",
	}); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("search_knowledge")
	if tool == nil {
		t.Fatal("search_knowledge not registered")
	}

	params, _ := json.Marshal(searchKnowledgeInput{
		Query: "go",
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(searchKnowledgeOutput)
	if !ok {
		t.Fatalf("expected searchKnowledgeOutput, got %T", result)
	}
	if out.Count < 1 {
		t.Errorf("expected at least 1 result, got %d", out.Count)
	}
	if len(out.Results) != out.Count {
		t.Errorf("results length %d != count %d", len(out.Results), out.Count)
	}
}

func TestSearchKnowledge_MissingQuery_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("search_knowledge")
	ctx := knowledgeAuthCtx("tenant-1")

	params, _ := json.Marshal(searchKnowledgeInput{
		Query: "",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing query, got nil")
	}
}

func TestSearchKnowledge_MissingAuthContext_ReturnsError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("search_knowledge")

	params, _ := json.Marshal(searchKnowledgeInput{
		Query: "anything",
	})

	// No auth context.
	_, err := tool.Handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}
}

func TestSearchKnowledge_CustomLimit(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	// Pre-populate 3 documents.
	for i, title := range []string{"Go A", "Go B", "Go C"} {
		if err := store.Index(context.Background(), knowledge.Document{
			ID: "doc-" + string(rune('1'+i)), TenantID: "tenant-1", Title: title, Content: "Go content.",
		}); err != nil {
			t.Fatal(err)
		}
	}

	tool := registry.Get("search_knowledge")
	ctx := knowledgeAuthCtx("tenant-1")

	limit := 1
	params, _ := json.Marshal(searchKnowledgeInput{
		Query: "go",
		Limit: &limit,
	})

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(searchKnowledgeOutput)
	if !ok {
		t.Fatalf("expected searchKnowledgeOutput, got %T", result)
	}
	if out.Count > 1 {
		t.Errorf("expected at most 1 result with limit=1, got %d", out.Count)
	}
}

// --- Clearance tier tests ---

func TestKnowledgeTools_ClearanceTier(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		toolName string
		want     mcp.ClearanceTier
	}{
		{"index_document is ClearanceExecute", "index_document", mcp.ClearanceExecute},
		{"search_knowledge is ClearanceInternal", "search_knowledge", mcp.ClearanceInternal},
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

// --- Error propagation tests ---

func TestIndexDocument_StoreError_PropagatesError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()
	store.err = fmt.Errorf("database connection lost")

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("index_document")
	ctx := knowledgeAuthCtx("tenant-1")

	params, _ := json.Marshal(indexDocumentInput{
		Title:   "Test",
		Content: "Data",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error from store, got nil")
	}
}

func TestSearchKnowledge_StoreError_PropagatesError(t *testing.T) {
	registry := mcp.NewToolRegistry()
	store := newMockKnowledgeStore()
	store.err = fmt.Errorf("database connection lost")

	if err := RegisterKnowledgeTools(registry, store); err != nil {
		t.Fatal(err)
	}

	tool := registry.Get("search_knowledge")
	ctx := knowledgeAuthCtx("tenant-1")

	params, _ := json.Marshal(searchKnowledgeInput{
		Query: "test",
	})

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Fatal("expected error from store, got nil")
	}
}
