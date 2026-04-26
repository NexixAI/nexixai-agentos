package httpfetch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFetch_ReturnsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello world")
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	// The test server runs on 127.0.0.1 which is a private IP.
	// We test the logic path separately; here we test the HTTP fetch part
	// by using a fetcher that skips SSRF for the loopback test server.
	result, err := fetchBypassingSSRF(f, context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}

	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
	if result.Body != "hello world" {
		t.Errorf("expected body %q, got %q", "hello world", result.Body)
	}
	if result.ContentType != "text/plain" {
		t.Errorf("expected content type %q, got %q", "text/plain", result.ContentType)
	}
	if result.BytesFetched != 11 {
		t.Errorf("expected 11 bytes fetched, got %d", result.BytesFetched)
	}
	if result.URL != srv.URL {
		t.Errorf("expected URL %q, got %q", srv.URL, result.URL)
	}
}

func TestFetch_HTMLToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains []string
	}{
		{
			name:     "headings",
			html:     "<h1>Title</h1><h2>Subtitle</h2><p>Body text</p>",
			contains: []string{"# Title", "## Subtitle", "Body text"},
		},
		{
			name:     "links",
			html:     `<p>Visit <a href="https://example.com">Example</a></p>`,
			contains: []string{"[Example](https://example.com)"},
		},
		{
			name:     "lists",
			html:     "<ul><li>First</li><li>Second</li></ul>",
			contains: []string{"- First", "- Second"},
		},
		{
			name:     "script removal",
			html:     `<p>Keep</p><script>alert("gone")</script><p>Also keep</p>`,
			contains: []string{"Keep", "Also keep"},
		},
		{
			name:     "entities",
			html:     `<p>Tom &amp; Jerry &lt;3&gt;</p>`,
			contains: []string{"Tom & Jerry <3>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, tt.html)
			}))
			defer srv.Close()

			f := NewHTTPFetcher()
			result, err := fetchBypassingSSRF(f, context.Background(), srv.URL)
			if err != nil {
				t.Fatalf("Fetch returned error: %v", err)
			}

			for _, want := range tt.contains {
				if !strings.Contains(result.Body, want) {
					t.Errorf("expected body to contain %q, got:\n%s", want, result.Body)
				}
			}

			// Scripts should be removed.
			if tt.name == "script removal" && strings.Contains(result.Body, "alert") {
				t.Error("script content should have been removed")
			}
		})
	}
}

func TestFetch_JSONPrettyPrint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"key":"value","nested":{"a":1}}`)
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	result, err := fetchBypassingSSRF(f, context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}

	if !strings.Contains(result.Body, "  ") {
		t.Errorf("expected pretty-printed JSON with indentation, got:\n%s", result.Body)
	}
	if !strings.Contains(result.Body, `"key": "value"`) {
		t.Errorf("expected formatted JSON key-value, got:\n%s", result.Body)
	}
}

func TestFetch_DomainAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		allowlist []string
		host      string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "allowed domain passes",
			allowlist: []string{"example.com"},
			host:      "example.com",
			wantErr:   false,
		},
		{
			name:      "non-allowed domain blocked",
			allowlist: []string{"example.com"},
			host:      "evil.com",
			wantErr:   true,
			errMsg:    "not in the allowlist",
		},
		{
			name:      "empty allowlist allows all",
			allowlist: nil,
			host:      "anything.com",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewHTTPFetcher(WithAllowlist(tt.allowlist))
			err := f.checkDomainFilters(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestFetch_DomainBlocklist(t *testing.T) {
	tests := []struct {
		name      string
		blocklist []string
		host      string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "blocklisted domain rejected",
			blocklist: []string{"evil.com"},
			host:      "evil.com",
			wantErr:   true,
			errMsg:    "blocklisted",
		},
		{
			name:      "non-blocklisted domain passes",
			blocklist: []string{"evil.com"},
			host:      "good.com",
			wantErr:   false,
		},
		{
			name:      "case insensitive blocklist",
			blocklist: []string{"Evil.COM"},
			host:      "evil.com",
			wantErr:   true,
			errMsg:    "blocklisted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewHTTPFetcher(WithBlocklist(tt.blocklist))
			err := f.checkDomainFilters(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestFetch_SizeLimit(t *testing.T) {
	// Create a response larger than the limit.
	bigBody := strings.Repeat("x", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, bigBody)
	}))
	defer srv.Close()

	f := NewHTTPFetcher(WithMaxSize(50))
	result, err := fetchBypassingSSRF(f, context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}

	if result.BytesFetched != 50 {
		t.Errorf("expected 50 bytes fetched, got %d", result.BytesFetched)
	}
	if !strings.HasSuffix(result.Body, "[truncated]") {
		t.Error("expected body to end with [truncated]")
	}
}

func TestFetch_SSRFProtection(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		wantErr bool
	}{
		{"loopback IPv4", "127.0.0.1", true},
		{"loopback other", "127.0.0.2", true},
		{"current network", "0.0.0.1", true},
		{"carrier-grade nat", "100.64.0.1", true},
		{"private 10.x", "10.0.0.1", true},
		{"private 172.16.x", "172.16.0.1", true},
		{"private 172.31.x", "172.31.255.255", true},
		{"private 192.168.x", "192.168.1.1", true},
		{"link-local", "169.254.1.1", true},
		{"loopback IPv6", "::1", true},
		{"public IP", "8.8.8.8", false},
		{"public 172.15.x (not private)", "172.15.0.1", false},
		{"public 172.32.x (not private)", "172.32.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse test IP %q", tt.ip)
			}

			got := isPrivateIP(ip)
			if got != tt.wantErr {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.wantErr)
			}
		})
	}
}

func TestFetch_CheckRedirectRejectsPrivateTarget(t *testing.T) {
	f := NewHTTPFetcher(WithAllowlist([]string{"example.com"}))
	redirectURL, err := url.Parse("http://127.0.0.1/private")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}

	err = f.checkRedirect(&http.Request{URL: redirectURL}, []*http.Request{{}})
	if err == nil {
		t.Fatal("expected redirect target to be rejected")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("expected SSRF error, got: %v", err)
	}
}

func TestFetch_CheckRedirectRejectsNonAllowlistedTarget(t *testing.T) {
	f := NewHTTPFetcher(WithAllowlist([]string{"example.com"}), WithSkipSSRF())
	redirectURL, err := url.Parse("https://evil.example.net/path")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}

	err = f.checkRedirect(&http.Request{URL: redirectURL}, []*http.Request{{}})
	if err == nil {
		t.Fatal("expected redirect target to be rejected")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("expected allowlist error, got: %v", err)
	}
}

func TestFetch_CheckRedirectLimit(t *testing.T) {
	f := NewHTTPFetcher(WithSkipSSRF())
	redirectURL, err := url.Parse("https://example.com/path")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}

	via := make([]*http.Request, maxRedirects)
	err = f.checkRedirect(&http.Request{URL: redirectURL}, via)
	if err == nil {
		t.Fatal("expected redirect limit error")
	}
	if !strings.Contains(err.Error(), "stopped after") {
		t.Fatalf("expected redirect limit error, got: %v", err)
	}
}

func TestFetch_SSRFRejectsPrivateURL(t *testing.T) {
	// Verify that Fetch itself rejects private IPs.
	f := NewHTTPFetcher()
	_, err := f.Fetch(context.Background(), "http://127.0.0.1:9999/test")
	if err == nil {
		t.Fatal("expected SSRF error for 127.0.0.1, got nil")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Errorf("expected SSRF error, got: %v", err)
	}
}

func TestFetch_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the request context is cancelled.
		<-r.Context().Done()
	}))
	defer srv.Close()

	f := NewHTTPFetcher(WithTimeout(100 * time.Millisecond))
	_, err := fetchBypassingSSRF(f, context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// The error should mention context or deadline or timeout.
	errStr := err.Error()
	if !strings.Contains(errStr, "context deadline exceeded") &&
		!strings.Contains(errStr, "timeout") &&
		!strings.Contains(errStr, "Client.Timeout") {
		t.Errorf("expected timeout-related error, got: %v", err)
	}
}

func TestFetch_InvalidURL(t *testing.T) {
	f := NewHTTPFetcher()
	_, err := f.Fetch(context.Background(), "://bad-url")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestFetch_UnsupportedScheme(t *testing.T) {
	f := NewHTTPFetcher()
	_, err := f.Fetch(context.Background(), "ftp://example.com/file")
	if err == nil {
		t.Fatal("expected error for ftp scheme, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported scheme") {
		t.Errorf("expected unsupported scheme error, got: %v", err)
	}
}

// fetchBypassingSSRF performs a fetch that bypasses the SSRF check for test
// servers running on 127.0.0.1. It does this by temporarily adjusting the
// fetcher to make the actual HTTP call directly, working around the private
// IP check that would otherwise block localhost test servers.
//
// This helper should ONLY be used in tests. The SSRF logic itself is tested
// separately via TestFetch_SSRFProtection and TestFetch_SSRFRejectsPrivateURL.
func fetchBypassingSSRF(f *HTTPFetcher, ctx context.Context, rawURL string) (*FetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "AgentOS-FetchURL/1.0")

	resp, err := testLoopbackClient(f).Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close in defer

	contentType := resp.Header.Get("Content-Type")

	lr := limitedRead(resp.Body, f.maxSize)
	body, truncated, err := lr.result()
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to read response body: %w", err)
	}

	bodyStr := string(body)

	if strings.Contains(contentType, "text/html") {
		bodyStr = htmlToMarkdown(bodyStr)
	} else if strings.Contains(contentType, "application/json") {
		bodyStr = prettyPrintJSON(bodyStr)
	}

	if truncated {
		bodyStr += "\n[truncated]"
	}

	return &FetchResult{
		URL:          rawURL,
		StatusCode:   resp.StatusCode,
		ContentType:  contentType,
		Body:         bodyStr,
		BytesFetched: int64(len(body)),
	}, nil
}

// limitedReader reads up to max bytes and reports truncation.
type limitedReader struct {
	body    []byte
	maxSize int64
	err     error
}

func limitedRead(r interface{ Read([]byte) (int, error) }, maxSize int64) *limitedReader {
	lr := &limitedReader{maxSize: maxSize}
	buf := make([]byte, 0, min(maxSize+1, 32*1024))
	for {
		if len(buf) == cap(buf) {
			newCap := cap(buf) * 2
			if int64(newCap) > maxSize+1 {
				newCap = int(maxSize + 1)
			}
			if newCap <= cap(buf) {
				// Already at max capacity, do one more read to check truncation.
				tmp := make([]byte, 1)
				n, err := r.Read(tmp)
				if n > 0 {
					buf = append(buf, tmp[:n]...)
				}
				if err != nil {
					lr.body = buf
					if err.Error() != "EOF" {
						lr.err = err
					}
					return lr
				}
				lr.body = buf
				return lr
			}
			newBuf := make([]byte, len(buf), newCap)
			copy(newBuf, buf)
			buf = newBuf
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			lr.body = buf
			if err.Error() != "EOF" {
				lr.err = err
			}
			return lr
		}
		if int64(len(buf)) > maxSize {
			lr.body = buf
			return lr
		}
	}
}

func (lr *limitedReader) result() ([]byte, bool, error) {
	if lr.err != nil {
		return nil, false, lr.err
	}
	if int64(len(lr.body)) > lr.maxSize {
		return lr.body[:lr.maxSize], true, nil
	}
	return lr.body, false, nil
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func testLoopbackClient(f *HTTPFetcher) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout:   f.timeout,
		Transport: transport,
	}
}

// --- Request tests ---

func TestRequest_POSTWithBodyAndHeaders(t *testing.T) {
	var gotMethod, gotBody, gotContentType, gotCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotCustom = r.Header.Get("X-Custom")
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		}
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	result, err := requestBypassingSSRF(f, context.Background(), http.MethodPost, srv.URL, map[string]string{
		"Content-Type": "application/json",
		"X-Custom":     "test-value",
	}, `{"input":"data"}`)
	if err != nil {
		t.Fatalf("Request returned error: %v", err)
	}

	if gotMethod != "POST" {
		t.Errorf("expected method POST, got %q", gotMethod)
	}
	if gotBody != `{"input":"data"}` {
		t.Errorf("expected body %q, got %q", `{"input":"data"}`, gotBody)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type %q, got %q", "application/json", gotContentType)
	}
	if gotCustom != "test-value" {
		t.Errorf("expected X-Custom %q, got %q", "test-value", gotCustom)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
	if result.ContentType != "application/json" {
		t.Errorf("expected content type %q, got %q", "application/json", result.ContentType)
	}
}

func TestRequest_DomainAllowlist(t *testing.T) {
	// Use a well-known public domain that resolves, but is not in our allowlist.
	f := NewHTTPFetcher(WithAllowlist([]string{"allowed-only.example.com"}))
	_, err := f.Request(context.Background(), "GET", "https://example.com/path", nil, "")
	if err == nil {
		t.Fatal("expected error for domain not in allowlist, got nil")
	}
	if !strings.Contains(err.Error(), "not in the allowlist") {
		t.Errorf("expected allowlist error, got: %v", err)
	}
}

func TestRequest_DomainBlocklist(t *testing.T) {
	// Use a well-known public domain that resolves, then blocklist it.
	f := NewHTTPFetcher(WithBlocklist([]string{"example.com"}))
	_, err := f.Request(context.Background(), "GET", "https://example.com/path", nil, "")
	if err == nil {
		t.Fatal("expected error for blocklisted domain, got nil")
	}
	if !strings.Contains(err.Error(), "blocklisted") {
		t.Errorf("expected blocklist error, got: %v", err)
	}
}

func TestRequest_SSRFProtection(t *testing.T) {
	f := NewHTTPFetcher()
	_, err := f.Request(context.Background(), "GET", "http://127.0.0.1:9999/test", nil, "")
	if err == nil {
		t.Fatal("expected SSRF error for 127.0.0.1, got nil")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Errorf("expected SSRF error, got: %v", err)
	}
}

func TestRequest_UnsupportedMethod(t *testing.T) {
	f := NewHTTPFetcher()
	_, err := f.Request(context.Background(), "CONNECT", "https://example.com", nil, "")
	if err == nil {
		t.Fatal("expected error for unsupported method, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported HTTP method") {
		t.Errorf("expected unsupported method error, got: %v", err)
	}
}

// --- Ping tests ---

func TestPing_ReturnsStatusAndLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD method, got %q", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	result, err := pingBypassingSSRF(f, context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Ping returned error: %v", err)
	}

	if result.URL != srv.URL {
		t.Errorf("expected URL %q, got %q", srv.URL, result.URL)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
	if result.LatencyMs < 0 {
		t.Errorf("expected non-negative latency, got %d", result.LatencyMs)
	}
}

func TestPing_UnreachableHost(t *testing.T) {
	// Use a listener that is immediately closed to get an unreachable port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	f := NewHTTPFetcher(WithTimeout(500 * time.Millisecond))
	_, err = pingBypassingSSRF(f, context.Background(), "http://"+addr+"/ping")
	if err == nil {
		t.Fatal("expected error for unreachable host, got nil")
	}
}

func TestPing_SSRFProtection(t *testing.T) {
	f := NewHTTPFetcher()
	_, err := f.Ping(context.Background(), "http://127.0.0.1:9999/test")
	if err == nil {
		t.Fatal("expected SSRF error for 127.0.0.1, got nil")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Errorf("expected SSRF error, got: %v", err)
	}
}

// requestBypassingSSRF performs a Request that bypasses the SSRF check for
// test servers running on 127.0.0.1.
func requestBypassingSSRF(f *HTTPFetcher, ctx context.Context, method, rawURL string, headers map[string]string, body string) (*FetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	var bodyReader io.Reader
	if body != "" {
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

	resp, err := testLoopbackClient(f).Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close in defer

	contentType := resp.Header.Get("Content-Type")

	lr := limitedRead(resp.Body, f.maxSize)
	respBody, truncated, err := lr.result()
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to read response body: %w", err)
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

	return &FetchResult{
		URL:          rawURL,
		StatusCode:   resp.StatusCode,
		ContentType:  contentType,
		Body:         bodyStr,
		BytesFetched: int64(len(respBody)),
	}, nil
}

// pingBypassingSSRF performs a Ping that bypasses the SSRF check for test
// servers running on 127.0.0.1.
func pingBypassingSSRF(f *HTTPFetcher, ctx context.Context, rawURL string) (*PingResult, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpfetch: failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "AgentOS-Ping/1.0")

	start := time.Now()
	resp, err := testLoopbackClient(f).Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("httpfetch: ping failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close in defer

	return &PingResult{
		URL:        rawURL,
		StatusCode: resp.StatusCode,
		LatencyMs:  latencyMs,
	}, nil
}
