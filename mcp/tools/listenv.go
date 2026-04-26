package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// listenvInputSchema is the JSON Schema for the list_env tool (no required parameters).
var listenvInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

// listenvResult is the tool's return value.
type listenvResult struct {
	Names []string `json:"names"`
	Count int      `json:"count"`
}

// RegisterListenvTool registers the list_env tool with the given registry.
func RegisterListenvTool(registry *mcp.ToolRegistry) error {
	return registry.Register(mcp.Tool{
		Name:         "list_env",
		Description:  "Returns the names (not values) of all environment variables. Used for post-deploy verification.",
		InputSchema:  listenvInputSchema,
		MinClearance: mcp.ClearanceAdmin,
		Static:       true,
		Handler:      makeListenvHandler(os.Environ),
	})
}

// makeListenvHandler returns a ToolHandler that lists environment variable names.
// The environFn parameter allows injecting a test double.
func makeListenvHandler(environFn func() []string) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		envPairs := environFn()
		names := make([]string, 0, len(envPairs))
		for _, pair := range envPairs {
			if k, _, ok := strings.Cut(pair, "="); ok && k != "" {
				names = append(names, k)
			}
		}
		sort.Strings(names)

		slog.Info("list_env: returning environment variable names", "count", len(names))
		if len(names) == 0 {
			return nil, fmt.Errorf("list_env: no environment variables found")
		}

		return listenvResult{
			Names: names,
			Count: len(names),
		}, nil
	}
}
