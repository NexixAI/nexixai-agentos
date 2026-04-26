package modelpolicy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

// discardLogger returns an slog.Logger that drops everything — keeps test
// output clean while still exercising the logging code paths.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestQueryFirstModelID_SingleModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"Qwen/Qwen3.6-35B-A3B","object":"model"}]}`))
	}))
	defer srv.Close()

	got, err := QueryFirstModelID(context.Background(), srv.URL+"/v1", srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Qwen/Qwen3.6-35B-A3B" {
		t.Errorf("got %q, want Qwen/Qwen3.6-35B-A3B", got)
	}
}

func TestQueryFirstModelID_MultiModel_FirstWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"first"},{"id":"second"},{"id":"third"}]}`))
	}))
	defer srv.Close()

	got, err := QueryFirstModelID(context.Background(), srv.URL+"/v1", srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "first" {
		t.Errorf("got %q, want first", got)
	}
}

func TestQueryFirstModelID_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srv.Close()

	_, err := QueryFirstModelID(context.Background(), srv.URL+"/v1", srv.Client())
	if !errors.Is(err, ErrEmptyModelList) {
		t.Errorf("got %v, want ErrEmptyModelList", err)
	}
}

func TestQueryFirstModelID_EmptyFirstID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"  "}]}`))
	}))
	defer srv.Close()

	_, err := QueryFirstModelID(context.Background(), srv.URL+"/v1", srv.Client())
	if !errors.Is(err, ErrEmptyModelList) {
		t.Errorf("got %v, want ErrEmptyModelList for whitespace-only id", err)
	}
}

func TestQueryFirstModelID_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := QueryFirstModelID(context.Background(), srv.URL+"/v1", srv.Client())
	var badStatus *ErrBadStatus
	if !errors.As(err, &badStatus) {
		t.Fatalf("got %v, want *ErrBadStatus", err)
	}
	if badStatus.Code != 500 {
		t.Errorf("got code %d, want 500", badStatus.Code)
	}
}

func TestQueryFirstModelID_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := QueryFirstModelID(context.Background(), srv.URL+"/v1", srv.Client())
	var badStatus *ErrBadStatus
	if !errors.As(err, &badStatus) || badStatus.Code != 404 {
		t.Errorf("got %v, want *ErrBadStatus{404}", err)
	}
}

func TestQueryFirstModelID_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":`)) // truncated
	}))
	defer srv.Close()

	_, err := QueryFirstModelID(context.Background(), srv.URL+"/v1", srv.Client())
	if err == nil {
		t.Fatal("expected decode error")
	}
	if errors.Is(err, ErrEmptyModelList) {
		t.Errorf("malformed JSON was misclassified as empty list")
	}
}

func TestQueryFirstModelID_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
			_, _ = w.Write([]byte(`{"data":[{"id":"x"}]}`))
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	_, err := QueryFirstModelID(ctx, srv.URL+"/v1", srv.Client())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestQueryFirstModelID_TrailingSlashOnBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q — base URL trimming broken", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"ok"}]}`))
	}))
	defer srv.Close()

	_, err := QueryFirstModelID(context.Background(), srv.URL+"/v1/", srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- ResolveDefaultModel tests ---------------------------------------------

func TestResolveDefaultModel_EnvWins(t *testing.T) {
	// Backend will NOT be hit when env is set; verify by panicking if it is.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("backend should not be queried when env is set")
	}))
	defer srv.Close()

	cfg := config.ModelConfig{
		Provider:     "openai",
		BaseURL:      srv.URL + "/v1",
		DefaultModel: "operator-pinned-model",
	}
	got, err := ResolveDefaultModel(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "operator-pinned-model" {
		t.Errorf("got %q, want operator-pinned-model", got)
	}
}

func TestResolveDefaultModel_BackendWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"Qwen/Qwen3.6-35B-A3B"}]}`))
	}))
	defer srv.Close()

	cfg := config.ModelConfig{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
	}
	got, err := ResolveDefaultModel(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Qwen/Qwen3.6-35B-A3B" {
		t.Errorf("got %q, want Qwen/Qwen3.6-35B-A3B", got)
	}
}

func TestResolveDefaultModel_NonOpenAIProvider_EmptyEnv(t *testing.T) {
	cfg := config.ModelConfig{
		Provider: "anthropic",
		BaseURL:  "",
	}
	got, err := ResolveDefaultModel(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string for non-openai provider with no env", got)
	}
}

func TestResolveDefaultModel_OpenAIMissingBaseURL(t *testing.T) {
	cfg := config.ModelConfig{
		Provider: "openai",
		BaseURL:  "",
	}
	_, err := ResolveDefaultModel(context.Background(), cfg, discardLogger())
	if err == nil {
		t.Fatal("expected error for openai provider with no base URL")
	}
}

func TestResolveDefaultModel_EmptyList_NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	cfg := config.ModelConfig{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
	}
	_, err := ResolveDefaultModel(context.Background(), cfg, discardLogger())
	if err == nil {
		t.Fatal("expected error for empty model list")
	}
	if !errors.Is(err, ErrEmptyModelList) {
		t.Errorf("error should wrap ErrEmptyModelList, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("empty-list should not retry; got %d calls, want 1", got)
	}
}

func TestResolveDefaultModel_RetryThenSucceed(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"eventually-up"}]}`))
	}))
	defer srv.Close()

	short := []time.Duration{
		10 * time.Millisecond,
		10 * time.Millisecond,
		10 * time.Millisecond,
		10 * time.Millisecond,
	}
	got, err := resolveWithBackoffs(context.Background(), srv.URL+"/v1", srv.Client(), short, time.Second, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "eventually-up" {
		t.Errorf("got %q, want eventually-up", got)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("expected 3 attempts, got %d", n)
	}
}

func TestResolveDefaultModel_ExhaustedRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	short := []time.Duration{
		5 * time.Millisecond,
		5 * time.Millisecond,
		5 * time.Millisecond,
	}
	_, err := resolveWithBackoffs(context.Background(), srv.URL+"/v1", srv.Client(), short, 100*time.Millisecond, discardLogger())
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if n := calls.Load(); n < 2 {
		t.Errorf("expected multiple attempts, got %d", n)
	}
	// Error must NOT expose raw base URL path/query — only scheme://host.
	if containsRawURL(err.Error(), srv.URL+"/v1") {
		t.Errorf("error leaked full base URL including path: %v", err)
	}
}

func TestResolveDefaultModel_ContextCanceledDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay so the first attempt runs, then we cancel
	// mid-backoff.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	long := []time.Duration{500 * time.Millisecond, 500 * time.Millisecond}
	_, err := resolveWithBackoffs(ctx, srv.URL+"/v1", srv.Client(), long, 5*time.Second, discardLogger())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func containsRawURL(s, url string) bool {
	// Ensure the path portion "/v1" isn't present verbatim in the error.
	// baseURLHost should have stripped it.
	return len(url) > 0 && indexOf(s, url) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
