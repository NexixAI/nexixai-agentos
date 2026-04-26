package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/ssrf"
)

const (
	maxBodyBytes         = 32 * 1024 // 32KB
	httpFetchTimeout     = 30 * time.Second
	httpFetchMaxRedirect = 5
)

// isPrivateIP delegates to the consolidated ssrf package (v9.0 L-9).
func isPrivateIP(ip net.IP) bool { return ssrf.IsPrivateIP(ip) }

func validateFetchTarget(ctx context.Context, parsed *url.URL, allowLoopback bool) error {
	if parsed == nil {
		return fmt.Errorf("request URL is nil")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("request URL has no host")
	}

	return ssrf.RejectPrivateIPs(ctx, host, allowLoopback)
}

func safeDialContext(allowLoopback bool, dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
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
	}
}

func httpFetchCheckRedirect(allowLoopback bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= httpFetchMaxRedirect {
			return fmt.Errorf("stopped after %d redirects", httpFetchMaxRedirect)
		}
		return validateFetchTarget(req.Context(), req.URL, allowLoopback)
	}
}

// HTTPFetchTool performs HTTP requests with SSRF protection.
type HTTPFetchTool struct {
	// AllowLoopback disables SSRF checking for loopback addresses (testing only).
	AllowLoopback bool
}

func (t *HTTPFetchTool) Name() string { return "http_fetch" }

func (t *HTTPFetchTool) Description() string {
	return "Fetch a URL over HTTP/HTTPS. Returns status, headers, and body (truncated to 32KB)."
}

func (t *HTTPFetchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":     map[string]any{"type": "string", "description": "The URL to fetch"},
			"method":  map[string]any{"type": "string", "description": "HTTP method (GET, POST, etc.)", "default": "GET"},
			"headers": map[string]any{"type": "object", "description": "Request headers"},
			"body":    map[string]any{"type": "string", "description": "Request body"},
		},
		"required": []string{"url"},
	}
}

type httpFetchInput struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type httpFetchOutput struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func (t *HTTPFetchTool) Execute(ctx context.Context, input string) (string, error) {
	var in httpFetchInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if in.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if in.Method == "" {
		in.Method = "GET"
	}

	allowLoopback := t.AllowLoopback
	if parsed, err := url.Parse(in.URL); err != nil {
		return "", fmt.Errorf("invalid request URL: %w", err)
	} else if err := validateFetchTarget(ctx, parsed, allowLoopback); err != nil {
		return "", err
	}

	// Build an HTTP client with SSRF-safe dialer.
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext: safeDialContext(allowLoopback, dialer),
	}

	client := &http.Client{
		Transport:     transport,
		Timeout:       httpFetchTimeout,
		CheckRedirect: httpFetchCheckRedirect(allowLoopback),
	}

	var bodyReader io.Reader
	if in.Body != "" {
		bodyReader = strings.NewReader(in.Body)
	}

	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}

	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read body up to maxBodyBytes + 1 to detect truncation.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBodyBytes+1)))
	if err != nil {
		return "", fmt.Errorf("error reading response: %w", err)
	}

	body := string(raw)
	if len(raw) > maxBodyBytes {
		body = string(raw[:maxBodyBytes])
	}

	// Collect response headers (first value only).
	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	out := httpFetchOutput{
		Status:  resp.StatusCode,
		Headers: respHeaders,
		Body:    body,
	}

	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("error encoding response: %w", err)
	}
	return string(data), nil
}
