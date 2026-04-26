package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// --- memory_store tool ---

type memoryStoreInput struct {
	Key        string            `json:"key"`
	Value      string            `json:"value"`
	TTLSeconds *int              `json:"ttl_seconds,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type memoryStoreOutput struct {
	Stored    bool   `json:"stored"`
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
}

var memoryStoreSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"key": {
			"type": "string",
			"description": "The key to store the memory under."
		},
		"value": {
			"type": "string",
			"description": "The value to store."
		},
		"ttl_seconds": {
			"type": "integer",
			"description": "Optional time-to-live in seconds. If omitted, the entry does not expire."
		},
		"metadata": {
			"type": "object",
			"description": "Optional metadata key-value pairs.",
			"additionalProperties": { "type": "string" }
		}
	},
	"required": ["key", "value"],
	"additionalProperties": false
}`)

// --- memory_recall tool ---

type memoryRecallInput struct {
	Key      *string `json:"key,omitempty"`
	Query    *string `json:"query,omitempty"`
	Limit    *int    `json:"limit,omitempty"`
	Semantic *bool   `json:"semantic,omitempty"`
}

type memoryRecallOutput struct {
	Entries []postgres.MemoryEntry `json:"entries"`
	Count   int                    `json:"count"`
}

var memoryRecallSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"key": {
			"type": "string",
			"description": "Exact key to recall. Provide either key or query, not both."
		},
		"query": {
			"type": "string",
			"description": "Keyword search query across keys and values. Provide either key or query, not both."
		},
		"limit": {
			"type": "integer",
			"description": "Maximum number of results to return (default 10, max 100)."
		},
		"semantic": {
			"type": "boolean",
			"description": "When true, use fuzzy/semantic matching (pg_trgm trigram similarity) instead of ILIKE. Default false."
		}
	},
	"additionalProperties": false
}`)

// --- get_thread tool ---

type getThreadInput struct {
	ThreadID string `json:"thread_id"`
	Limit    *int   `json:"limit,omitempty"`
}

type getThreadOutput struct {
	ThreadID string                 `json:"thread_id"`
	Messages []postgres.MemoryEntry `json:"messages"`
	Count    int                    `json:"count"`
}

var getThreadSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"thread_id": {
			"type": "string",
			"description": "The thread ID to retrieve messages for."
		},
		"limit": {
			"type": "integer",
			"description": "Maximum number of messages to return (default 50, max 100)."
		}
	},
	"required": ["thread_id"],
	"additionalProperties": false
}`)

// RegisterMemoryTools registers memory_store and memory_recall with the given
// tool registry.
func RegisterMemoryTools(registry *mcp.ToolRegistry, memoryStore postgres.AgentMemoryStore) error {
	if err := registry.Register(mcp.Tool{
		Name:         "memory_store",
		Description:  "Store a key-value memory entry for the calling agent. Supports optional TTL and metadata.",
		InputSchema:  memoryStoreSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeMemoryStoreHandler(memoryStore),
	}); err != nil {
		return fmt.Errorf("register memory_store: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "memory_recall",
		Description:  "Recall memory entries by exact key, keyword search, or semantic fuzzy match for the calling agent.",
		InputSchema:  memoryRecallSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeMemoryRecallHandler(memoryStore),
	}); err != nil {
		return fmt.Errorf("register memory_recall: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "get_thread",
		Description:  "Retrieve conversation messages for a thread by thread ID.",
		InputSchema:  getThreadSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Handler:      makeGetThreadHandler(memoryStore),
	}); err != nil {
		return fmt.Errorf("register get_thread: %w", err)
	}

	return nil
}

// makeMemoryStoreHandler returns a handler that stores a memory entry.
func makeMemoryStoreHandler(store postgres.AgentMemoryStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input memoryStoreInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.Key == "" {
			return nil, fmt.Errorf("key is required")
		}
		if input.Value == "" {
			return nil, fmt.Errorf("value is required")
		}

		// Extract identity from MCP auth context.
		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		var ttl time.Duration
		if input.TTLSeconds != nil && *input.TTLSeconds > 0 {
			ttl = time.Duration(*input.TTLSeconds) * time.Second
		}

		if err := store.Store(ctx, ac.AgentID, ac.TenantID, input.Key, input.Value, ttl, input.Metadata); err != nil {
			slog.Error("mcp/memory: store failed",
				"agent_id", ac.AgentID,
				"tenant_id", ac.TenantID,
				"key", input.Key,
				"error", err,
			)
			return nil, fmt.Errorf("failed to store memory: %w", err)
		}

		slog.Info("mcp/memory: stored",
			"agent_id", ac.AgentID,
			"tenant_id", ac.TenantID,
			"key", input.Key,
			"size_bytes", len(input.Value),
		)

		return memoryStoreOutput{
			Stored:    true,
			Key:       input.Key,
			SizeBytes: int64(len(input.Value)),
		}, nil
	}
}

// makeMemoryRecallHandler returns a handler that recalls memory entries
// by exact key or keyword search.
func makeMemoryRecallHandler(store postgres.AgentMemoryStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input memoryRecallInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		// Must provide at least one of key or query.
		hasKey := input.Key != nil && *input.Key != ""
		hasQuery := input.Query != nil && *input.Query != ""

		if !hasKey && !hasQuery {
			return nil, fmt.Errorf("must provide either key or query")
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

		var entries []postgres.MemoryEntry

		semantic := input.Semantic != nil && *input.Semantic

		if hasKey {
			// Exact key recall.
			entry, err := store.Recall(ctx, ac.AgentID, ac.TenantID, *input.Key)
			if err != nil {
				slog.Error("mcp/memory: recall failed",
					"agent_id", ac.AgentID,
					"key", *input.Key,
					"error", err,
				)
				return nil, fmt.Errorf("failed to recall memory: %w", err)
			}
			if entry != nil {
				entries = append(entries, *entry)
			}
		} else if semantic {
			// Fuzzy/semantic search using pg_trgm trigram similarity.
			var err error
			entries, err = store.SemanticSearch(ctx, ac.AgentID, ac.TenantID, *input.Query, limit)
			if err != nil {
				slog.Error("mcp/memory: semantic search failed",
					"agent_id", ac.AgentID,
					"query", *input.Query,
					"error", err,
				)
				return nil, fmt.Errorf("failed to semantic search memory: %w", err)
			}
		} else {
			// Keyword search (ILIKE).
			var err error
			entries, err = store.Search(ctx, ac.AgentID, ac.TenantID, *input.Query, limit)
			if err != nil {
				slog.Error("mcp/memory: search failed",
					"agent_id", ac.AgentID,
					"query", *input.Query,
					"error", err,
				)
				return nil, fmt.Errorf("failed to search memory: %w", err)
			}
		}

		if entries == nil {
			entries = []postgres.MemoryEntry{}
		}

		slog.Info("mcp/memory: recall",
			"agent_id", ac.AgentID,
			"tenant_id", ac.TenantID,
			"count", len(entries),
		)

		return memoryRecallOutput{
			Entries: entries,
			Count:   len(entries),
		}, nil
	}
}

// makeGetThreadHandler returns a handler that retrieves conversation messages
// for a thread by ID. Threads are stored as memory entries with key prefix
// "thread:{thread_id}:*".
func makeGetThreadHandler(store postgres.AgentMemoryStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var input getThreadInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if input.ThreadID == "" {
			return nil, fmt.Errorf("thread_id is required")
		}

		// Extract identity from MCP auth context.
		ac, ok := mcp.GetMCPAuth(ctx)
		if !ok {
			return nil, fmt.Errorf("missing auth context")
		}

		limit := 50
		if input.Limit != nil && *input.Limit > 0 {
			limit = *input.Limit
		}

		entries, err := store.GetThread(ctx, ac.AgentID, ac.TenantID, input.ThreadID, limit)
		if err != nil {
			slog.Error("mcp/memory: get_thread failed",
				"agent_id", ac.AgentID,
				"tenant_id", ac.TenantID,
				"thread_id", input.ThreadID,
				"error", err,
			)
			return nil, fmt.Errorf("failed to get thread: %w", err)
		}

		if entries == nil {
			entries = []postgres.MemoryEntry{}
		}

		slog.Info("mcp/memory: get_thread",
			"agent_id", ac.AgentID,
			"tenant_id", ac.TenantID,
			"thread_id", input.ThreadID,
			"count", len(entries),
		)

		return getThreadOutput{
			ThreadID: input.ThreadID,
			Messages: entries,
			Count:    len(entries),
		}, nil
	}
}
