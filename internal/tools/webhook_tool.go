package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/ssrf"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// WebhookTool executes a tool call by POSTing to a webhook URL.
type WebhookTool struct {
	name           string
	description    string
	inputSchema    map[string]any
	webhookURL     string
	headers        map[string]string
	timeout        time.Duration
	// AllowLoopback disables SSRF checking for loopback addresses (testing only).
	AllowLoopback bool
}

// NewWebhookTool creates a WebhookTool from a CustomTool definition.
func NewWebhookTool(ct types.CustomTool) *WebhookTool {
	timeout := 30 * time.Second
	if ct.TimeoutMs > 0 {
		timeout = time.Duration(ct.TimeoutMs) * time.Millisecond
	}
	return &WebhookTool{
		name:        ct.Name,
		description: ct.Description,
		inputSchema: ct.InputSchema,
		webhookURL:  ct.WebhookURL,
		headers:     ct.Headers,
		timeout:     timeout,
	}
}

func (t *WebhookTool) Name() string        { return t.name }
func (t *WebhookTool) Description() string  { return t.description }
func (t *WebhookTool) InputSchema() map[string]any {
	if t.inputSchema != nil {
		return t.inputSchema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *WebhookTool) Execute(ctx context.Context, input string) (string, error) {
	// Reject non-http/https schemes before doing any work (v9.0 L-6).
	parsed, err := url.Parse(t.webhookURL)
	if err != nil {
		return "", fmt.Errorf("invalid webhook URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported webhook URL scheme %q (only http and https are allowed)", parsed.Scheme)
	}

	// Build SSRF-safe HTTP client using consolidated ssrf package (v9.0 L-9).
	allowLoopback := t.AllowLoopback
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address: %w", err)
			}
			ips, err := ssrf.ResolveHost(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if allowLoopback && ip.IsLoopback() {
					continue
				}
				if ssrf.IsPrivateIP(ip) {
					return nil, fmt.Errorf("request to private IP %s is blocked", ip)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	client := &http.Client{Transport: transport, Timeout: t.timeout}

	// Parse input arguments.
	var args any
	if input != "" {
		if err := json.Unmarshal([]byte(input), &args); err != nil {
			args = input
		}
	}

	body, err := json.Marshal(map[string]any{"input": args})
	if err != nil {
		return "", fmt.Errorf("failed to marshal webhook body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.webhookURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("invalid webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response up to 32KB.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("error reading webhook response: %w", err)
	}

	result := string(raw)
	if len(raw) > maxBodyBytes {
		result = string(raw[:maxBodyBytes])
	}

	return result, nil
}
