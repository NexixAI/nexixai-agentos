package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- web_search tool ---

type webSearchInput struct {
	Query      string `json:"query"`
	MaxResults *int   `json:"max_results,omitempty"`
	Categories string `json:"categories,omitempty"`
}

type webSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Engine  string `json:"engine"`
}

type webSearchOutput struct {
	Results         []webSearchResult `json:"results"`
	Query           string            `json:"query"`
	NumberOfResults int               `json:"number_of_results"`
}

var webSearchSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "The search query."
		},
		"max_results": {
			"type": "integer",
			"description": "Maximum number of results to return (default 5, max 20)."
		},
		"categories": {
			"type": "string",
			"description": "Comma-separated search categories: general, science, it, news."
		}
	},
	"required": ["query"],
	"additionalProperties": false
}`)

// searxngResponse matches the SearXNG JSON API response.
type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
		Engine  string `json:"engine"`
	} `json:"results"`
	NumberOfResults int `json:"number_of_results"`
}

// RegisterWebSearchTool registers the web_search tool with the given registry.
func RegisterWebSearchTool(registry *mcp.ToolRegistry) error {
	return registry.Register(mcp.Tool{
		Name:         "web_search",
		Description:  "Search the web using the self-hosted SearXNG metasearch engine. Returns titles, URLs, and content snippets from multiple search engines.",
		InputSchema:  webSearchSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeWebSearchHandler(),
	})
}

func makeWebSearchHandler() mcp.ToolHandler {
	searxngURL := os.Getenv("SEARXNG_URL")
	if searxngURL == "" {
		searxngURL = "http://localhost:8888"
	}

	client := &http.Client{Timeout: 15 * time.Second}

	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input webSearchInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		maxResults := 5
		if input.MaxResults != nil && *input.MaxResults > 0 {
			maxResults = *input.MaxResults
			if maxResults > 20 {
				maxResults = 20
			}
		}

		// Build SearXNG request URL
		u, err := url.Parse(searxngURL + "/search")
		if err != nil {
			return nil, fmt.Errorf("invalid SEARXNG_URL: %w", err)
		}
		q := u.Query()
		q.Set("q", input.Query)
		q.Set("format", "json")
		if input.Categories != "" {
			q.Set("categories", input.Categories)
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			slog.Error("mcp/websearch: SearXNG request failed", "url", u.String(), "error", err)
			return nil, fmt.Errorf("SearXNG is unreachable: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("SearXNG returned status %d: %s", resp.StatusCode, string(body))
		}

		var sr searxngResponse
		if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			return nil, fmt.Errorf("decode SearXNG response: %w", err)
		}

		results := make([]webSearchResult, 0, maxResults)
		for i, r := range sr.Results {
			if i >= maxResults {
				break
			}
			results = append(results, webSearchResult{
				Title:   r.Title,
				URL:     r.URL,
				Content: r.Content,
				Engine:  r.Engine,
			})
		}

		slog.Info("mcp/websearch: search",
			"query", input.Query,
			"results", len(results),
			"total", sr.NumberOfResults,
		)

		return webSearchOutput{
			Results:         results,
			Query:           input.Query,
			NumberOfResults: len(results),
		}, nil
	}
}

