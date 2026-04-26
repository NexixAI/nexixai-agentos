package tools

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// queryMetricsInputSchema is the JSON Schema for the query_metrics tool.
var queryMetricsInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "PromQL query to execute against Prometheus"
		}
	},
	"required": ["query"],
	"additionalProperties": false
}`)

// getAlertsInputSchema is the JSON Schema for the get_alerts tool.
var getAlertsInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

// getContainerStatusInputSchema is the JSON Schema for the get_container_status tool.
var getContainerStatusInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"container_name": {
			"type": "string",
			"description": "Name of the Docker container to look up"
		},
		"endpoint_id": {
			"type": "string",
			"description": "Portainer endpoint ID (default: 3)"
		}
	},
	"required": [],
	"additionalProperties": false
}`)

// getAuditLogInputSchema is the JSON Schema for the get_audit_log tool.
var getAuditLogInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"agent_id": {
			"type": "string",
			"description": "Filter audit entries by agent/principal ID"
		},
		"tenant_id": {
			"type": "string",
			"description": "Filter audit entries by tenant ID"
		},
		"limit": {
			"type": "integer",
			"description": "Maximum number of entries to return (default: 50)"
		}
	},
	"additionalProperties": false
}`)

// AuditFilter specifies filtering criteria for audit log queries.
type AuditFilter struct {
	AgentID  string
	TenantID string
	Limit    int
}

// AuditEntry represents a single audit log entry returned by the audit store.
type AuditEntry struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	TenantID  string    `json:"tenant_id"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// ObserveDeps holds the dependencies required by RegisterObserveTools.
type ObserveDeps struct {
	PromClient   metrics.PrometheusClient
	PortainerURL string                // e.g. "https://192.168.50.57:9443/api"
	PortainerKey string                // X-API-Key value for Portainer
	AuditStore   *InMemoryAuditStore   // may be nil if audit reading is not available
}

// RegisterObserveTools registers the query_metrics, get_alerts,
// get_container_status, and get_audit_log tools with the given registry.
func RegisterObserveTools(registry *mcp.ToolRegistry, deps ObserveDeps) error {
	if err := registry.Register(mcp.Tool{
		Name:         "query_metrics",
		Description:  "Executes a PromQL instant query against Prometheus and returns the result data.",
		InputSchema:  queryMetricsInputSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Category:     "observe",
		Handler:      makeQueryMetricsHandler(deps.PromClient),
	}); err != nil {
		return fmt.Errorf("register query_metrics: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "get_alerts",
		Description:  "Returns all currently active Prometheus alerts.",
		InputSchema:  getAlertsInputSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Category:     "observe",
		Handler:      makeGetAlertsHandler(deps.PromClient),
	}); err != nil {
		return fmt.Errorf("register get_alerts: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "get_container_status",
		Description:  "Queries Portainer for the status of a Docker container by name. Returns name, state, status, image, created time, and ports.",
		InputSchema:  getContainerStatusInputSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Category:     "observe",
		Handler:      makeGetContainerStatusHandler(deps.PortainerURL, deps.PortainerKey),
	}); err != nil {
		return fmt.Errorf("register get_container_status: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "get_audit_log",
		Description:  "Reads audit log entries with optional filters for agent ID, tenant ID, and result limit.",
		InputSchema:  getAuditLogInputSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Category:     "observe",
		Handler:      makeGetAuditLogHandler(deps.AuditStore),
	}); err != nil {
		return fmt.Errorf("register get_audit_log: %w", err)
	}

	if err := registry.Register(mcp.Tool{
		Name:         "read_container_logs",
		Description:  "Fetches the last N log lines (stdout + stderr) from a Docker container via Portainer.",
		InputSchema:  readContainerLogsInputSchema,
		MinClearance: mcp.ClearanceInternal,
		Static:       true,
		Category:     "observe",
		Handler:      makeReadContainerLogsHandler(deps.PortainerURL, deps.PortainerKey),
	}); err != nil {
		return fmt.Errorf("register read_container_logs: %w", err)
	}

	return nil
}

// readContainerLogsInputSchema is the JSON Schema for the read_container_logs tool.
var readContainerLogsInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"container_name": {
			"type": "string",
			"description": "Name of the Docker container"
		},
		"tail": {
			"type": "integer",
			"description": "Number of log lines to return (default: 100, max: 1000)"
		},
		"endpoint_id": {
			"type": "string",
			"description": "Portainer endpoint ID (default: '3' for orchestrator)"
		}
	},
	"required": ["container_name"],
	"additionalProperties": false
}`)

// queryMetricsArgs holds the parsed arguments for query_metrics.
type queryMetricsArgs struct {
	Query string `json:"query"`
}

// makeQueryMetricsHandler returns a ToolHandler that dispatches PromQL queries
// to the given PrometheusClient.
func makeQueryMetricsHandler(client metrics.PrometheusClient) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var args queryMetricsArgs
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("query_metrics: invalid arguments: %w", err)
		}
		if args.Query == "" {
			return nil, fmt.Errorf("query_metrics: missing required parameter 'query'")
		}

		slog.Info("query_metrics: executing", "query", args.Query)

		result, err := client.Query(ctx, args.Query)
		if err != nil {
			return nil, fmt.Errorf("query_metrics: %w", err)
		}

		return result, nil
	}
}

// makeGetAlertsHandler returns a ToolHandler that retrieves active alerts
// from the given PrometheusClient.
func makeGetAlertsHandler(client metrics.PrometheusClient) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		slog.Info("get_alerts: fetching active alerts")

		alerts, err := client.GetAlerts(ctx)
		if err != nil {
			return nil, fmt.Errorf("get_alerts: %w", err)
		}

		return alerts, nil
	}
}

// containerStatusArgs holds the parsed arguments for get_container_status.
type containerStatusArgs struct {
	ContainerName string `json:"container_name"`
	EndpointID    string `json:"endpoint_id"`
}

// portainerContainer is the subset of Portainer's container JSON we care about.
type portainerContainer struct {
	ID      string          `json:"Id"`
	Names   []string        `json:"Names"`
	State   string          `json:"State"`
	Status  string          `json:"Status"`
	Image   string          `json:"Image"`
	Created int64           `json:"Created"`
	Ports   json.RawMessage `json:"Ports"`
}

// containerStatusResult is the tool's return value.
type containerStatusResult struct {
	Name    string          `json:"name"`
	State   string          `json:"state"`
	Status  string          `json:"status"`
	Image   string          `json:"image"`
	Created string          `json:"created"`
	Ports   json.RawMessage `json:"ports"`
}

// portainerHTTPClient is the HTTP client used for Portainer API calls.
// It tolerates self-signed certificates on the internal Portainer endpoint.
var portainerHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // internal self-signed cert behind nginx proxy
		},
	},
}

// makeGetContainerStatusHandler returns a ToolHandler that queries the
// Portainer API for a Docker container by name.
func makeGetContainerStatusHandler(portainerURL, portainerKey string) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		if portainerKey == "" {
			return nil, fmt.Errorf("get_container_status: PORTAINER_API_KEY is not set — generate an API token in Portainer (Settings → Users → API tokens) and set PORTAINER_API_KEY")
		}

		var args containerStatusArgs
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("get_container_status: invalid arguments: %w", err)
		}
		if args.EndpointID == "" {
			args.EndpointID = "3"
		}

		slog.Info("get_container_status: querying",
			"container_name", args.ContainerName,
			"endpoint_id", args.EndpointID,
		)

		url := fmt.Sprintf("%s/endpoints/%s/docker/containers/json?all=true",
			strings.TrimRight(portainerURL, "/"), args.EndpointID)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("get_container_status: build request: %w", err)
		}
		req.Header.Set("X-API-Key", portainerKey)

		resp, err := portainerHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("get_container_status: portainer request: %w", err)
		}
		defer func() { _ = resp.Body.Close() }() //nolint:errcheck // best-effort close

		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("get_container_status: portainer returned 401 Unauthorized — PORTAINER_API_KEY may be expired or invalid; regenerate it in Portainer (Settings → Users → API tokens) and update the deployment secret")
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("get_container_status: portainer returned %d: %s", resp.StatusCode, string(body))
		}

		var containers []portainerContainer
		if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
			return nil, fmt.Errorf("get_container_status: decode response: %w", err)
		}

		// If no container name, return all containers
		if args.ContainerName == "" {
			var results []containerStatusResult
			for _, c := range containers {
				name := ""
				if len(c.Names) > 0 {
					name = strings.TrimPrefix(c.Names[0], "/")
				}
				results = append(results, containerStatusResult{
					Name:    name,
					State:   c.State,
					Status:  c.Status,
					Image:   c.Image,
					Created: time.Unix(c.Created, 0).UTC().Format(time.RFC3339),
					Ports:   c.Ports,
				})
			}
			return results, nil
		}

		// Portainer prefixes container names with "/".
		target := "/" + args.ContainerName
		for _, c := range containers {
			for _, name := range c.Names {
				if name == target || name == args.ContainerName {
					return containerStatusResult{
						Name:    strings.TrimPrefix(name, "/"),
						State:   c.State,
						Status:  c.Status,
						Image:   c.Image,
						Created: time.Unix(c.Created, 0).UTC().Format(time.RFC3339),
						Ports:   c.Ports,
					}, nil
				}
			}
		}

		return nil, fmt.Errorf("get_container_status: container %q not found", args.ContainerName)
	}
}

// getAuditLogArgs holds the parsed arguments for get_audit_log.
type getAuditLogArgs struct {
	AgentID  string `json:"agent_id"`
	TenantID string `json:"tenant_id"`
	Limit    int    `json:"limit"`
}

// makeGetAuditLogHandler returns a ToolHandler that reads audit log entries
// via the given InMemoryAuditStore.
func makeGetAuditLogHandler(store *InMemoryAuditStore) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var args getAuditLogArgs
		if params != nil {
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, fmt.Errorf("get_audit_log: invalid arguments: %w", err)
			}
		}
		if args.Limit <= 0 {
			args.Limit = 50
		}

		slog.Info("get_audit_log: reading",
			"agent_id", args.AgentID,
			"tenant_id", args.TenantID,
			"limit", args.Limit,
		)

		if store == nil {
			return nil, fmt.Errorf("get_audit_log: audit store not configured")
		}

		entries, err := store.Read(ctx, AuditFilter{
			AgentID:  args.AgentID,
			TenantID: args.TenantID,
			Limit:    args.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("get_audit_log: %w", err)
		}

		return entries, nil
	}
}

// ---------------------------------------------------------------------------
// read_container_logs
// ---------------------------------------------------------------------------

type readContainerLogsArgs struct {
	ContainerName string `json:"container_name"`
	Tail          int    `json:"tail"`
	EndpointID    string `json:"endpoint_id"`
}

type containerLogsResult struct {
	ContainerName string `json:"container_name"`
	Logs          string `json:"logs"`
	Lines         int    `json:"lines"`
	Timestamp     string `json:"timestamp"`
}

func makeReadContainerLogsHandler(portainerURL, portainerKey string) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		if portainerKey == "" {
			return nil, fmt.Errorf("read_container_logs: PORTAINER_API_KEY is not set")
		}

		var args readContainerLogsArgs
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("read_container_logs: invalid arguments: %w", err)
		}
		if args.ContainerName == "" {
			return nil, fmt.Errorf("read_container_logs: missing required parameter 'container_name'")
		}
		if args.EndpointID == "" {
			args.EndpointID = "3"
		}
		if args.Tail <= 0 {
			args.Tail = 100
		}
		if args.Tail > 1000 {
			args.Tail = 1000
		}

		slog.Info("read_container_logs: fetching",
			"container_name", args.ContainerName,
			"tail", args.Tail,
			"endpoint_id", args.EndpointID,
		)

		// First, find the container ID by name (Portainer logs API needs the ID)
		listURL := fmt.Sprintf("%s/endpoints/%s/docker/containers/json?all=true",
			strings.TrimRight(portainerURL, "/"), args.EndpointID)

		listReq, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return nil, fmt.Errorf("read_container_logs: build list request: %w", err)
		}
		listReq.Header.Set("X-API-Key", portainerKey)

		listResp, err := portainerHTTPClient.Do(listReq)
		if err != nil {
			return nil, fmt.Errorf("read_container_logs: portainer list request: %w", err)
		}
		defer func() { _ = listResp.Body.Close() }() //nolint:errcheck // best-effort close

		if listResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(listResp.Body, 512))
			return nil, fmt.Errorf("read_container_logs: portainer list returned %d: %s", listResp.StatusCode, string(body))
		}

		var containers []portainerContainer
		if err := json.NewDecoder(listResp.Body).Decode(&containers); err != nil {
			return nil, fmt.Errorf("read_container_logs: decode container list: %w", err)
		}

		// Find the container by name
		var containerID string
		target := "/" + args.ContainerName
		for _, c := range containers {
			for _, name := range c.Names {
				if name == target || name == args.ContainerName {
					containerID = c.ID
					break
				}
			}
			if containerID != "" {
				break
			}
		}
		if containerID == "" {
			return nil, fmt.Errorf("read_container_logs: container %q not found", args.ContainerName)
		}

		// Fetch logs
		logsURL := fmt.Sprintf("%s/endpoints/%s/docker/containers/%s/logs?stdout=1&stderr=1&tail=%d&timestamps=1",
			strings.TrimRight(portainerURL, "/"), args.EndpointID, containerID, args.Tail)

		logsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, logsURL, nil)
		if err != nil {
			return nil, fmt.Errorf("read_container_logs: build logs request: %w", err)
		}
		logsReq.Header.Set("X-API-Key", portainerKey)

		logsResp, err := portainerHTTPClient.Do(logsReq)
		if err != nil {
			return nil, fmt.Errorf("read_container_logs: portainer logs request: %w", err)
		}
		defer func() { _ = logsResp.Body.Close() }() //nolint:errcheck // best-effort close

		if logsResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(logsResp.Body, 512))
			return nil, fmt.Errorf("read_container_logs: portainer logs returned %d: %s", logsResp.StatusCode, string(body))
		}

		// Docker log stream has 8-byte headers per frame. Strip them for clean output.
		raw, err := io.ReadAll(io.LimitReader(logsResp.Body, 1024*1024)) // 1MB max
		if err != nil {
			return nil, fmt.Errorf("read_container_logs: read logs: %w", err)
		}
		logs := stripDockerLogHeaders(raw)
		lines := strings.Count(logs, "\n")

		return containerLogsResult{
			ContainerName: args.ContainerName,
			Logs:          logs,
			Lines:         lines,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
		}, nil
	}
}

// stripDockerLogHeaders removes the 8-byte Docker log stream headers from each
// line. Docker multiplexed streams prefix each frame with [stream_type(1) + 0(3) + size(4)].
func stripDockerLogHeaders(raw []byte) string {
	var sb strings.Builder
	for len(raw) >= 8 {
		// Header: 1 byte stream type, 3 bytes padding, 4 bytes big-endian size
		size := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
		raw = raw[8:]
		if size > len(raw) {
			size = len(raw)
		}
		sb.Write(raw[:size])
		raw = raw[size:]
	}
	// If no valid headers were found, return raw as-is (plain text logs)
	if sb.Len() == 0 {
		return string(raw)
	}
	return sb.String()
}
