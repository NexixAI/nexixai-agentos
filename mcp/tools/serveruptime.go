package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// serverUptimeInputSchema is the JSON Schema for server_uptime (no required parameters).
var serverUptimeInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

// serverUptimeResult is the tool's return value.
type serverUptimeResult struct {
	UptimeSeconds float64 `json:"uptime_seconds"`
}

// RegisterServerUptimeTool registers the server_uptime tool with the given registry.
func RegisterServerUptimeTool(registry *mcp.ToolRegistry) error {
	return registry.Register(mcp.Tool{
		Name:         "server_uptime",
		Description:  "Reads /proc/uptime and returns the server uptime in seconds.",
		InputSchema:  serverUptimeInputSchema,
		MinClearance: mcp.ClearancePublic,
		Static:       true,
		Handler:      makeServerUptimeHandler("/proc/uptime"),
	})
}

// makeServerUptimeHandler returns a ToolHandler that reads uptime from the given path.
func makeServerUptimeHandler(path string) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Error("server_uptime: failed to read uptime", "path", path, "error", err)
			return nil, fmt.Errorf("server_uptime: failed to read %s: %w", path, err)
		}

		fields := strings.Fields(string(data))
		if len(fields) < 1 {
			return nil, fmt.Errorf("server_uptime: unexpected format in %s", path)
		}

		uptime, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return nil, fmt.Errorf("server_uptime: failed to parse uptime value %q: %w", fields[0], err)
		}

		slog.Info("server_uptime: read uptime", "seconds", uptime)
		return serverUptimeResult{UptimeSeconds: uptime}, nil
	}
}
