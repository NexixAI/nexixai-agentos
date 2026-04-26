package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/NexixAI/nexixai-agentos/internal/knowledge"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- index_document tool ---

type indexDocumentInput struct {
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	Source   string            `json:"source,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type indexDocumentOutput struct {
	Indexed bool   `json:"indexed"`
	DocID   string `json:"doc_id"`
}

var indexDocumentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"title": {
			"type": "string",
			"description": "The title of the document to index."
		},
		"content": {
			"type": "string",
			"description": "The full text content of the document."
		},
		"source": {
			"type": "string",
			"description": "Optional source identifier (e.g. URL, filename)."
		},
		"metadata": {
			"type": "object",
			"description": "Optional metadata key-value pairs.",
			"additionalProperties": { "type": "string" }
		}
	},
	"required": ["title", "content"],
	"additionalProperties": false
}`)

// --- search_knowledge tool ---

type searchKnowledgeInput struct {
	Query string `json:"query"`
	Limit *int   `json:"limit,omitempty"`
}

type searchKnowledgeOutput struct {
	Results []knowledge.SearchResult `json:"results"`
	Count   int                      `json:"count"`
}

var searchKnowledgeSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "The search query to match against indexed documents."
		},
		"limit": {
			"type": "integer",
			"description": "Maximum number of results to return (default 10, max 100)."
		}
	},
	"required": ["query"],
	"additionalProperties": false
}`)

// RegisterKnowledgeTools registers index_document and search_knowledge with the
// given tool registry.
func RegisterKnowledgeTools(registry *mcp.ToolRegistry, store knowledge.KnowledgeStore) error {
	if err := registry.Register(mcp.Tool{
		Name:         "index_document",
		Description:  "Index a document for full-text knowledge search within the tenant's knowledge base.",
		InputSchema:  indexDocumentSchema,
		MinClearance: mcp.ClearanceExecute,
		Static:       true,
		Handler:      makeIndexDocumentHandler(store),
	}); err != nil {
		return fmt.Errorf("register index_document: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "search_knowledge",
		Description:  "Search the tenant's knowledge base using full-text search. Returns ranked results with highlighted snippets.",
		InputSchema:  searchKnowledgeSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeSearchKnowledgeHandler(store),
	}); err != nil {
		return fmt.Errorf("register search_knowledge: %w", err)
	}

	return nil
}

// makeIndexDocumentHandler returns a handler that indexes a document.
func makeIndexDocumentHandler(store knowledge.KnowledgeStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input indexDocumentInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.Title == "" {
			return nil, fmt.Errorf("title is required")
		}
		if input.Content == "" {
			return nil, fmt.Errorf("content is required")
		}

		// Extract identity from MCP auth context.
		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		docID, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("generate document id: %w", err)
		}

		doc := knowledge.Document{
			ID:       docID,
			TenantID: ac.TenantID,
			Title:    input.Title,
			Content:  input.Content,
			Source:   input.Source,
			Metadata: input.Metadata,
		}

		if err := store.Index(ctx, doc); err != nil {
			slog.Error("mcp/knowledge: index failed",
				"tenant_id", ac.TenantID,
				"title", input.Title,
				"error", err,
			)
			return nil, fmt.Errorf("failed to index document: %w", err)
		}

		slog.Info("mcp/knowledge: indexed",
			"tenant_id", ac.TenantID,
			"doc_id", docID,
			"title", input.Title,
		)

		return indexDocumentOutput{
			Indexed: true,
			DocID:   docID,
		}, nil
	}
}

// makeSearchKnowledgeHandler returns a handler that searches indexed documents.
func makeSearchKnowledgeHandler(store knowledge.KnowledgeStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input searchKnowledgeInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		// Extract identity from MCP auth context.
		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		limit := 10
		if input.Limit != nil && *input.Limit > 0 {
			limit = *input.Limit
		}

		results, err := store.Search(ctx, ac.TenantID, input.Query, limit)
		if err != nil {
			slog.Error("mcp/knowledge: search failed",
				"tenant_id", ac.TenantID,
				"query", input.Query,
				"error", err,
			)
			return nil, fmt.Errorf("failed to search knowledge: %w", err)
		}

		if results == nil {
			results = []knowledge.SearchResult{}
		}

		slog.Info("mcp/knowledge: search",
			"tenant_id", ac.TenantID,
			"query", input.Query,
			"count", len(results),
		)

		return searchKnowledgeOutput{
			Results: results,
			Count:   len(results),
		}, nil
	}
}
