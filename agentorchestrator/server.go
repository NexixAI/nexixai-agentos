package agentorchestrator

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/admin"
	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/compliance"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/lifecycle"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
	"github.com/NexixAI/nexixai-agentos/internal/logging"
	"github.com/NexixAI/nexixai-agentos/internal/auth"
	healthpkg "github.com/NexixAI/nexixai-agentos/internal/health"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/id"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/middleware"
	"github.com/NexixAI/nexixai-agentos/internal/quota"
	"github.com/NexixAI/nexixai-agentos/internal/storage"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
	"github.com/NexixAI/nexixai-agentos/internal/tenants"
	"github.com/NexixAI/nexixai-agentos/internal/tools"
	"github.com/NexixAI/nexixai-agentos/internal/types"
	"github.com/NexixAI/nexixai-agentos/internal/kbclient"
	"github.com/NexixAI/nexixai-agentos/internal/usage"
)

type Server struct {
	version      string
	defaultModel string
	modelBaseURL string // vLLM/provider base URL for proxying /v1/models
	routerCfg    RouterConfig
	intentCfg    *IntentConfig
	composer     *Composer

	runs        storage.FullRunStore
	agents      storage.AgentStore
	tenants     *tenants.Store
	limiter          *quota.Limiter
	tokenBudget      *quota.TokenBudget
	chatConcurrency  chan struct{} // semaphore for /v1/chat/completions concurrency
	audit       audit.Logger
	health      *healthpkg.Checker

	executor   *Executor
	memory     storage.MemoryStore
	kv         storage.KVStore
	storageCfg config.StorageConfig
	sharedDB   *sql.DB // shared postgres pool when backend=postgres; nil otherwise (v9.0 #12)

	// Agent version history store.
	agentVersions postgres.AgentVersionStore

	// RBAC + API key stores (nil = disabled, pass-through).
	memberStore middleware.RBACMemberStore
	apiKeyStore middleware.RBACAPIKeyStore

	// Optional handler dependencies (nil when not configured).
	usageHandler       http.Handler
	usageExportHandler http.Handler
	exportHandler      *compliance.ExportHandler
	deleteHandler      *compliance.DeleteHandler
	piiScanHandler     http.Handler
	backupHandler      http.HandlerFunc
	purgeHandler       http.HandlerFunc
	pgMemberStore      postgres.MemberStore
}

// ServerOption configures optional settings on Server construction.
// v11.3: introduced to carry the resolved default-model id from main.go
// without going through os.Setenv global state.
type ServerOption func(*serverOptions)

type serverOptions struct {
	defaultModel *string // nil = use env-derived; non-nil = explicit override
}

// WithServerDefaultModel overrides the default chat model id used when a caller
// omits "model" in the request body. When unset, the server uses
// AGENTOS_MODEL_DEFAULT from env via config.LoadModelConfig().
//
// Named distinctly from the executor-scoped WithDefaultModel to avoid a
// package-level name collision.
func WithServerDefaultModel(model string) ServerOption {
	return func(o *serverOptions) { o.defaultModel = &model }
}

func New(version string, opts ...ServerOption) (*Server, error) {
	return NewWithProvider(version, nil, opts...)
}

// NewWithProvider creates a Server with an optional custom model provider.
// If provider is nil, a stub provider is used.
func NewWithProvider(version string, provider ModelProvider, opts ...ServerOption) (*Server, error) {
	srv, provider, storageCfg, eventLog, err := newStorageLayer(version, provider, opts...)
	if err != nil {
		return nil, err
	}

	if err := srv.setupIntentRouter(); err != nil {
		return nil, err
	}

	srv.wireOptionalHandlers(storageCfg, eventLog)

	srv.restoreConcurrentCounts()

	// Seed demo agent for default tenant.
	defaultTenant := auth.DefaultTenant()
	if defaultTenant != "" {
		srv.seedDemoAgent(defaultTenant)
	}

	// Mark service as ready after initialization completes.
	srv.health.SetReady(true)

	return srv, nil
}

// newStorageLayer initializes all storage backends, the model provider, executor,
// and health checker. It returns the partially-constructed Server along with
// the resolved provider, storage config, and event log for downstream wiring.
//
// When the backend is "postgres", a single shared *sql.DB pool is opened and
// passed to every store via their FromDB constructors, avoiding independent
// connection pools per store (v9.0 #12 / M-9).
func newStorageLayer(version string, provider ModelProvider, opts ...ServerOption) (*Server, ModelProvider, config.StorageConfig, storage.EventLogStore, error) {
	serverOpts := serverOptions{}
	for _, o := range opts {
		o(&serverOpts)
	}
	storageCfg := config.LoadFromEnv()

	var (
		runStore    storage.FullRunStore
		agentStore  storage.AgentStore
		memoryStore storage.MemoryStore
		kvStore     storage.KVStore
		eventLog    storage.EventLogStore
		err         error
	)

	var sharedDB *sql.DB
	if strings.ToLower(storageCfg.Backend) == "postgres" {
		// Open a single shared connection pool for all postgres stores (v9.0 #12).
		var dbErr error
		sharedDB, dbErr = storage.OpenSharedDB(storageCfg.Postgres)
		if dbErr != nil {
			return nil, nil, config.StorageConfig{}, nil, fmt.Errorf("shared postgres pool: %w", dbErr)
		}
		runStore = postgres.NewRunStoreFromDB(sharedDB)
		agentStore = postgres.NewAgentStoreFromDB(sharedDB)
		memoryStore = postgres.NewMemoryStoreFromDB(sharedDB)
		kvStore = postgres.NewKVStoreFromDB(sharedDB, storageCfg.KVMaxValueSize, storageCfg.KVMaxKeysPerAgent)
		eventLog = postgres.NewEventLogStoreFromDB(sharedDB)
	} else {
		runStore, err = storage.NewRunStoreFromEnv()
		if err != nil {
			return nil, nil, config.StorageConfig{}, nil, err
		}
		agentStore, err = storage.NewAgentStoreFromEnv()
		if err != nil {
			return nil, nil, config.StorageConfig{}, nil, err
		}
		memoryStore, err = storage.NewMemoryStore(storageCfg)
		if err != nil {
			return nil, nil, config.StorageConfig{}, nil, err
		}
		kvStore, err = storage.NewKVStore(storageCfg)
		if err != nil {
			return nil, nil, config.StorageConfig{}, nil, err
		}
		eventLog, err = storage.NewEventLogStore(storageCfg, "data")
		if err != nil {
			return nil, nil, config.StorageConfig{}, nil, err
		}
	}

	tenantStore := tenants.NewStore()
	defaultTenant := auth.DefaultTenant()
	if defaultTenant != "" {
		tenantStore.EnsureDefault(defaultTenant)
	}
	hc := healthpkg.NewChecker(version)
	auditLogger := audit.NewFromEnv()

	// Create model provider from env config if none injected.
	if provider == nil {
		p, err := modelpolicy.NewProviderFromConfig()
		if err != nil {
			return nil, nil, config.StorageConfig{}, nil, fmt.Errorf("model provider initialization failed: %w", err)
		}
		provider = p
	}

	modelCfg := config.LoadModelConfig()
	// v11.3: option override beats env-derived default, so cmd/agentos/main.go
	// can pass the dynamically-resolved value without mutating os.Setenv.
	effectiveDefaultModel := modelCfg.DefaultModel
	if serverOpts.defaultModel != nil {
		effectiveDefaultModel = *serverOpts.defaultModel
	}
	execCfg := config.LoadExecConfig()
	toolRegistry := tools.NewRegistry()
	tokenBudget := quota.NewTokenBudgetFromEnv()
	executor := NewExecutor(execCfg, storageCfg, provider, toolRegistry, memoryStore, runStore, auditLogger,
		WithTokenBudget(tokenBudget), WithEventLog(eventLog), WithDefaultModel(effectiveDefaultModel))

	// Wire health checks.
	if modelCfg.BaseURL != "" {
		hc.Register(healthpkg.ProviderCheck("model_provider", false, modelCfg.BaseURL))
	}

	// GPU VRAM health — reports degraded when VRAM is high with zero active requests.
	promURL := os.Getenv("AGENTOS_PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://localhost:9090"
	}
	hc.Register(healthpkg.GPUVRAMCheck("gpu_vram", false, promURL))

	srv := &Server{
		version:       version,
		defaultModel:  effectiveDefaultModel,
		modelBaseURL:  modelCfg.BaseURL,
		routerCfg:     LoadRouterConfig(),
		runs:          runStore,
		agents:        agentStore,
		tenants:       tenantStore,
		limiter:         quota.NewFromEnv("AGENTOS_QUOTA_RUN_CREATE_QPS", "AGENTOS_QUOTA_CONCURRENT_RUNS", 10, 25),
		chatConcurrency: make(chan struct{}, quota.EnvInt("AGENTOS_CHAT_MAX_CONCURRENT", 16)),
		tokenBudget:     tokenBudget,
		audit:         auditLogger,
		health:        hc,
		executor:      executor,
		memory:        memoryStore,
		kv:            kvStore,
		storageCfg:    storageCfg,
		sharedDB:      sharedDB,
		agentVersions: postgres.NewMemoryAgentVersionStore(),
	}

	return srv, provider, storageCfg, eventLog, nil
}

// setupIntentRouter wires the intent-based router and KB-backed composer
// if the router config is enabled and the intent config file exists on disk.
func (s *Server) setupIntentRouter() error {
	if !s.routerCfg.Enabled {
		return nil
	}
	intentPath := envOr("AGENTOS_INTENT_CONFIG_PATH", "/etc/agentos/intent-router.yml")
	if _, statErr := os.Stat(intentPath); statErr != nil {
		slog.Info("intent router config not found, using binary classification", "path", intentPath)
		return nil
	}
	icfg, err := LoadIntentConfig(intentPath)
	if err != nil {
		return fmt.Errorf("intent router config: %w", err)
	}
	s.intentCfg = icfg

	// KB client for composition context injection
	kbURL := os.Getenv("AGENTOS_KB_URL")
	kbKey := os.Getenv("AGENTOS_KB_API_KEY")
	var kbClient *kbclient.KBClient
	if kbURL != "" {
		kbClient = kbclient.NewKBClient(kbURL, kbKey)
	}
	s.composer = NewComposer(icfg, kbClient)
	slog.Info("intent router loaded", "version", icfg.Version, "intents", len(icfg.Intents))
	return nil
}

// wireOptionalHandlers sets up handlers that depend on postgres or specific
// subsystems (usage, compliance, PII scanning, admin backup/purge).
func (s *Server) wireOptionalHandlers(storageCfg config.StorageConfig, eventLog storage.EventLogStore) {
	if strings.ToLower(storageCfg.Backend) == "postgres" {
		// Reuse the shared DB pool opened in newStorageLayer (v9.0 #12).
		// Fall back to opening a new pool only if sharedDB is nil (e.g. tests).
		db := s.sharedDB
		if db == nil {
			var dbErr error
			db, dbErr = postgres.OpenDB(storageCfg.Postgres)
			if dbErr != nil {
				slog.Warn("admin DB unavailable", "error", dbErr)
			}
		}
		if db != nil {
			usageQueryStore := postgres.NewUsageQueryStoreFromDB(db)
			s.usageHandler = usage.NewUsageHandler(usageQueryStore)
			s.usageExportHandler = usage.NewExportHandler(usageQueryStore)

			s.backupHandler = admin.BackupCheckHandler(db)
			s.purgeHandler = lifecycle.AdminPurgeHandler(db, lifecycle.LoadPurgeConfig())

			memberStore := postgres.NewMemberStore(db)
			s.pgMemberStore = memberStore
			s.memberStore = memberStore

			apiKeyStore := postgres.NewAPIKeyStore(db)
			s.apiKeyStore = apiKeyStore
		}
	}

	// PII scan works with any event log backend.
	if eventLog != nil {
		s.piiScanHandler = compliance.NewPIIScanHandler(eventLog)
	}

	// Compliance handlers work without postgres (in-memory job tracking).
	s.exportHandler = compliance.NewExportHandler(compliance.NewExportJobStore(100), nil)
	s.deleteHandler = compliance.NewDeleteHandler(nil, nil)
}

// restoreConcurrentCounts rebuilds the concurrent run counter from in-progress
// runs so the quota limiter survives server restarts.
func (s *Server) restoreConcurrentCounts() {
	allTenants := s.tenants.List()
	if len(allTenants) == 0 {
		return
	}
	concurrentCounts := make(map[string]int)
	for _, t := range allTenants {
		runs, _ := s.runs.List(context.Background(), t.TenantID)
		for _, r := range runs {
			if r.Status == "running" || r.Status == "queued" {
				concurrentCounts[r.TenantID]++
			}
		}
	}
	if len(concurrentCounts) > 0 {
		s.limiter.WithInitialConcurrent(concurrentCounts)
	}
}

func (s *Server) seedDemoAgent(tenantID string) {
	now := time.Now().UTC().Format(time.RFC3339)
	demoAgent := types.Agent{
		AgentID:     "agt_demo",
		TenantID:    tenantID,
		Name:        "Demo Agent",
		Description: "Sample agent for validation",
		Version:     "1.0",
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	// Best-effort: demo agent may already exist from a previous run.
	if err := s.agents.Create(context.Background(), demoAgent); err != nil {
		slog.Warn("demo agent seed skipped", "agent_id", demoAgent.AgentID, "error", err)
	}
}

// rbacWrap wraps a handler function with RBAC enforcement for the given role.
func (s *Server) rbacWrap(role string, fn http.HandlerFunc) http.Handler {
	return middleware.RBACMiddleware(s.memberStore, role)(http.HandlerFunc(fn))
}

// requireRole checks RBAC for a given role inside a compound handler.
// Returns true if allowed, false (and writes 403) if denied.
func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, role string) bool {
	// Delegate to the same middleware logic by running a sub-handler.
	allowed := true
	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	check := middleware.RBACMiddleware(s.memberStore, role)(inner)
	rec := httptest.NewRecorder()
	check.ServeHTTP(rec, r)
	if rec.Code == http.StatusForbidden {
		// Copy the response to the real writer.
		for k, vs := range rec.Header() {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		allowed = false
	}
	return allowed
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/health", s.health.Handler())
	mux.HandleFunc("/v1/ready", s.health.ReadyHandler())
	mux.Handle("/v1/chat/completions", s.rbacWrap("developer", s.handleChatCompletions))
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/models/", s.handleModelByID)
	mux.HandleFunc("/v1/embeddings", s.handleEmbeddings)
	mux.HandleFunc("/v1/agents/", s.handleAgents) // RBAC applied per-method inside
	mux.HandleFunc("/v1/runs/", s.handleRuns)     // RBAC applied per-method inside
	mux.HandleFunc("/v1/admin/tenants", s.handleTenants)
	mux.HandleFunc("/v1/admin/tenants/", s.handleTenants)
	mux.HandleFunc("/v1/tenants/api-keys", s.handleAPIKeys)
	mux.HandleFunc("/v1/tenants/api-keys/", s.handleAPIKeys)
	mux.HandleFunc("/v1/admin/audit-log", s.handleAuditLog)
	mux.HandleFunc("/v1/admin/circuit-breaker", s.handleCircuitBreakerStatus)
	mux.HandleFunc("/v1/admin/backup-check", s.handleAdminBackupCheck)
	mux.HandleFunc("/v1/admin/purge", s.handleAdminPurge)
	mux.HandleFunc("/v1/admin/pii-scan", s.handleAdminPIIScan)
	mux.HandleFunc("/v1/usage", s.handleUsage)
	mux.HandleFunc("/v1/usage/export", s.handleUsageExport)
	mux.HandleFunc("/v1/auth/refresh", auth.RefreshHandler(nil))
	mux.HandleFunc("/v1/tenants/data-export", s.handleDataExport)
	mux.HandleFunc("/v1/tenants/data-delete", s.handleDataDelete)
	mux.HandleFunc("/v1/tenants/members", s.handleMembers)
	mux.HandleFunc("/v1/tenants/members/", s.handleMembers)
	mux.Handle("/metrics", middleware.ProtectMetrics(metrics.Handler()))

	h := middleware.APIKeyMiddleware(s.apiKeyStore)(mux)
	h = middleware.WithAuth(h)
	h = middleware.EnsureRequestID(h)
	h = logging.RequestContextMiddleware(h)
	h = metrics.Instrument("agent-orchestrator", h)
	return h
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	tenantID, ok := resolveTenant(w, r, ac)
	if !ok {
		return
	}
	if !s.tenantsExists(tenantID) {
		httpx.Error(w, http.StatusForbidden, "tenant_unknown", "tenant not found", httpx.CorrelationID(r), false)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/agents/")
	path = strings.Trim(path, "/")

	// /v1/agents - List or Create agents
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			if !s.requireRole(w, r, "viewer") {
				return
			}
			s.handleAgentList(w, r, tenantID)
		case http.MethodPost:
			if !s.requireRole(w, r, "developer") {
				return
			}
			s.handleAgentCreate(w, r, tenantID)
		default:
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		}
		return
	}

	parts := strings.Split(path, "/")

	// /v1/agents/{agent_id} - Get, Update, or Delete agent
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if !s.requireRole(w, r, "viewer") {
				return
			}
			s.handleAgentGet(w, r, tenantID, parts[0])
		case http.MethodPut:
			if !s.requireRole(w, r, "developer") {
				return
			}
			s.handleAgentUpdate(w, r, tenantID, parts[0])
		case http.MethodDelete:
			if !s.requireRole(w, r, "developer") {
				return
			}
			s.handleAgentDelete(w, r, tenantID, parts[0])
		default:
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		}
		return
	}

	// GET /v1/agents/{agent_id}/versions - List agent version history
	if len(parts) == 2 && parts[1] == "versions" && r.Method == http.MethodGet {
		if !s.requireRole(w, r, "viewer") {
			return
		}
		s.handleAgentVersions(w, r, tenantID, parts[0])
		return
	}

	// POST /v1/agents/{agent_id}/rollback - Rollback agent to a previous version
	if len(parts) == 2 && parts[1] == "rollback" && r.Method == http.MethodPost {
		if !s.requireRole(w, r, "developer") {
			return
		}
		s.handleAgentRollback(w, r, tenantID, parts[0])
		return
	}

	// POST /v1/agents/{agent_id}/runs:batch - Batch create runs
	if len(parts) == 2 && parts[1] == "runs:batch" && r.Method == http.MethodPost {
		if !s.requireRole(w, r, "developer") {
			return
		}
		s.handleRunBatch(w, r, tenantID, parts[0], ac)
		return
	}

	// /v1/agents/{agent_id}/runs - List or Create runs
	if len(parts) == 2 && parts[1] == "runs" {
		switch r.Method {
		case http.MethodGet:
			if !s.requireRole(w, r, "viewer") {
				return
			}
			s.handleRunList(w, r, tenantID, parts[0])
		case http.MethodPost:
			if !s.requireRole(w, r, "developer") {
				return
			}
			s.handleRunCreate(w, r, tenantID, parts[0], ac)
		default:
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		}
		return
	}

	// DELETE /v1/agents/{agent_id}/memory - Clear conversation memory
	if len(parts) == 2 && parts[1] == "memory" && r.Method == http.MethodDelete {
		s.handleMemoryClear(w, r, tenantID, parts[0])
		return
	}

	// /v1/agents/{agent_id}/kv - KV operations
	if len(parts) >= 2 && parts[1] == "kv" {
		s.handleKV(w, r, tenantID, parts[0], parts[2:])
		return
	}

	httpx.Error(w, http.StatusNotFound, "not_found", "not found", httpx.CorrelationID(r), false)
}

func (s *Server) handleAgentList(w http.ResponseWriter, r *http.Request, tenantID string) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	after := r.URL.Query().Get("after")

	agents, err := s.agents.List(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "agent_list_failed", "failed to list agents", httpx.CorrelationID(r), true)
		return
	}
	if agents == nil {
		agents = []types.Agent{}
	}

	// Sort by AgentID for deterministic cursor-based pagination.
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].AgentID < agents[j].AgentID
	})

	// Apply cursor: skip agents with AgentID <= after.
	if after != "" {
		idx := 0
		for idx < len(agents) && agents[idx].AgentID <= after {
			idx++
		}
		agents = agents[idx:]
	}

	hasMore := len(agents) > limit
	if hasMore {
		agents = agents[:limit]
	}

	resp := types.AgentListResponse{Agents: agents, HasMore: hasMore, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentGet(w http.ResponseWriter, r *http.Request, tenantID, agentID string) {
	agent, ok, err := s.agents.Get(r.Context(), tenantID, agentID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "agent_lookup_failed", "failed to load agent", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		// Return 404 for non-existent or different tenant agent
		httpx.Error(w, http.StatusNotFound, "not_found", "agent not found", httpx.CorrelationID(r), false)
		return
	}
	resp := types.AgentGetResponse{Agent: agent, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusOK, resp)
}

// validAgentID matches alphanumeric, hyphens, and underscores, 3-64 chars.
var validAgentID = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,64}$`)

func (s *Server) handleAgentCreate(w http.ResponseWriter, r *http.Request, tenantID string) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req types.AgentCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}

	if req.AgentID == "" || !validAgentID.MatchString(req.AgentID) {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "agent_id must be 3-64 alphanumeric/hyphen/underscore characters", httpx.CorrelationID(r), false)
		return
	}
	if req.Name == "" || len(req.Name) > 256 {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "name is required and must be 1-256 characters", httpx.CorrelationID(r), false)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	agent := types.Agent{
		AgentID:     req.AgentID,
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
		Version:     "1",
		Status:      "active",
		Config:      req.Config,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.agents.Create(r.Context(), agent); err != nil {
		if errors.Is(err, storage.ErrAgentExists) {
			httpx.Error(w, http.StatusConflict, "conflict", "agent already exists", httpx.CorrelationID(r), false)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "agent_create_failed", "failed to create agent", httpx.CorrelationID(r), true)
		return
	}

	// Record version snapshot for the newly created agent.
	configJSON, err := json.Marshal(agent.Config)
	if err != nil {
		slog.Error("failed to marshal agent config for version snapshot", "error", err, "agent_id", agent.AgentID)
	} else {
		ac2, _ := auth.Get(r.Context())
		if snapErr := s.agentVersions.CreateSnapshot(r.Context(), tenantID, agent.AgentID, "v1", ac2.PrincipalID, configJSON); snapErr != nil {
			slog.Error("failed to create version snapshot on agent create", "error", snapErr, "agent_id", agent.AgentID)
		}
	}

	resp := types.AgentCreateResponse{Agent: agent, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusCreated, resp)
}

func (s *Server) handleAgentUpdate(w http.ResponseWriter, r *http.Request, tenantID, agentID string) {
	agent, ok, err := s.agents.Get(r.Context(), tenantID, agentID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "agent_lookup_failed", "failed to load agent", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "agent not found", httpx.CorrelationID(r), false)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req types.AgentUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}

	if req.Name == "" || len(req.Name) > 256 {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "name is required and must be 1-256 characters", httpx.CorrelationID(r), false)
		return
	}

	// Increment version.
	ver, _ := strconv.Atoi(agent.Version)
	agent.Version = strconv.Itoa(ver + 1)
	agent.Name = req.Name
	agent.Description = req.Description
	agent.Config = req.Config
	agent.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := s.agents.Save(r.Context(), agent); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "agent_update_failed", "failed to update agent", httpx.CorrelationID(r), true)
		return
	}

	// Record version snapshot for the updated agent.
	configJSON, err := json.Marshal(agent.Config)
	if err != nil {
		slog.Error("failed to marshal agent config for version snapshot", "error", err, "agent_id", agent.AgentID)
	} else {
		count, countErr := s.agentVersions.CountVersions(r.Context(), tenantID, agent.AgentID)
		if countErr != nil {
			slog.Error("failed to count versions for snapshot", "error", countErr, "agent_id", agent.AgentID)
		} else {
			versionLabel := fmt.Sprintf("v%d", count+1)
			ac2, _ := auth.Get(r.Context())
			if snapErr := s.agentVersions.CreateSnapshot(r.Context(), tenantID, agent.AgentID, versionLabel, ac2.PrincipalID, configJSON); snapErr != nil {
				slog.Error("failed to create version snapshot on agent update", "error", snapErr, "agent_id", agent.AgentID)
			}
		}
	}

	resp := types.AgentGetResponse{Agent: agent, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentDelete(w http.ResponseWriter, r *http.Request, tenantID, agentID string) {
	if err := s.agents.Delete(r.Context(), tenantID, agentID); err != nil {
		if errors.Is(err, storage.ErrAgentNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "agent not found", httpx.CorrelationID(r), false)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "agent_delete_failed", "failed to delete agent", httpx.CorrelationID(r), true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentVersions(w http.ResponseWriter, r *http.Request, tenantID, agentID string) {
	// Verify agent exists.
	_, ok, err := s.agents.Get(r.Context(), tenantID, agentID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "agent_lookup_failed", "failed to load agent", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "agent not found", httpx.CorrelationID(r), false)
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	var afterID int64
	if v := r.URL.Query().Get("after"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			afterID = n
		}
	}

	versions, hasMore, err := s.agentVersions.ListVersions(r.Context(), tenantID, agentID, limit, afterID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "version_list_failed", "failed to list versions", httpx.CorrelationID(r), true)
		return
	}
	if versions == nil {
		versions = []postgres.AgentVersion{}
	}

	resp := map[string]any{
		"versions":       versions,
		"has_more":       hasMore,
		"correlation_id": httpx.CorrelationID(r),
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentRollback(w http.ResponseWriter, r *http.Request, tenantID, agentID string) {
	// Verify agent exists.
	agent, ok, err := s.agents.Get(r.Context(), tenantID, agentID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "agent_lookup_failed", "failed to load agent", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "agent not found", httpx.CorrelationID(r), false)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}
	if req.Version == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "version is required", httpx.CorrelationID(r), false)
		return
	}

	// Look up the target version snapshot.
	snapshot, err := s.agentVersions.GetVersion(r.Context(), tenantID, agentID, req.Version)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "version_lookup_failed", "failed to look up version", httpx.CorrelationID(r), true)
		return
	}
	if snapshot == nil {
		httpx.Error(w, http.StatusNotFound, "version_not_found", "version not found", httpx.CorrelationID(r), false)
		return
	}

	// Check for running runs on this agent.
	runs, err := s.runs.ListByAgent(r.Context(), tenantID, agentID, 100, "")
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_check_failed", "failed to check running runs", httpx.CorrelationID(r), true)
		return
	}
	for _, run := range runs {
		if run.Status == "running" || run.Status == "queued" {
			httpx.Error(w, http.StatusConflict, "active_runs", "cannot rollback while runs are active", httpx.CorrelationID(r), false)
			return
		}
	}

	// Apply the snapshot config to the agent.
	var snapshotConfig *types.AgentConfig
	if len(snapshot.Config) > 0 && string(snapshot.Config) != "null" {
		var cfg types.AgentConfig
		if err := json.Unmarshal(snapshot.Config, &cfg); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "config_parse_failed", "failed to parse snapshot config", httpx.CorrelationID(r), true)
			return
		}
		snapshotConfig = &cfg
	}

	// Increment agent version.
	ver, _ := strconv.Atoi(agent.Version)
	agent.Version = strconv.Itoa(ver + 1)
	agent.Config = snapshotConfig
	agent.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := s.agents.Save(r.Context(), agent); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "agent_update_failed", "failed to update agent", httpx.CorrelationID(r), true)
		return
	}

	// Record the rollback as a new version snapshot.
	count, countErr := s.agentVersions.CountVersions(r.Context(), tenantID, agentID)
	if countErr != nil {
		slog.Error("failed to count versions for rollback snapshot", "error", countErr, "agent_id", agentID)
	} else {
		versionLabel := fmt.Sprintf("v%d", count+1)
		ac, _ := auth.Get(r.Context())
		if snapErr := s.agentVersions.CreateSnapshot(r.Context(), tenantID, agentID, versionLabel, ac.PrincipalID, snapshot.Config); snapErr != nil {
			slog.Error("failed to create version snapshot on rollback", "error", snapErr, "agent_id", agentID)
		}
	}

	resp := types.AgentGetResponse{Agent: agent, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleRunCreate(w http.ResponseWriter, r *http.Request, tenantID, agentID string, ac auth.AuthContext) {
	if !s.limiter.AllowQPS(tenantID) {
		metrics.IncQuotaDenied("agent-orchestrator", "runs_create_qps")
		httpx.Error(w, http.StatusTooManyRequests, "quota_exceeded", "run create QPS exceeded", httpx.CorrelationID(r), true)
		s.audit.Log(audit.Entry{
			TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "runs.create", Resource: "agent-orchestrator", Outcome: "denied",
			CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
			Meta: map[string]any{"reason": "qps_exceeded"},
		})
		return
	}
	if !s.limiter.TryIncConcurrent(tenantID) {
		metrics.IncQuotaDenied("agent-orchestrator", "runs_concurrency")
		httpx.Error(w, http.StatusTooManyRequests, "quota_exceeded", "concurrent runs exceeded", httpx.CorrelationID(r), true)
		s.audit.Log(audit.Entry{
			TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "runs.create", Resource: "agent-orchestrator", Outcome: "denied",
			CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
			Meta: map[string]any{"reason": "concurrent_exceeded"},
		})
		return
	}
	if s.tokenBudget != nil && !s.tokenBudget.Check(tenantID) {
		s.limiter.DecConcurrent(tenantID)
		metrics.IncQuotaDenied("agent-orchestrator", "token_budget")
		httpx.Error(w, http.StatusTooManyRequests, "token_budget_exceeded", "hourly token budget exceeded", httpx.CorrelationID(r), true)
		s.audit.Log(audit.Entry{
			TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "runs.create", Resource: "agent-orchestrator", Outcome: "denied",
			CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
			Meta: map[string]any{"reason": "token_budget_exceeded"},
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req types.RunCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.limiter.DecConcurrent(tenantID)
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}

	// Check idempotency key if provided
	if req.IdempotencyKey != "" {
		existingRun, found, err := s.runs.GetByIdempotencyKey(r.Context(), tenantID, req.IdempotencyKey)
		if err != nil {
			// Fail-open for availability: log error and proceed with creation
			// In production, this should use structured logging
		} else if found {
			// Return existing run with 200 OK (not 201 Created)
			s.limiter.DecConcurrent(tenantID)
			resp := types.RunCreateResponse{Run: existingRun, CorrelationID: httpx.CorrelationID(r)}
			httpx.JSON(w, http.StatusOK, resp)
			return
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	runID := id.New("run")

	run := types.Run{
		TenantID:       tenantID,
		AgentID:        agentID,
		RunID:          runID,
		Status:         "queued",
		CreatedAt:      now,
		EventsURL:      "/v1/runs/" + runID + "/events",
		Input:          req.Input,
		RunOptions:     req.RunOptions,
		IdempotencyKey: req.IdempotencyKey,
	}

	if err := s.runs.Create(r.Context(), run); err != nil {
		s.limiter.DecConcurrent(tenantID)
		code := http.StatusInternalServerError
		errCode := "run_persist_failed"
		retryable := true
		if errors.Is(err, storage.ErrRunExists) {
			code = http.StatusConflict
			errCode = "conflict"
			retryable = false
		} else if errors.Is(err, storage.ErrInvalidRun) {
			code = http.StatusBadRequest
			errCode = "invalid_request"
			retryable = false
		}
		httpx.Error(w, code, errCode, "failed to persist run", httpx.CorrelationID(r), retryable)
		return
	}

	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "runs.create", Resource: "run/" + runID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		Meta: map[string]any{"agent_id": agentID},
	})

	// Submit to executor for async processing.
	if s.executor != nil {
		// Resolve agent for config.
		agent, agentOk, _ := s.agents.Get(r.Context(), tenantID, agentID)
		if !agentOk {
			// Use a minimal agent if not found.
			agent = types.Agent{AgentID: agentID, TenantID: tenantID}
		}

		// Determine timeout.
		timeout := 300 * time.Second
		if run.RunOptions.TimeoutMs > 0 {
			timeout = time.Duration(run.RunOptions.TimeoutMs) * time.Millisecond
		} else if agent.Config != nil && agent.Config.TimeoutMs > 0 {
			timeout = time.Duration(agent.Config.TimeoutMs) * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		var events *EventSink
		if s.executor.eventLog != nil {
			events = NewEventSinkWithLog(tenantID, agentID, runID, s.executor.eventLog)
		} else {
			events = NewEventSink(tenantID, agentID, runID)
		}
		job := &runJob{
			run:    run,
			agent:  agent,
			ctx:    ctx,
			cancel: cancel,
			events: events,
		}
		s.executor.Submit(job)
	}

	resp := types.RunCreateResponse{Run: run, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusCreated, resp)
}

func (s *Server) handleRunBatch(w http.ResponseWriter, r *http.Request, tenantID, agentID string, ac auth.AuthContext) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req types.RunBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}

	if len(req.Runs) == 0 {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "at least one run is required", httpx.CorrelationID(r), false)
		return
	}
	if len(req.Runs) > 50 {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "maximum 50 runs per batch", httpx.CorrelationID(r), false)
		return
	}

	// Resolve agent once.
	agent, agentOk, _ := s.agents.Get(r.Context(), tenantID, agentID)
	if !agentOk {
		agent = types.Agent{AgentID: agentID, TenantID: tenantID}
	}

	var created []types.Run
	for _, runReq := range req.Runs {
		// Check QPS quota.
		if !s.limiter.AllowQPS(tenantID) {
			metrics.IncQuotaDenied("agent-orchestrator", "runs_create_qps")
			httpx.Error(w, http.StatusTooManyRequests, "quota_exceeded", "run create QPS exceeded", httpx.CorrelationID(r), true)
			return
		}
		// Check concurrency quota.
		if !s.limiter.TryIncConcurrent(tenantID) {
			metrics.IncQuotaDenied("agent-orchestrator", "runs_concurrency")
			httpx.Error(w, http.StatusTooManyRequests, "quota_exceeded", "concurrent runs exceeded", httpx.CorrelationID(r), true)
			return
		}
		// Check token budget.
		if s.tokenBudget != nil && !s.tokenBudget.Check(tenantID) {
			s.limiter.DecConcurrent(tenantID)
			metrics.IncQuotaDenied("agent-orchestrator", "token_budget")
			httpx.Error(w, http.StatusTooManyRequests, "token_budget_exceeded", "hourly token budget exceeded", httpx.CorrelationID(r), true)
			return
		}

		now := time.Now().UTC().Format(time.RFC3339)
		runID := id.New("run")
		run := types.Run{
			TenantID:       tenantID,
			AgentID:        agentID,
			RunID:          runID,
			Status:         "queued",
			CreatedAt:      now,
			EventsURL:      "/v1/runs/" + runID + "/events",
			Input:          runReq.Input,
			RunOptions:     runReq.RunOptions,
			IdempotencyKey: runReq.IdempotencyKey,
		}

		if err := s.runs.Create(r.Context(), run); err != nil {
			s.limiter.DecConcurrent(tenantID)
			// Log but continue with partial results if we've already created some.
			if len(created) > 0 {
				break
			}
			httpx.Error(w, http.StatusInternalServerError, "run_persist_failed", "failed to persist run", httpx.CorrelationID(r), true)
			return
		}

		// Submit to executor.
		if s.executor != nil {
			timeout := 300 * time.Second
			if run.RunOptions.TimeoutMs > 0 {
				timeout = time.Duration(run.RunOptions.TimeoutMs) * time.Millisecond
			} else if agent.Config != nil && agent.Config.TimeoutMs > 0 {
				timeout = time.Duration(agent.Config.TimeoutMs) * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			var events *EventSink
			if s.executor.eventLog != nil {
				events = NewEventSinkWithLog(tenantID, agentID, runID, s.executor.eventLog)
			} else {
				events = NewEventSink(tenantID, agentID, runID)
			}
			job := &runJob{
				run:    run,
				agent:  agent,
				ctx:    ctx,
				cancel: cancel,
				events: events,
			}
			s.executor.Submit(job)
		}

		created = append(created, run)
	}

	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "runs.batch_create", Resource: "agent/" + agentID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		Meta: map[string]any{"count": len(created)},
	})

	resp := types.RunBatchResponse{Runs: created, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusCreated, resp)
}

func (s *Server) handleRunList(w http.ResponseWriter, r *http.Request, tenantID, agentID string) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	after := r.URL.Query().Get("after")
	status := r.URL.Query().Get("status")

	runs, err := s.runs.ListByAgent(r.Context(), tenantID, agentID, limit+1, after)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_list_failed", "failed to list runs", httpx.CorrelationID(r), true)
		return
	}
	if runs == nil {
		runs = []types.Run{}
	}

	// Filter by status if requested.
	if status != "" {
		filtered := make([]types.Run, 0, len(runs))
		for _, run := range runs {
			if run.Status == status {
				filtered = append(filtered, run)
			}
		}
		runs = filtered
	}

	hasMore := len(runs) > limit
	if hasMore {
		runs = runs[:limit]
	}

	resp := types.RunListResponse{
		Runs:          runs,
		HasMore:       hasMore,
		CorrelationID: httpx.CorrelationID(r),
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	tenantID, ok := resolveTenant(w, r, ac)
	if !ok {
		return
	}
	if !s.tenantsExists(tenantID) {
		httpx.Error(w, http.StatusForbidden, "tenant_unknown", "tenant not found", httpx.CorrelationID(r), false)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		httpx.Error(w, http.StatusNotFound, "not_found", "not found", httpx.CorrelationID(r), false)
		return
	}
	runID := parts[0]

	// Check for :cancel action (e.g., /v1/runs/run_123:cancel)
	if strings.HasSuffix(runID, ":cancel") {
		if !s.requireRole(w, r, "developer") {
			return
		}
		runID = strings.TrimSuffix(runID, ":cancel")
		s.handleCancel(w, r, tenantID, runID, ac)
		return
	}

	// POST /v1/runs/{id}/retry — retry a failed/errored run
	if len(parts) == 2 && parts[1] == "retry" {
		if r.Method != http.MethodPost {
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
			return
		}
		if !s.requireRole(w, r, "developer") {
			return
		}
		s.handleRunRetry(w, r, tenantID, runID, ac)
		return
	}

	if len(parts) == 2 && parts[1] == "events" {
		if !s.requireRole(w, r, "viewer") {
			return
		}
		s.handleEvents(w, r, tenantID, runID)
		return
	}

	if len(parts) == 2 && parts[1] == "stream" {
		if !s.requireRole(w, r, "viewer") {
			return
		}
		s.handleStream(w, r, tenantID, runID)
		return
	}

	if r.Method != http.MethodGet || len(parts) != 1 {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	if !s.requireRole(w, r, "viewer") {
		return
	}

	run, ok, err := s.runs.Get(r.Context(), tenantID, runID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_lookup_failed", "failed to load run", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "run not found", httpx.CorrelationID(r), false)
		return
	}

	resp := types.RunGetResponse{Run: run, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, tenantID, runID string) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	run, ok, err := s.runs.Get(r.Context(), tenantID, runID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_lookup_failed", "failed to load run", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "run not found", httpx.CorrelationID(r), false)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, okf := w.(http.Flusher)
	if !okf {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Parse Last-Event-ID for reconnection support.
	afterSequence := 0
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if n, err := strconv.Atoi(lastID); err == nil {
			afterSequence = n
		}
	}

	// Retrieve events from the executor's event sink, if available.
	var envs []types.EventEnvelope
	if s.executor != nil {
		if sink := s.executor.Events(runID); sink != nil {
			if afterSequence > 0 {
				envs = sink.EventsFromSequence(afterSequence)
			} else {
				envs = sink.Events()
			}
		}
	}

	// Fallback to durable event log if no in-memory events (run may have completed).
	if len(envs) == 0 && s.executor != nil && s.executor.eventLog != nil {
		if logged, err := s.executor.eventLog.QueryFromSequence(r.Context(), tenantID, runID, afterSequence); err == nil && len(logged) > 0 {
			envs = logged
		}
	}

	// Fallback: if no events recorded, emit a synthetic event.
	if len(envs) == 0 && afterSequence == 0 {
		envs = []types.EventEnvelope{{
			Event: types.Event{
				EventID:  id.New("evt"),
				Sequence: 1,
				Time:     time.Now().UTC().Format(time.RFC3339),
				Type:     "agentos.run.step.completed",
				TenantID: run.TenantID,
				AgentID:  run.AgentID,
				RunID:    run.RunID,
				StepID:   "step_1",
				Trace:    types.TraceContext{Traceparent: "00-00000000000000000000000000000000-0000000000000000-01"},
				Payload:  map[string]any{"status": "ok"},
			},
		}}
	}

	bw := bufio.NewWriter(w)
	for _, env := range envs {
		b, _ := json.Marshal(env)
		// Emit event ID for SSE reconnection.
		_, _ = bw.WriteString("id: " + strconv.Itoa(env.Event.Sequence) + "\n")
		_, _ = bw.WriteString("event: agentos.event\n")
		_, _ = bw.WriteString("data: " + string(b) + "\n\n")
	}
	_ = bw.Flush()
	flusher.Flush()
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, tenantID, runID string) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	run, ok, err := s.runs.Get(r.Context(), tenantID, runID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_lookup_failed", "failed to load run", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "run not found", httpx.CorrelationID(r), false)
		return
	}

	// If the run is already terminal, send done and return.
	switch run.Status {
	case "completed", "failed", "canceled":
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, _ = fmt.Fprint(w, "data: {\"done\":true}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Look up the active event sink.
	var sink *EventSink
	if s.executor != nil {
		sink = s.executor.EventSinkForRun(runID)
	}
	if sink == nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "no active event sink for run", httpx.CorrelationID(r), false)
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, okf := w.(http.Flusher)
	if !okf {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	// Parse from_sequence query parameter.
	fromSequence := 0
	if qs := r.URL.Query().Get("from_sequence"); qs != "" {
		if n, parseErr := strconv.Atoi(qs); parseErr == nil {
			fromSequence = n
		}
	}

	bw := bufio.NewWriter(w)

	// Replay historical events with sequence > fromSequence.
	if fromSequence >= 0 {
		var replay []types.EventEnvelope
		if fromSequence > 0 {
			replay = sink.EventsFromSequence(fromSequence)
		} else {
			replay = sink.Events()
		}
		for _, env := range replay {
			b, marshalErr := json.Marshal(env)
			if marshalErr != nil {
				slog.Error("failed to marshal event for stream", "error", marshalErr, "run_id", runID)
				continue
			}
			_, _ = bw.WriteString("id: " + strconv.Itoa(env.Event.Sequence) + "\n")
			_, _ = bw.WriteString("data: " + string(b) + "\n\n")
		}
		_ = bw.Flush()
		flusher.Flush()
	}

	// Subscribe for live events.
	ch := sink.Subscribe()
	defer sink.Unsubscribe(ch)

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected.
			return
		case env, open := <-ch:
			if !open {
				// Run completed, send done signal.
				_, _ = bw.WriteString("data: {\"done\":true}\n\n")
				_ = bw.Flush()
				flusher.Flush()
				return
			}
			b, marshalErr := json.Marshal(env)
			if marshalErr != nil {
				slog.Error("failed to marshal event for stream", "error", marshalErr, "run_id", runID)
				continue
			}
			_, _ = bw.WriteString("id: " + strconv.Itoa(env.Event.Sequence) + "\n")
			_, _ = bw.WriteString("data: " + string(b) + "\n\n")
			_ = bw.Flush()
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = bw.WriteString(": heartbeat\n\n")
			_ = bw.Flush()
			flusher.Flush()
		}
	}
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, tenantID, runID string, ac auth.AuthContext) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	run, ok, err := s.runs.Get(r.Context(), tenantID, runID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_lookup_failed", "failed to load run", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		// Tenant isolation: return 404 (not 403) for runs not in this tenant
		httpx.Error(w, http.StatusNotFound, "not_found", "run not found", httpx.CorrelationID(r), false)
		return
	}

	// State transition validation
	switch run.Status {
	case "completed", "failed", "canceled":
		// Cannot cancel from terminal states
		httpx.Error(w, http.StatusConflict, "invalid_state_transition", "cannot cancel run in "+run.Status+" state", httpx.CorrelationID(r), false)
		return
	case "queued", "running":
		// Can cancel from these states
	default:
		// Unknown state, allow cancellation
	}

	// Cancel the executor job if running.
	if s.executor != nil {
		s.executor.Cancel(run.RunID)
	}

	// Update run to canceled state
	run.Status = "canceled"
	run.CompletedAt = time.Now().UTC().Format(time.RFC3339)

	if err := s.runs.Save(r.Context(), run); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_persist_failed", "failed to persist run cancellation", httpx.CorrelationID(r), true)
		return
	}

	// Cascade cancellation to child runs.
	s.cascadeCancelChildren(r.Context(), tenantID, runID, 0)

	// Decrement concurrent run quota
	s.limiter.DecConcurrent(tenantID)

	// Audit log
	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "runs.cancel", Resource: "run/" + runID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
	})

	resp := types.RunCancelResponse{Run: run, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusOK, resp)
}

// maxCancelDepth returns the configured maximum delegation depth for cascading
// cancellation, from AGENTOS_MAX_DELEGATION_DEPTH env var (default 5).
func maxCancelDepth() int {
	if v := os.Getenv("AGENTOS_MAX_DELEGATION_DEPTH"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return 5
}

// cascadeCancelChildren finds child runs of parentRunID (via retry_of or
// parent_run_id) and cancels any that are in queued or running state.
// Recursion is bounded by AGENTOS_MAX_DELEGATION_DEPTH.
func (s *Server) cascadeCancelChildren(ctx context.Context, tenantID, parentRunID string, depth int) {
	maxDepth := maxCancelDepth()
	if depth >= maxDepth {
		slog.Warn("cascade cancel depth limit reached",
			"parent_run_id", parentRunID,
			"depth", depth,
			"max_depth", maxDepth,
		)
		return
	}

	children, err := s.runs.ListChildRuns(ctx, tenantID, parentRunID)
	if err != nil {
		slog.Error("failed to list child runs for cascade cancel",
			"parent_run_id", parentRunID,
			"error", err,
		)
		return
	}

	for _, child := range children {
		if child.Status != "queued" && child.Status != "running" {
			continue
		}

		slog.Info("cascaded cancel",
			"parent_run_id", parentRunID,
			"child_run_id", child.RunID,
		)

		// Cancel the executor job if running.
		if s.executor != nil {
			s.executor.Cancel(child.RunID)
		}

		child.Status = "canceled"
		child.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		if err := s.runs.Save(ctx, child); err != nil {
			slog.Error("failed to persist child run cancellation",
				"child_run_id", child.RunID,
				"error", err,
			)
			continue
		}

		s.limiter.DecConcurrent(tenantID)

		// Recurse into grandchildren.
		s.cascadeCancelChildren(ctx, tenantID, child.RunID, depth+1)
	}
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	if !hasScope(ac.Scopes, "tenants:admin") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "admin scope required", httpx.CorrelationID(r), false)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/admin/tenants")
	path = strings.Trim(path, "/")

	if path == "" {
		switch r.Method {
		case http.MethodGet:
			list := s.tenants.List()
			httpx.JSON(w, http.StatusOK, map[string]any{"tenants": list, "correlation_id": httpx.CorrelationID(r)})
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
			var t types.Tenant
			if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
				httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
				return
			}
			if err := s.tenants.Create(t); err != nil {
				code := http.StatusBadRequest
				errCode := "invalid_request"
				if errors.Is(err, tenants.ErrTenantExists) {
					code = http.StatusConflict
					errCode = "conflict"
				}
				httpx.Error(w, code, errCode, err.Error(), httpx.CorrelationID(r), false)
				return
			}
			s.audit.Log(audit.Entry{
				TenantID: t.TenantID, PrincipalID: ac.PrincipalID, Action: "tenants.create", Resource: "tenant/" + t.TenantID, Outcome: "allowed",
				CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
			})
			httpx.JSON(w, http.StatusCreated, map[string]any{"tenant": t, "correlation_id": httpx.CorrelationID(r)})
		default:
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		}
		return
	}

	tenantID := path
	switch r.Method {
	case http.MethodGet:
		if t, ok := s.tenants.Get(tenantID); ok {
			httpx.JSON(w, http.StatusOK, map[string]any{"tenant": t, "correlation_id": httpx.CorrelationID(r)})
			return
		}
		httpx.Error(w, http.StatusNotFound, "not_found", "tenant not found", httpx.CorrelationID(r), false)
	case http.MethodPut, http.MethodPatch:
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		var t types.Tenant
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
			return
		}
		updated, err := s.tenants.Update(tenantID, t)
		if err != nil {
			code := http.StatusBadRequest
			errCode := "invalid_request"
			if errors.Is(err, tenants.ErrNotFound) {
				code = http.StatusNotFound
				errCode = "not_found"
			}
			httpx.Error(w, code, errCode, err.Error(), httpx.CorrelationID(r), false)
			return
		}
		s.audit.Log(audit.Entry{
			TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "tenants.update", Resource: "tenant/" + tenantID, Outcome: "allowed",
			CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		})
		httpx.JSON(w, http.StatusOK, map[string]any{"tenant": updated, "correlation_id": httpx.CorrelationID(r)})
	case http.MethodDelete:
		if _, err := s.tenants.Delete(tenantID); err != nil {
			code := http.StatusBadRequest
			errCode := "invalid_request"
			if errors.Is(err, tenants.ErrNotFound) {
				code = http.StatusNotFound
				errCode = "not_found"
			}
			httpx.Error(w, code, errCode, err.Error(), httpx.CorrelationID(r), false)
			return
		}
		s.audit.Log(audit.Entry{
			TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "tenants.delete", Resource: "tenant/" + tenantID, Outcome: "allowed",
			CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		})
		httpx.JSON(w, http.StatusOK, map[string]any{"deleted": tenantID, "correlation_id": httpx.CorrelationID(r)})
	default:
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
	}
}

func (s *Server) handleMemoryClear(w http.ResponseWriter, r *http.Request, tenantID, agentID string) {
	if s.memory == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "memory store not configured", httpx.CorrelationID(r), false)
		return
	}
	if err := s.memory.Clear(r.Context(), tenantID, agentID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "memory_clear_failed", "failed to clear memory", httpx.CorrelationID(r), true)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cleared": true, "correlation_id": httpx.CorrelationID(r)})
}

func (s *Server) handleKV(w http.ResponseWriter, r *http.Request, tenantID, agentID string, subParts []string) {
	if s.kv == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "kv store not configured", httpx.CorrelationID(r), false)
		return
	}

	// GET /v1/agents/{agent_id}/kv — list keys
	if len(subParts) == 0 {
		if r.Method != http.MethodGet {
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
			return
		}
		keys, err := s.kv.ListKeys(r.Context(), tenantID, agentID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "kv_list_failed", "failed to list keys", httpx.CorrelationID(r), true)
			return
		}
		if keys == nil {
			keys = []string{}
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"keys": keys, "correlation_id": httpx.CorrelationID(r)})
		return
	}

	// Operations on /v1/agents/{agent_id}/kv/{key}
	key := subParts[0]
	if err := types.ValidateKey(key); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_key", err.Error(), httpx.CorrelationID(r), false)
		return
	}

	switch r.Method {
	case http.MethodGet:
		value, found, err := s.kv.Get(r.Context(), tenantID, agentID, key)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "kv_get_failed", "failed to get key", httpx.CorrelationID(r), true)
			return
		}
		if !found {
			httpx.Error(w, http.StatusNotFound, "not_found", "key not found", httpx.CorrelationID(r), false)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"key": key, "value": value, "correlation_id": httpx.CorrelationID(r)})

	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, int64(s.storageCfg.KVMaxValueSize)+1024))
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "read_failed", "failed to read body", httpx.CorrelationID(r), false)
			return
		}
		var payload struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
			return
		}
		if err := types.ValidateValueSize(payload.Value, s.storageCfg.KVMaxValueSize); err != nil {
			httpx.Error(w, http.StatusBadRequest, "value_too_large", err.Error(), httpx.CorrelationID(r), false)
			return
		}
		if err := s.kv.Set(r.Context(), tenantID, agentID, key, payload.Value); err != nil {
			code := http.StatusInternalServerError
			errCode := "kv_set_failed"
			msg := "failed to set key"
			if errors.Is(err, types.ErrMaxKeysExceeded) {
				code = http.StatusBadRequest
				errCode = "max_keys_exceeded"
				msg = err.Error()
			}
			httpx.Error(w, code, errCode, msg, httpx.CorrelationID(r), code == http.StatusInternalServerError)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"key": key, "correlation_id": httpx.CorrelationID(r)})

	case http.MethodDelete:
		if err := s.kv.Delete(r.Context(), tenantID, agentID, key); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "kv_delete_failed", "failed to delete key", httpx.CorrelationID(r), true)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"deleted": key, "correlation_id": httpx.CorrelationID(r)})

	default:
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
	}
}

// resolveTenant delegates to the shared auth.ResolveTenantHTTP helper (v9.0 L-8).
func resolveTenant(w http.ResponseWriter, r *http.Request, ac auth.AuthContext) (string, bool) {
	return auth.ResolveTenantHTTP(w, r, ac)
}

func (s *Server) tenantsExists(tenantID string) bool {
	if tenantID == "" {
		return false
	}
	_, ok := s.tenants.Get(tenantID)
	return ok
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	tenantID, ok := resolveTenant(w, r, ac)
	if !ok {
		return
	}

	// Extract key_id from path for DELETE /v1/tenants/api-keys/{key_id}.
	path := strings.TrimPrefix(r.URL.Path, "/v1/tenants/api-keys")
	keyID := strings.Trim(path, "/")

	switch {
	case r.Method == http.MethodGet && keyID == "":
		s.handleAPIKeyList(w, r, tenantID)
	case r.Method == http.MethodPost && keyID == "":
		s.handleAPIKeyCreate(w, r, tenantID, ac)
	case r.Method == http.MethodDelete && keyID != "":
		s.handleAPIKeyRevoke(w, r, tenantID, keyID, ac)
	default:
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
	}
}

func (s *Server) handleAPIKeyList(w http.ResponseWriter, r *http.Request, tenantID string) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	after := r.URL.Query().Get("after")

	// If no API key store is configured, return empty list.
	if s.apiKeyStore == nil {
		resp := map[string]any{
			"api_keys":       []any{},
			"has_more":       false,
			"correlation_id": httpx.CorrelationID(r),
		}
		httpx.JSON(w, http.StatusOK, resp)
		return
	}

	type apiKeyLister interface {
		List(ctx context.Context, tenantID string) ([]postgres.APIKeyRecord, error)
	}
	lister, ok := s.apiKeyStore.(apiKeyLister)
	if !ok {
		resp := map[string]any{
			"api_keys":       []any{},
			"has_more":       false,
			"correlation_id": httpx.CorrelationID(r),
		}
		httpx.JSON(w, http.StatusOK, resp)
		return
	}

	keys, err := lister.List(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "apikey_list_failed", "failed to list API keys", httpx.CorrelationID(r), true)
		return
	}
	if keys == nil {
		keys = []postgres.APIKeyRecord{}
	}

	if after != "" {
		idx := 0
		for idx < len(keys) && keys[idx].KeyID <= after {
			idx++
		}
		keys = keys[idx:]
	}

	hasMore := len(keys) > limit
	if hasMore {
		keys = keys[:limit]
	}

	resp := map[string]any{
		"api_keys":       keys,
		"has_more":       hasMore,
		"correlation_id": httpx.CorrelationID(r),
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request, tenantID string, ac auth.AuthContext) {
	if !s.requireRole(w, r, "admin") {
		return
	}

	if s.apiKeyStore == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "API key management requires postgres backend", httpx.CorrelationID(r), false)
		return
	}

	type apiKeyCreator interface {
		Create(ctx context.Context, key postgres.APIKeyRecord) error
	}
	creator, ok := s.apiKeyStore.(apiKeyCreator)
	if !ok {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "API key creation not supported by store", httpx.CorrelationID(r), false)
		return
	}

	var req struct {
		Name          string `json:"name"`
		Role          string `json:"role"`
		ExpiresInDays *int   `json:"expires_in_days,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}
	if req.Name == "" {
		req.Name = "unnamed"
	}
	if req.Role == "" {
		req.Role = "developer"
	}
	validRoles := map[string]bool{"owner": true, "admin": true, "developer": true, "viewer": true}
	if !validRoles[req.Role] {
		httpx.Error(w, http.StatusBadRequest, "invalid_role", "role must be one of: owner, admin, developer, viewer", httpx.CorrelationID(r), false)
		return
	}

	keyID, fullKey, prefix, keyHash, err := auth.GenerateKey()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "key_generation_failed", "failed to generate API key", httpx.CorrelationID(r), true)
		return
	}

	now := time.Now().UTC()
	rec := postgres.APIKeyRecord{
		KeyID:     keyID,
		TenantID:  tenantID,
		KeyHash:   keyHash,
		KeyPrefix: prefix,
		Name:      req.Name,
		Role:      req.Role,
		CreatedBy: ac.PrincipalID,
		CreatedAt: now,
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		exp := now.AddDate(0, 0, *req.ExpiresInDays)
		rec.ExpiresAt = &exp
	}

	if err := creator.Create(r.Context(), rec); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "key_create_failed", "failed to create API key", httpx.CorrelationID(r), true)
		return
	}

	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "apikeys.create",
		Resource: "apikey/" + keyID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
	})

	resp := map[string]any{
		"key_id":         keyID,
		"api_key":        fullKey,
		"name":           req.Name,
		"role":           req.Role,
		"created_at":     rec.CreatedAt.Format(time.RFC3339),
		"correlation_id": httpx.CorrelationID(r),
	}
	if rec.ExpiresAt != nil {
		resp["expires_at"] = rec.ExpiresAt.Format(time.RFC3339)
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

func (s *Server) handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request, tenantID, keyID string, ac auth.AuthContext) {
	if !s.requireRole(w, r, "admin") {
		return
	}

	if s.apiKeyStore == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "API key management requires postgres backend", httpx.CorrelationID(r), false)
		return
	}

	type apiKeyRevoker interface {
		Revoke(ctx context.Context, tenantID, keyID string) error
	}
	revoker, ok := s.apiKeyStore.(apiKeyRevoker)
	if !ok {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "API key revocation not supported by store", httpx.CorrelationID(r), false)
		return
	}

	if err := revoker.Revoke(r.Context(), tenantID, keyID); err != nil {
		if errors.Is(err, postgres.ErrAPIKeyNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "API key not found or already revoked", httpx.CorrelationID(r), false)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "key_revoke_failed", "failed to revoke API key", httpx.CorrelationID(r), true)
		return
	}

	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "apikeys.revoke",
		Resource: "apikey/" + keyID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
	})

	now := time.Now().UTC()
	httpx.JSON(w, http.StatusOK, map[string]any{
		"key_id":         keyID,
		"revoked_at":     now.Format(time.RFC3339),
		"correlation_id": httpx.CorrelationID(r),
	})
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	ac, _ := auth.Get(r.Context())
	if !hasScope(ac.Scopes, "tenants:admin") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "admin scope required", httpx.CorrelationID(r), false)
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	after := r.URL.Query().Get("after")
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	action := r.URL.Query().Get("action")

	// Delegate to the admin audit query handler.
	entries := admin.QueryAuditLog(s.audit, admin.AuditQueryParams{
		Start:  start,
		End:    end,
		Action: action,
		Limit:  limit,
		After:  after,
	})

	httpx.JSON(w, http.StatusOK, map[string]any{
		"entries":        entries.Entries,
		"has_more":       entries.HasMore,
		"correlation_id": httpx.CorrelationID(r),
	})
}

func hasScope(scopes []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, s := range scopes {
		if strings.EqualFold(strings.TrimSpace(s), target) {
			return true
		}
	}
	return false
}

// handleRunRetry creates a new run that retries a failed/errored run.
func (s *Server) handleRunRetry(w http.ResponseWriter, r *http.Request, tenantID, runID string, ac auth.AuthContext) {
	original, ok, err := s.runs.Get(r.Context(), tenantID, runID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_lookup_failed", "failed to load run", httpx.CorrelationID(r), true)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "run not found", httpx.CorrelationID(r), false)
		return
	}

	// Only terminal failure states can be retried.
	if original.Status != "failed" && original.Status != "error" {
		httpx.Error(w, http.StatusConflict, "run_not_terminal", "only failed or errored runs can be retried", httpx.CorrelationID(r), false)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	newRunID := id.New("run")

	retryRun := types.Run{
		TenantID:   original.TenantID,
		AgentID:    original.AgentID,
		RunID:      newRunID,
		Status:     "queued",
		CreatedAt:  now,
		EventsURL:  "/v1/runs/" + newRunID + "/events",
		Input:      original.Input,
		RunOptions: original.RunOptions,
		RetryOf:    original.RunID,
	}

	if err := s.runs.Create(r.Context(), retryRun); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "run_persist_failed", "failed to persist retry run", httpx.CorrelationID(r), true)
		return
	}

	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "runs.retry", Resource: "run/" + newRunID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		Meta: map[string]any{"original_run_id": original.RunID, "agent_id": original.AgentID},
	})

	// Submit the retry run to the executor.
	if s.executor != nil {
		agent, agentOk, _ := s.agents.Get(r.Context(), tenantID, original.AgentID)
		if !agentOk {
			agent = types.Agent{AgentID: original.AgentID, TenantID: tenantID}
		}

		timeout := 300 * time.Second
		if retryRun.RunOptions.TimeoutMs > 0 {
			timeout = time.Duration(retryRun.RunOptions.TimeoutMs) * time.Millisecond
		} else if agent.Config != nil && agent.Config.TimeoutMs > 0 {
			timeout = time.Duration(agent.Config.TimeoutMs) * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		var events *EventSink
		if s.executor.eventLog != nil {
			events = NewEventSinkWithLog(tenantID, original.AgentID, newRunID, s.executor.eventLog)
		} else {
			events = NewEventSink(tenantID, original.AgentID, newRunID)
		}
		job := &runJob{
			run:    retryRun,
			agent:  agent,
			ctx:    ctx,
			cancel: cancel,
			events: events,
		}
		s.executor.Submit(job)
	}

	resp := types.RunCreateResponse{Run: retryRun, CorrelationID: httpx.CorrelationID(r)}
	httpx.JSON(w, http.StatusCreated, resp)
}

// handleCircuitBreakerStatus returns the circuit breaker status (admin only).
func (s *Server) handleCircuitBreakerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	ac, _ := auth.Get(r.Context())
	if !hasScope(ac.Scopes, "tenants:admin") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "admin scope required", httpx.CorrelationID(r), false)
		return
	}

	if s.executor == nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"state": "not_configured", "correlation_id": httpx.CorrelationID(r)})
		return
	}

	type circuitBreakerReporter interface {
		CircuitBreakerStatus() modelpolicy.CircuitBreakerStatusInfo
	}

	rp, ok := s.executor.provider.(circuitBreakerReporter)
	if !ok {
		httpx.JSON(w, http.StatusOK, map[string]any{"state": "not_configured", "correlation_id": httpx.CorrelationID(r)})
		return
	}

	status := rp.CircuitBreakerStatus()
	httpx.JSON(w, http.StatusOK, map[string]any{
		"state":            status.State,
		"failures":         status.Failures,
		"last_failure":     status.LastFailure,
		"recovery_timeout": status.RecoveryTimeout.String(),
		"correlation_id":   httpx.CorrelationID(r),
	})
}

// handleAdminBackupCheck delegates to admin.BackupCheckHandler.
func (s *Server) handleAdminBackupCheck(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	if !hasScope(ac.Scopes, "tenants:admin") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "admin scope required", httpx.CorrelationID(r), false)
		return
	}
	if s.backupHandler == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "backup check requires postgres backend", httpx.CorrelationID(r), false)
		return
	}
	s.backupHandler(w, r)
}

// handleAdminPurge delegates to lifecycle.AdminPurgeHandler.
func (s *Server) handleAdminPurge(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	if !hasScope(ac.Scopes, "tenants:admin") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "admin scope required", httpx.CorrelationID(r), false)
		return
	}
	if s.purgeHandler == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "purge requires postgres backend", httpx.CorrelationID(r), false)
		return
	}
	s.purgeHandler(w, r)
}

// handleAdminPIIScan delegates to compliance.PIIScanHandler.
func (s *Server) handleAdminPIIScan(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	if !hasScope(ac.Scopes, "tenants:admin") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "admin scope required", httpx.CorrelationID(r), false)
		return
	}
	if s.piiScanHandler == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "pii scan requires event log", httpx.CorrelationID(r), false)
		return
	}
	s.piiScanHandler.ServeHTTP(w, r)
}

// handleUsage delegates to usage.UsageHandler.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, "developer") {
		return
	}
	if s.usageHandler == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "usage reporting requires postgres backend", httpx.CorrelationID(r), false)
		return
	}
	s.usageHandler.ServeHTTP(w, r)
}

// handleUsageExport delegates to usage.ExportHandler.
func (s *Server) handleUsageExport(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	if !hasScope(ac.Scopes, "tenants:admin") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "admin scope required", httpx.CorrelationID(r), false)
		return
	}
	if s.usageExportHandler == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "usage export requires postgres backend", httpx.CorrelationID(r), false)
		return
	}
	s.usageExportHandler.ServeHTTP(w, r)
}

// handleDataExport delegates to compliance.ExportHandler.
func (s *Server) handleDataExport(w http.ResponseWriter, r *http.Request) {
	if s.exportHandler == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "data export not configured", httpx.CorrelationID(r), false)
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.exportHandler.HandleCreate(w, r)
	case http.MethodGet:
		s.exportHandler.HandleStatus(w, r)
	default:
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST or GET only", httpx.CorrelationID(r), false)
	}
}

// handleDataDelete delegates to compliance.DeleteHandler.
func (s *Server) handleDataDelete(w http.ResponseWriter, r *http.Request) {
	if s.deleteHandler == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "data delete not configured", httpx.CorrelationID(r), false)
		return
	}
	s.deleteHandler.ServeHTTP(w, r)
}

// handleMembers handles POST/DELETE /v1/tenants/members.
func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	ac, _ := auth.Get(r.Context())
	tenantID, ok := resolveTenant(w, r, ac)
	if !ok {
		return
	}
	if !s.requireRole(w, r, "owner") {
		return
	}
	if s.pgMemberStore == nil {
		httpx.Error(w, http.StatusNotImplemented, "not_implemented", "member management requires postgres backend", httpx.CorrelationID(r), false)
		return
	}

	// Extract member ID from path for DELETE /v1/tenants/members/{id}
	path := strings.TrimPrefix(r.URL.Path, "/v1/tenants/members")
	path = strings.Trim(path, "/")

	switch r.Method {
	case http.MethodPost:
		var req struct {
			PrincipalID string `json:"principal_id"`
			Role        string `json:"role"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid request body", httpx.CorrelationID(r), false)
			return
		}
		if req.PrincipalID == "" || req.Role == "" {
			httpx.Error(w, http.StatusBadRequest, "missing_field", "principal_id and role are required", httpx.CorrelationID(r), false)
			return
		}
		switch req.Role {
		case "owner", "admin", "developer", "viewer":
			// valid
		default:
			httpx.Error(w, http.StatusBadRequest, "invalid_role", "role must be one of: owner, admin, developer, viewer", httpx.CorrelationID(r), false)
			return
		}
		if err := s.pgMemberStore.SetRole(r.Context(), tenantID, req.PrincipalID, req.Role); err != nil {
			slog.Error("failed to add member", "error", err, "tenant_id", tenantID)
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to add member", httpx.CorrelationID(r), true)
			return
		}
		s.audit.Log(audit.Entry{
			TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "members.add",
			Resource: "member/" + req.PrincipalID, Outcome: "allowed",
			CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		})
		httpx.JSON(w, http.StatusCreated, map[string]string{
			"tenant_id": tenantID, "principal_id": req.PrincipalID, "role": req.Role,
		})
	case http.MethodDelete:
		if path == "" {
			httpx.Error(w, http.StatusBadRequest, "missing_member_id", "member principal_id is required in path", httpx.CorrelationID(r), false)
			return
		}
		if err := s.pgMemberStore.DeleteMember(r.Context(), tenantID, path); err != nil {
			if errors.Is(err, postgres.ErrMemberNotFound) {
				httpx.Error(w, http.StatusNotFound, "not_found", "member not found", httpx.CorrelationID(r), false)
				return
			}
			slog.Error("failed to remove member", "error", err, "tenant_id", tenantID)
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to remove member", httpx.CorrelationID(r), true)
			return
		}
		s.audit.Log(audit.Entry{
			TenantID: tenantID, PrincipalID: ac.PrincipalID, Action: "members.remove",
			Resource: "member/" + path, Outcome: "allowed",
			CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		})
		httpx.JSON(w, http.StatusOK, map[string]string{"deleted": path})
	default:
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST or DELETE only", httpx.CorrelationID(r), false)
	}
}
