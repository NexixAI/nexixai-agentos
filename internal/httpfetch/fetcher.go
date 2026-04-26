package httpfetch

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/ssrf"
)

// DefaultMaxSize is the default maximum response body size (10 MB).
const DefaultMaxSize int64 = 10 * 1024 * 1024

// DefaultTimeout is the default HTTP request timeout.
const DefaultTimeout = 30 * time.Second

const maxRedirects = 5

// FetchResult holds the result of a URL fetch.
type FetchResult struct {
	URL          string `json:"url"`
	StatusCode   int    `json:"status_code"`
	ContentType  string `json:"content_type"`
	Body         string `json:"body"`
	BytesFetched int64  `json:"bytes_fetched"`
}

// PingResult holds the result of a URL ping (HEAD request).
type PingResult struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	LatencyMs  int64  `json:"latency_ms"`
}

// Fetcher is the interface for URL fetching. Implementations must be safe
// for concurrent use.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (*FetchResult, error)
	Request(ctx context.Context, method, rawURL string, headers map[string]string, body string) (*FetchResult, error)
	Ping(ctx context.Context, rawURL string) (*PingResult, error)
}

// HTTPFetcher fetches URLs over HTTP with domain filtering, size limits,
// timeout enforcement, and SSRF protection.
type HTTPFetcher struct {
	allowlist     []string
	blocklist     []string
	maxSize       int64
	timeout       time.Duration
	client        *http.Client
	skipSSRF      bool // allow private IPs (for internal lab use)
	skipTLSVerify bool // skip TLS certificate verification
}

// Option configures an HTTPFetcher.
type Option func(*HTTPFetcher)

// WithAllowlist sets the domain allowlist. When non-empty, only domains in
// the list are permitted.
func WithAllowlist(domains []string) Option {
	return func(f *HTTPFetcher) {
		f.allowlist = domains
	}
}

// WithBlocklist sets the domain blocklist. Domains in the list are always
// rejected, even if they appear in the allowlist.
func WithBlocklist(domains []string) Option {
	return func(f *HTTPFetcher) {
		f.blocklist = domains
	}
}

// WithMaxSize sets the maximum response body size in bytes.
func WithMaxSize(n int64) Option {
	return func(f *HTTPFetcher) {
		f.maxSize = n
	}
}

// WithTimeout sets the HTTP request timeout.
func WithTimeout(d time.Duration) Option {
	return func(f *HTTPFetcher) {
		f.timeout = d
	}
}

// WithSkipSSRF disables SSRF protection, allowing private IPs.
// Only use this for internal/lab deployments.
func WithSkipSSRF() Option {
	return func(f *HTTPFetcher) {
		f.skipSSRF = true
	}
}

// WithSkipTLSVerify disables TLS certificate verification.
// Only use this for internal/lab deployments with self-signed certs.
func WithSkipTLSVerify() Option {
	return func(f *HTTPFetcher) {
		f.skipTLSVerify = true
	}
}

// NewHTTPFetcher creates a new HTTPFetcher with the given options.
func NewHTTPFetcher(opts ...Option) *HTTPFetcher {
	f := &HTTPFetcher{
		maxSize: DefaultMaxSize,
		timeout: DefaultTimeout,
	}
	for _, opt := range opts {
		opt(f)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if f.skipTLSVerify {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // explicitly requested via WithSkipTLSVerify
		}
	}
	dialer := &net.Dialer{Timeout: f.timeout}
	transport.DialContext = f.safeDialContext(dialer)
	f.client = &http.Client{
		Timeout:       f.timeout,
		Transport:     transport,
		CheckRedirect: f.checkRedirect,
	}
	return f
}

func (f *HTTPFetcher) safeDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("httpfetch: invalid dial target %q: %w", addr, err)
		}

		if f.skipSSRF {
			return dialer.DialContext(ctx, network, addr)
		}

		ips, err := ssrf.ResolveHost(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ssrf.IsPrivateIP(ip) {
				return nil, fmt.Errorf("httpfetch: SSRF protection — host %q resolves to private IP %s", host, ip)
			}
		}

		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
}

func (f *HTTPFetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("httpfetch: stopped after %d redirects", maxRedirects)
	}
	return f.validateURL(req.URL)
}

func (f *HTTPFetcher) validateURL(parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("httpfetch: URL is nil")
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("httpfetch: unsupported scheme %q (only http and https are allowed)", parsed.Scheme)
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("httpfetch: URL has no host")
	}

	if !f.skipSSRF {
		if err := rejectPrivateIP(host); err != nil {
			return err
		}
	}

	return f.checkDomainFilters(host)
}

// Fetch performs a GET request to the given URL and returns the result.
// It enforces domain filtering, SSRF protection, size limits, and timeout.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string) (*FetchResult, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: invalid URL: %w", err)
	}

	if err := f.validateURL(parsed); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "AgentOS-FetchURL/1.0")

	slog.Debug("httpfetch: fetching URL", "url", rawURL)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close in defer

	contentType := resp.Header.Get("Content-Type")

	// Read up to maxSize+1 bytes to detect truncation.
	lr := io.LimitReader(resp.Body, f.maxSize+1)
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to read response body: %w", err)
	}

	truncated := false
	if int64(len(body)) > f.maxSize {
		body = body[:f.maxSize]
		truncated = true
	}

	bodyStr := string(body)

	// Content-type-based transformations.
	if strings.Contains(contentType, "text/html") {
		bodyStr = htmlToMarkdown(bodyStr)
	} else if strings.Contains(contentType, "application/json") {
		bodyStr = prettyPrintJSON(bodyStr)
	}

	if truncated {
		bodyStr += "\n[truncated]"
	}

	result := &FetchResult{
		URL:          rawURL,
		StatusCode:   resp.StatusCode,
		ContentType:  contentType,
		Body:         bodyStr,
		BytesFetched: int64(len(body)),
	}

	slog.Debug("httpfetch: fetch complete",
		"url", rawURL,
		"status", resp.StatusCode,
		"content_type", contentType,
		"bytes", result.BytesFetched,
		"truncated", truncated,
	)

	return result, nil
}

// allowedMethods is the set of HTTP methods supported by Request.
var allowedMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodPost:    true,
	http.MethodPut:     true,
	http.MethodDelete:  true,
	http.MethodPatch:   true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// methodAllowsBody returns true if the HTTP method conventionally carries a
// request body.
func methodAllowsBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

// Request performs an HTTP request with the given method, URL, headers, and
// optional body. It enforces the same domain filtering, SSRF protection, and
// size limits as Fetch.
func (f *HTTPFetcher) Request(ctx context.Context, method, rawURL string, headers map[string]string, body string) (*FetchResult, error) {
	method = strings.ToUpper(method)
	if !allowedMethods[method] {
		return nil, fmt.Errorf("httpfetch: unsupported HTTP method %q", method)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: invalid URL: %w", err)
	}

	if err := f.validateURL(parsed); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	var bodyReader io.Reader
	if methodAllowsBody(method) && body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "AgentOS-HTTPRequest/1.0")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	slog.Debug("httpfetch: request", "method", method, "url", rawURL)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close in defer

	contentType := resp.Header.Get("Content-Type")

	lr := io.LimitReader(resp.Body, f.maxSize+1)
	respBody, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to read response body: %w", err)
	}

	truncated := false
	if int64(len(respBody)) > f.maxSize {
		respBody = respBody[:f.maxSize]
		truncated = true
	}

	bodyStr := string(respBody)

	if strings.Contains(contentType, "text/html") {
		bodyStr = htmlToMarkdown(bodyStr)
	} else if strings.Contains(contentType, "application/json") {
		bodyStr = prettyPrintJSON(bodyStr)
	}

	if truncated {
		bodyStr += "\n[truncated]"
	}

	result := &FetchResult{
		URL:          rawURL,
		StatusCode:   resp.StatusCode,
		ContentType:  contentType,
		Body:         bodyStr,
		BytesFetched: int64(len(respBody)),
	}

	slog.Debug("httpfetch: request complete",
		"method", method,
		"url", rawURL,
		"status", resp.StatusCode,
		"content_type", contentType,
		"bytes", result.BytesFetched,
		"truncated", truncated,
	)

	return result, nil
}

// Ping performs a HEAD request to the given URL and returns the status code
// and round-trip latency. It enforces the same domain filtering and SSRF
// protection as Fetch.
func (f *HTTPFetcher) Ping(ctx context.Context, rawURL string) (*PingResult, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: invalid URL: %w", err)
	}

	if err := f.validateURL(parsed); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "AgentOS-Ping/1.0")

	slog.Debug("httpfetch: ping", "url", rawURL)

	start := time.Now()
	resp, err := f.client.Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("httpfetch: ping failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close in defer

	result := &PingResult{
		URL:        rawURL,
		StatusCode: resp.StatusCode,
		LatencyMs:  latencyMs,
	}

	slog.Debug("httpfetch: ping complete",
		"url", rawURL,
		"status", resp.StatusCode,
		"latency_ms", latencyMs,
	)

	return result, nil
}

// rejectPrivateIP checks if the host resolves to a private/reserved IP range
// and rejects it to prevent SSRF attacks.
// Delegates to the consolidated ssrf package (v9.0 L-1, L-9).
func rejectPrivateIP(host string) error {
	return ssrf.RejectPrivateIPs(context.Background(), host, false)
}

// isPrivateIP delegates to the consolidated ssrf package (v9.0 L-9).
func isPrivateIP(ip net.IP) bool { return ssrf.IsPrivateIP(ip) }

// checkDomainFilters validates the host against allowlist and blocklist.
func (f *HTTPFetcher) checkDomainFilters(host string) error {
	host = strings.ToLower(host)

	for _, blocked := range f.blocklist {
		if strings.ToLower(blocked) == host {
			return fmt.Errorf("httpfetch: domain %q is blocklisted", host)
		}
	}

	if len(f.allowlist) > 0 {
		for _, allowed := range f.allowlist {
			if strings.ToLower(allowed) == host {
				return nil
			}
		}
		return fmt.Errorf("httpfetch: domain %q is not in the allowlist", host)
	}

	return nil
}

// prettyPrintJSON attempts to pretty-print JSON. If it fails, the original
// string is returned unchanged.
func prettyPrintJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// --- Simple regex-based HTML to markdown conversion ---

var (
	reScript    = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle     = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	reH1        = regexp.MustCompile(`(?i)<h1[^>]*>(.*?)</h1>`)
	reH2        = regexp.MustCompile(`(?i)<h2[^>]*>(.*?)</h2>`)
	reH3        = regexp.MustCompile(`(?i)<h3[^>]*>(.*?)</h3>`)
	reH4        = regexp.MustCompile(`(?i)<h4[^>]*>(.*?)</h4>`)
	reH5        = regexp.MustCompile(`(?i)<h5[^>]*>(.*?)</h5>`)
	reH6        = regexp.MustCompile(`(?i)<h6[^>]*>(.*?)</h6>`)
	reLink      = regexp.MustCompile(`(?i)<a[^>]+href="([^"]*)"[^>]*>(.*?)</a>`)
	reParagraph = regexp.MustCompile(`(?i)</?p[^>]*>`)
	reBr        = regexp.MustCompile(`(?i)<br\s*/?>`)
	reLi        = regexp.MustCompile(`(?i)<li[^>]*>(.*?)</li>`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
	reMultiNL   = regexp.MustCompile(`\n{3,}`)
	reEntity    = regexp.MustCompile(`&(amp|lt|gt|quot|apos|nbsp);`)
)

// htmlToMarkdown performs a simple regex-based conversion of HTML to markdown.
// It strips scripts/styles, converts headings/links/lists, and removes
// remaining tags.
func htmlToMarkdown(html string) string {
	s := html

	// Remove script and style blocks.
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")

	// Convert headings.
	s = reH1.ReplaceAllString(s, "\n# $1\n")
	s = reH2.ReplaceAllString(s, "\n## $1\n")
	s = reH3.ReplaceAllString(s, "\n### $1\n")
	s = reH4.ReplaceAllString(s, "\n#### $1\n")
	s = reH5.ReplaceAllString(s, "\n##### $1\n")
	s = reH6.ReplaceAllString(s, "\n###### $1\n")

	// Convert links: <a href="url">text</a> -> [text](url)
	s = reLink.ReplaceAllString(s, "[$2]($1)")

	// Convert list items.
	s = reLi.ReplaceAllString(s, "\n- $1")

	// Convert paragraphs and line breaks.
	s = reParagraph.ReplaceAllString(s, "\n")
	s = reBr.ReplaceAllString(s, "\n")

	// Strip remaining HTML tags.
	s = reTag.ReplaceAllString(s, "")

	// Decode common HTML entities.
	s = reEntity.ReplaceAllStringFunc(s, decodeEntity)

	// Collapse excessive newlines.
	s = reMultiNL.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}

// decodeEntity decodes a matched HTML entity reference.
func decodeEntity(entity string) string {
	switch entity {
	case "&amp;":
		return "&"
	case "&lt;":
		return "<"
	case "&gt;":
		return ">"
	case "&quot;":
		return "\""
	case "&apos;":
		return "'"
	case "&nbsp;":
		return " "
	default:
		return entity
	}
}
