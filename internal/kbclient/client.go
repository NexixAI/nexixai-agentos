// Package kbclient provides an HTTP client for the nexixai-kb knowledge base
// service, implementing the knowledge.KnowledgeStore interface so MCP tools
// can delegate to the central KB rather than requiring a local Postgres.
package kbclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/knowledge"
)

// KBClient translates knowledge.KnowledgeStore calls into HTTP requests
// against the nexixai-kb service API.
type KBClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewKBClient returns a KBClient pointing at the given base URL (e.g.
// "http://nexixai-kb:9093"). A 30-second default timeout is applied.
func NewKBClient(baseURL, apiKey string) *KBClient {
	return &KBClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// --- knowledge.KnowledgeStore implementation ---

// indexRequest mirrors the POST /v1/index request body expected by nexixai-kb.
type indexRequest struct {
	ID       string            `json:"id"`
	TenantID string            `json:"tenant_id"`
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	Source   string            `json:"source,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// searchResultWire mirrors the JSON returned by GET /v1/search on the KB service.
// The KB returns a plain JSON array, not wrapped in {"results": [...]}.
type searchResultWire struct {
	ID      string  `json:"ID"`
	Repo    string  `json:"Repo"`
	Path    string  `json:"Path"`
	Title   string  `json:"Title"`
	Snippet string  `json:"Snippet"`
	Score   float64 `json:"Score"`
}

// Index sends a document to POST /v1/index on the KB service.
func (c *KBClient) Index(ctx context.Context, doc knowledge.Document) error {
	body, err := json.Marshal(indexRequest{
		ID:       doc.ID,
		TenantID: doc.TenantID,
		Title:    doc.Title,
		Content:  doc.Content,
		Source:   doc.Source,
		Metadata: doc.Metadata,
	})
	if err != nil {
		return fmt.Errorf("kbclient: marshal index request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/index", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("kbclient: create index request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kbclient: index request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("kbclient: index returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Search calls GET /v1/search on the KB service and converts the results
// into knowledge.SearchResult values.
func (c *KBClient) Search(ctx context.Context, tenantID, query string, limit int) ([]knowledge.SearchResult, error) {
	u, err := url.Parse(c.baseURL + "/v1/search")
	if err != nil {
		return nil, fmt.Errorf("kbclient: parse search URL: %w", err)
	}

	q := u.Query()
	q.Set("tenant_id", tenantID)
	q.Set("query", query)
	q.Set("limit", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("kbclient: create search request: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kbclient: search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("kbclient: search returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var wires []searchResultWire
	if err := json.NewDecoder(resp.Body).Decode(&wires); err != nil {
		return nil, fmt.Errorf("kbclient: decode search response: %w", err)
	}

	results := make([]knowledge.SearchResult, len(wires))
	for i, r := range wires {
		results[i] = knowledge.SearchResult{
			Document: knowledge.Document{
				ID:     r.ID,
				Title:  r.Title,
				Source: r.Repo + "/" + r.Path,
			},
			Rank:    r.Score,
			Snippet: r.Snippet,
		}
	}

	return results, nil
}

// Delete calls DELETE /v1/documents/{id} on the KB service, scoping by tenant.
func (c *KBClient) Delete(ctx context.Context, tenantID, docID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/v1/documents/"+url.PathEscape(docID), nil)
	if err != nil {
		return fmt.Errorf("kbclient: create delete request: %w", err)
	}
	req.Header.Set("X-Tenant-ID", tenantID)
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kbclient: delete request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("kbclient: delete returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (c *KBClient) applyAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}
