package kbclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/knowledge"
)

// --- Interface compliance ---

var _ knowledge.KnowledgeStore = (*KBClient)(nil)

// --- Index tests ---

func TestIndex_PostsCorrectBody(t *testing.T) {
	var gotMethod, gotPath, gotContentType, gotAuth string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewKBClient(srv.URL, "kb-test-token")

	doc := knowledge.Document{
		ID:       "doc-1",
		TenantID: "tenant-1",
		Title:    "Test Document",
		Content:  "The content of the test document.",
		Source:   "unit-test",
		Metadata: map[string]string{"env": "test"},
	}

	err := client.Index(context.Background(), doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/index" {
		t.Errorf("path = %q, want /v1/index", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if gotAuth != "Bearer kb-test-token" {
		t.Errorf("Authorization = %q, want Bearer kb-test-token", gotAuth)
	}

	var req indexRequest
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}
	if req.ID != "doc-1" {
		t.Errorf("body.id = %q, want %q", req.ID, "doc-1")
	}
	if req.TenantID != "tenant-1" {
		t.Errorf("body.tenant_id = %q, want %q", req.TenantID, "tenant-1")
	}
	if req.Title != "Test Document" {
		t.Errorf("body.title = %q, want %q", req.Title, "Test Document")
	}
	if req.Content != "The content of the test document." {
		t.Errorf("body.content = %q, want %q", req.Content, "The content of the test document.")
	}
	if req.Source != "unit-test" {
		t.Errorf("body.source = %q, want %q", req.Source, "unit-test")
	}
	if req.Metadata["env"] != "test" {
		t.Errorf("body.metadata[env] = %q, want %q", req.Metadata["env"], "test")
	}
}

func TestIndex_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	client := NewKBClient(srv.URL, "")
	err := client.Index(context.Background(), knowledge.Document{
		ID: "doc-1", TenantID: "t1", Title: "T", Content: "C",
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

// --- Search tests ---

func TestSearch_GetsCorrectParams(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotTenantID, gotQuery, gotLimit string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotTenantID = r.URL.Query().Get("tenant_id")
		gotQuery = r.URL.Query().Get("query")
		gotLimit = r.URL.Query().Get("limit")

		// The KB service returns a plain JSON array of searchResultWire.
		results := []searchResultWire{
			{
				ID:      "doc-1",
				Repo:    "nexixai-agentos",
				Path:    "docs/testing.md",
				Title:   "Go Testing",
				Snippet: "**Go** Testing...",
				Score:   0.75,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results) //nolint:errcheck // test helper
	}))
	defer srv.Close()

	client := NewKBClient(srv.URL, "search-token")
	results, err := client.Search(context.Background(), "tenant-1", "go testing", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/search" {
		t.Errorf("path = %q, want /v1/search", gotPath)
	}
	if gotAuth != "Bearer search-token" {
		t.Errorf("Authorization = %q, want Bearer search-token", gotAuth)
	}
	if gotTenantID != "tenant-1" {
		t.Errorf("tenant_id = %q, want %q", gotTenantID, "tenant-1")
	}
	if gotQuery != "go testing" {
		t.Errorf("query = %q, want %q", gotQuery, "go testing")
	}
	if gotLimit != "5" {
		t.Errorf("limit = %q, want %q", gotLimit, "5")
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "doc-1" {
		t.Errorf("result[0].ID = %q, want %q", results[0].ID, "doc-1")
	}
	if results[0].Title != "Go Testing" {
		t.Errorf("result[0].Title = %q, want %q", results[0].Title, "Go Testing")
	}
	if results[0].Source != "nexixai-agentos/docs/testing.md" {
		t.Errorf("result[0].Source = %q, want %q", results[0].Source, "nexixai-agentos/docs/testing.md")
	}
	if results[0].Rank != 0.75 {
		t.Errorf("result[0].Rank = %f, want 0.75", results[0].Rank)
	}
	if results[0].Snippet != "**Go** Testing..." {
		t.Errorf("result[0].Snippet = %q, want %q", results[0].Snippet, "**Go** Testing...")
	}
}

func TestSearch_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer srv.Close()

	client := NewKBClient(srv.URL, "")
	_, err := client.Search(context.Background(), "tenant-1", "test", 10)
	if err == nil {
		t.Fatal("expected error for 502 response")
	}
}

// --- Delete tests ---

func TestDelete_SendsCorrectRequest(t *testing.T) {
	var gotMethod, gotPath, gotTenantHeader, gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotTenantHeader = r.Header.Get("X-Tenant-ID")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewKBClient(srv.URL, "delete-token")
	err := client.Delete(context.Background(), "tenant-1", "doc-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/v1/documents/doc-42" {
		t.Errorf("path = %q, want /v1/documents/doc-42", gotPath)
	}
	if gotTenantHeader != "tenant-1" {
		t.Errorf("X-Tenant-ID = %q, want %q", gotTenantHeader, "tenant-1")
	}
	if gotAuth != "Bearer delete-token" {
		t.Errorf("Authorization = %q, want Bearer delete-token", gotAuth)
	}
}

func TestDelete_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer srv.Close()

	client := NewKBClient(srv.URL, "")
	err := client.Delete(context.Background(), "tenant-1", "doc-missing")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

// --- Connection error tests ---

func TestIndex_ConnectionError(t *testing.T) {
	// Use an unreachable URL to trigger a connection error.
	client := NewKBClient("http://127.0.0.1:1", "")
	err := client.Index(context.Background(), knowledge.Document{
		ID: "doc-1", TenantID: "t1", Title: "T", Content: "C",
	})
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestSearch_ConnectionError(t *testing.T) {
	client := NewKBClient("http://127.0.0.1:1", "")
	_, err := client.Search(context.Background(), "t1", "test", 10)
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestDelete_ConnectionError(t *testing.T) {
	client := NewKBClient("http://127.0.0.1:1", "")
	err := client.Delete(context.Background(), "t1", "doc-1")
	if err == nil {
		t.Fatal("expected connection error")
	}
}
