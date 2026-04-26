package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Document represents a knowledge document indexed for full-text search.
type Document struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Source    string            `json:"source,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// SearchResult wraps a Document with search-specific ranking information.
type SearchResult struct {
	Document
	Rank    float64 `json:"rank"`
	Snippet string  `json:"snippet"`
}

// KnowledgeStore abstracts knowledge document persistence and full-text search.
// Implementations MUST enforce per-tenant isolation: every query filters by tenant_id.
type KnowledgeStore interface {
	// Index inserts or replaces a knowledge document and updates the full-text search vector.
	Index(ctx context.Context, doc Document) error
	// Search performs a full-text search within a tenant's documents, returning
	// results ordered by relevance rank with highlighted snippets.
	Search(ctx context.Context, tenantID, query string, limit int) ([]SearchResult, error)
	// Delete removes a knowledge document by tenant and document ID.
	Delete(ctx context.Context, tenantID, docID string) error
}

// PostgresKnowledgeStore implements KnowledgeStore backed by Postgres
// full-text search using tsvector/tsquery.
type PostgresKnowledgeStore struct {
	db *sql.DB
}

// NewPostgresKnowledgeStore creates a PostgresKnowledgeStore with the given database.
func NewPostgresKnowledgeStore(db *sql.DB) *PostgresKnowledgeStore {
	return &PostgresKnowledgeStore{db: db}
}

// Index inserts a knowledge document with a computed tsvector for full-text search.
// On conflict (same ID), the existing document is replaced.
func (s *PostgresKnowledgeStore) Index(ctx context.Context, doc Document) error {
	if doc.ID == "" {
		return fmt.Errorf("knowledge: document id must not be empty")
	}
	if doc.TenantID == "" {
		return fmt.Errorf("knowledge: tenant_id must not be empty")
	}
	if doc.Title == "" {
		return fmt.Errorf("knowledge: title must not be empty")
	}
	if doc.Content == "" {
		return fmt.Errorf("knowledge: content must not be empty")
	}

	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("knowledge: marshal metadata: %w", err)
	}

	now := time.Now().UTC()

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO knowledge_documents (id, tenant_id, title, content, source, metadata, tsv, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, to_tsvector('english', $3 || ' ' || $4), $7)
		 ON CONFLICT (id) DO UPDATE
		 SET tenant_id = EXCLUDED.tenant_id,
		     title = EXCLUDED.title,
		     content = EXCLUDED.content,
		     source = EXCLUDED.source,
		     metadata = EXCLUDED.metadata,
		     tsv = to_tsvector('english', EXCLUDED.title || ' ' || EXCLUDED.content),
		     created_at = EXCLUDED.created_at`,
		doc.ID, doc.TenantID, doc.Title, doc.Content, doc.Source, metadataJSON, now,
	)
	if err != nil {
		return fmt.Errorf("knowledge: index document: %w", err)
	}

	slog.Debug("knowledge: indexed document",
		"doc_id", doc.ID,
		"tenant_id", doc.TenantID,
		"title", doc.Title,
	)

	return nil
}

// Search performs a full-text search using Postgres ts_rank and ts_headline.
// Returns results ordered by rank descending, limited to the specified count.
// An empty query returns an empty result set (not an error).
func (s *PostgresKnowledgeStore) Search(ctx context.Context, tenantID, query string, limit int) ([]SearchResult, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("knowledge: tenant_id must not be empty")
	}
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	// Cap search results to prevent unbounded memory.
	const maxLimit = 100
	if limit > maxLimit {
		limit = maxLimit
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, title, content, source, metadata, created_at,
		        ts_rank(tsv, plainto_tsquery('english', $2)) AS rank,
		        ts_headline('english', content, plainto_tsquery('english', $2),
		                    'StartSel=**,StopSel=**,MaxWords=35,MinWords=15') AS snippet
		 FROM knowledge_documents
		 WHERE tenant_id = $1
		   AND tsv @@ plainto_tsquery('english', $2)
		 ORDER BY rank DESC
		 LIMIT $3`,
		tenantID, query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("knowledge: search query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var sr SearchResult
		var metadataJSON []byte

		if err := rows.Scan(
			&sr.ID, &sr.TenantID, &sr.Title, &sr.Content, &sr.Source,
			&metadataJSON, &sr.CreatedAt, &sr.Rank, &sr.Snippet,
		); err != nil {
			return nil, fmt.Errorf("knowledge: scan search result: %w", err)
		}

		if len(metadataJSON) > 0 {
			sr.Metadata = make(map[string]string)
			if err := json.Unmarshal(metadataJSON, &sr.Metadata); err != nil {
				return nil, fmt.Errorf("knowledge: unmarshal metadata: %w", err)
			}
		}

		results = append(results, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("knowledge: iterate search results: %w", err)
	}

	return results, nil
}

// Delete removes a knowledge document by tenant and document ID.
func (s *PostgresKnowledgeStore) Delete(ctx context.Context, tenantID, docID string) error {
	if tenantID == "" {
		return fmt.Errorf("knowledge: tenant_id must not be empty")
	}
	if docID == "" {
		return fmt.Errorf("knowledge: doc_id must not be empty")
	}

	_, err := s.db.ExecContext(ctx,
		"DELETE FROM knowledge_documents WHERE tenant_id = $1 AND id = $2",
		tenantID, docID,
	)
	if err != nil {
		return fmt.Errorf("knowledge: delete document: %w", err)
	}

	slog.Debug("knowledge: deleted document",
		"doc_id", docID,
		"tenant_id", tenantID,
	)

	return nil
}
