package metrics

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registry = prometheus.NewRegistry()

	httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_http_requests_total",
			Help: "Total HTTP requests served by AgentOS services.",
		},
		[]string{"service", "method", "code"},
	)

	httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "method", "code"},
	)

	quotaDenied = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_quota_denied_total",
			Help: "Total quota denials.",
		},
		[]string{"service", "kind"},
	)

	fedForwardFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_federation_forward_failures_total",
			Help: "Federation forward failures (remote create run failures).",
		},
		[]string{"service", "reason"},
	)

	// Track 1 (v1.035): Enhanced observability metrics.

	modelCallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_model_call_duration_seconds",
			Help:    "Model provider call duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"model", "provider", "status"}, // status: ok, error
	)

	modelTokensUsed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_model_tokens_total",
			Help: "Total tokens consumed by model calls.",
		},
		[]string{"tenant", "model", "direction"}, // direction: prompt, completion
	)

	dailyTokenBudgetUsed = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agentos_daily_token_budget_used",
			Help: "Cumulative model tokens used per tenant for the current UTC day.",
		},
		[]string{"tenant"},
	)

	toolExecDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_tool_execution_duration_seconds",
			Help:    "Tool execution duration in seconds.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 5, 10, 30},
		},
		[]string{"tool", "status"}, // status: ok, error
	)

	runLifecycle = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_run_lifecycle_total",
			Help: "Run state transitions.",
		},
		[]string{"tenant", "transition"}, // transition: queued, started, completed, failed, canceled
	)

	runsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agentos_runs_active",
			Help: "Number of currently active (running) runs.",
		},
		[]string{"tenant"},
	)

	runSteps = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_run_steps_total",
			Help:    "Number of steps per completed run.",
			Buckets: []float64{1, 2, 3, 5, 10, 15, 20},
		},
		[]string{"tenant"},
	)

	memoryOps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_memory_operations_total",
			Help: "Memory store operations.",
		},
		[]string{"operation", "status"}, // operation: append, get_recent, clear; status: ok, error
	)

	kvOps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_kv_operations_total",
			Help: "KV store operations.",
		},
		[]string{"operation", "status"}, // operation: get, set, delete, list; status: ok, error
	)

	intentClassifications = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_intent_classifications_total",
			Help: "Total intent classifications by intent label.",
		},
		[]string{"intent", "fallback"},
	)

	intentClassifyDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_intent_classify_duration_seconds",
			Help:    "Intent classifier latency in seconds.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"intent"},
	)

	intentKBSearchDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_intent_kb_search_duration_seconds",
			Help:    "KB search latency during intent composition.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.0, 5.0},
		},
		[]string{"intent"},
	)

	intentKBResults = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_intent_kb_results_count",
			Help:    "Number of KB results returned per intent composition.",
			Buckets: []float64{0, 1, 3, 5, 10, 15, 20},
		},
		[]string{"intent"},
	)

	codegenTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_codegen_generations_total",
			Help: "Total code generation requests.",
		},
		[]string{"repo", "status"},
	)

	// v11.0 Issue #4: chat content-type counters for multimodal observability.
	chatMessagesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_chat_messages_total",
			Help: "Total chat_completion calls by content type (text | multimodal).",
		},
		[]string{"tenant_id", "content_type"},
	)

	chatImageParts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_chat_image_parts_total",
			Help: "Total image_url content parts submitted to chat_completion.",
		},
		[]string{"tenant_id"},
	)

	codegenDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_codegen_generation_duration_seconds",
			Help:    "Code generation latency in seconds.",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
		},
		[]string{"repo"},
	)

	codegenTokens = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentos_codegen_generation_tokens",
			Help:    "Tokens used per code generation.",
			Buckets: []float64{100, 500, 1000, 2000, 4000, 8000, 16000},
		},
		[]string{"repo"},
	)
)

func init() {
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpRequests,
		httpDuration,
		quotaDenied,
		fedForwardFailures,
		modelCallDuration,
		modelTokensUsed,
		dailyTokenBudgetUsed,
		toolExecDuration,
		runLifecycle,
		runsActive,
		runSteps,
		memoryOps,
		kvOps,
		intentClassifications,
		intentClassifyDuration,
		intentKBSearchDuration,
		intentKBResults,
		codegenTotal,
		codegenDuration,
		codegenTokens,
		chatMessagesTotal,
		chatImageParts,
	)
}

// ObserveChatMessage records a chat_completion call. contentType is either
// "text" or "multimodal"; imageParts is the number of image_url parts in the
// message (0 for text-only calls). See v11.0 Issue #4.
func ObserveChatMessage(tenantID, contentType string, imageParts int) {
	chatMessagesTotal.WithLabelValues(truncateLabel(tenantID), contentType).Inc()
	if imageParts > 0 {
		chatImageParts.WithLabelValues(truncateLabel(tenantID)).Add(float64(imageParts))
	}
}

var (
	nowUTC = func() time.Time {
		return time.Now().UTC()
	}

	dailyTokenBudgetState = &dailyTokenBudgetTracker{
		usageByTenant: make(map[string]float64),
	}
)

type dailyTokenBudgetTracker struct {
	mu            sync.Mutex
	currentDay    string
	usageByTenant map[string]float64
}

func (d *dailyTokenBudgetTracker) syncDay(now time.Time) {
	day := now.UTC().Format("2006-01-02")
	if d.currentDay == day {
		return
	}
	d.currentDay = day
	clear(d.usageByTenant)
	dailyTokenBudgetUsed.Reset()
}

func (d *dailyTokenBudgetTracker) add(tenant string, tokens float64, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.syncDay(now)
	d.usageByTenant[tenant] += tokens
	dailyTokenBudgetUsed.WithLabelValues(tenant).Set(d.usageByTenant[tenant])
}

func (d *dailyTokenBudgetTracker) syncForScrape(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.syncDay(now)
}

// InitMetrics pre-initializes Vec metrics with zero-value label combinations so
// they appear in /metrics output immediately, not only after first use. Without
// this, dashboards show NO DATA until the first model call / codegen / etc.
func InitMetrics() {
	// Model call metrics — initialize with placeholder labels
	modelCallDuration.WithLabelValues("_init", "_init", "ok")
	modelTokensUsed.WithLabelValues("_init", "_init", "prompt")
	modelTokensUsed.WithLabelValues("_init", "_init", "completion")

	// Tool execution
	toolExecDuration.WithLabelValues("_init", "ok")

	// Run lifecycle
	for _, t := range []string{"queued", "started", "completed", "failed", "canceled"} {
		runLifecycle.WithLabelValues("_init", t)
	}
	runsActive.WithLabelValues("_init")
	runSteps.WithLabelValues("_init")

	// Storage operations
	for _, op := range []string{"append", "get_recent", "clear"} {
		memoryOps.WithLabelValues(op, "ok")
	}
	for _, op := range []string{"get", "set", "delete", "list"} {
		kvOps.WithLabelValues(op, "ok")
	}

	// Intent classification
	intentClassifications.WithLabelValues("_init", "false")
	intentClassifyDuration.WithLabelValues("_init")
	intentKBSearchDuration.WithLabelValues("_init")
	intentKBResults.WithLabelValues("_init")

	// Code generation
	codegenTotal.WithLabelValues("_init", "ok")
	codegenDuration.WithLabelValues("_init")
	codegenTokens.WithLabelValues("_init")

	// Quota and federation
	quotaDenied.WithLabelValues("_init", "qps")
	fedForwardFailures.WithLabelValues("_init", "_init")
}

// Handler returns a Prometheus scrape handler for the AgentOS registry.
func Handler() http.Handler {
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dailyTokenBudgetState.syncForScrape(nowUTC())
		handler.ServeHTTP(w, r)
	})
}

type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Instrument wraps an HTTP handler and records basic request counters and latency.
func Instrument(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusCapturingWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(sw, r)
		code := strconv.Itoa(sw.status)
		httpRequests.WithLabelValues(service, r.Method, code).Inc()
		httpDuration.WithLabelValues(service, r.Method, code).Observe(time.Since(start).Seconds())
	})
}

func IncQuotaDenied(service, kind string) {
	quotaDenied.WithLabelValues(service, kind).Inc()
}

func IncFederationForwardFailure(service, reason string) {
	fedForwardFailures.WithLabelValues(service, reason).Inc()
}

// ObserveModelCall records model call duration and status.
func ObserveModelCall(model, provider, status string, durationSec float64) {
	modelCallDuration.WithLabelValues(model, provider, status).Observe(durationSec)
}

// AddModelTokens records token usage for a model call.
func AddModelTokens(tenant, model string, prompt, completion int) {
	t, m := truncateLabel(tenant), truncateLabel(model)
	modelTokensUsed.WithLabelValues(t, m, "prompt").Add(float64(prompt))
	modelTokensUsed.WithLabelValues(t, m, "completion").Add(float64(completion))
	dailyTokenBudgetState.add(t, float64(prompt+completion), nowUTC())
}

// ObserveToolExec records tool execution duration and status.
func ObserveToolExec(tool, status string, durationSec float64) {
	toolExecDuration.WithLabelValues(tool, status).Observe(durationSec)
}

// IncRunLifecycle records a run state transition.
func IncRunLifecycle(tenant, transition string) {
	runLifecycle.WithLabelValues(truncateLabel(tenant), transition).Inc()
}

// IncRunsActive increments the active runs gauge.
func IncRunsActive(tenant string) {
	runsActive.WithLabelValues(truncateLabel(tenant)).Inc()
}

// DecRunsActive decrements the active runs gauge.
func DecRunsActive(tenant string) {
	runsActive.WithLabelValues(truncateLabel(tenant)).Dec()
}

// ObserveRunSteps records the number of steps for a completed run.
func ObserveRunSteps(tenant string, steps int) {
	runSteps.WithLabelValues(truncateLabel(tenant)).Observe(float64(steps))
}

// IncMemoryOp records a memory store operation.
func IncMemoryOp(operation, status string) {
	memoryOps.WithLabelValues(operation, status).Inc()
}

// IncKVOp records a KV store operation.
func IncKVOp(operation, status string) {
	kvOps.WithLabelValues(operation, status).Inc()
}

// ObserveIntentClassification records an intent classification event.
func ObserveIntentClassification(intent string, fallback bool) {
	fb := "false"
	if fallback {
		fb = "true"
	}
	intentClassifications.WithLabelValues(truncateLabel(intent), fb).Inc()
}

// ObserveIntentClassifyDuration records classifier call latency.
func ObserveIntentClassifyDuration(intent string, durationSec float64) {
	intentClassifyDuration.WithLabelValues(truncateLabel(intent)).Observe(durationSec)
}

// IncCodegenTotal records a code generation event.
func IncCodegenTotal(repo, status string) {
	codegenTotal.WithLabelValues(truncateLabel(repo), status).Inc()
}

// ObserveCodegenDuration records code generation latency.
func ObserveCodegenDuration(repo string, durationSec float64) {
	codegenDuration.WithLabelValues(truncateLabel(repo)).Observe(durationSec)
}

// ObserveCodegenTokens records tokens used for code generation.
func ObserveCodegenTokens(repo string, tokens float64) {
	codegenTokens.WithLabelValues(truncateLabel(repo)).Observe(tokens)
}

// ObserveIntentKBSearch records KB search latency and result count during intent composition.
func ObserveIntentKBSearch(intent string, durationSec float64, resultCount int) {
	intentKBSearchDuration.WithLabelValues(truncateLabel(intent)).Observe(durationSec)
	intentKBResults.WithLabelValues(truncateLabel(intent)).Observe(float64(resultCount))
}

// MetricsRequireAuth returns whether metrics endpoints should require auth.
// Defaults to true (restrictive). Set AGENTOS_METRICS_REQUIRE_AUTH=0 to
// explicitly disable in local/dev environments. Parse errors default to true.
func MetricsRequireAuth() bool {
	v := os.Getenv("AGENTOS_METRICS_REQUIRE_AUTH")
	if v == "" {
		return true
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return true // fail closed: parse error → require auth
	}
	return i != 0
}

// maxLabelLen caps metric label values to prevent cardinality explosion.
const maxLabelLen = 128

// truncateLabel caps a label value to maxLabelLen to bound cardinality.
func truncateLabel(s string) string {
	if len(s) > maxLabelLen {
		return s[:maxLabelLen]
	}
	return s
}
