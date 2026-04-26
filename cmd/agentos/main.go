package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq" // Postgres driver

	agentorchestrator "github.com/NexixAI/nexixai-agentos/agentorchestrator"
	"github.com/NexixAI/nexixai-agentos/federation"
	"github.com/NexixAI/nexixai-agentos/internal/agents"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/governance"
	"github.com/NexixAI/nexixai-agentos/internal/deploy"
	"github.com/NexixAI/nexixai-agentos/internal/httpfetch"
	"github.com/NexixAI/nexixai-agentos/internal/kbclient"
	"github.com/NexixAI/nexixai-agentos/internal/logging"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/sandbox"
	"github.com/NexixAI/nexixai-agentos/internal/storage"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
	"github.com/NexixAI/nexixai-agentos/internal/telemetry"
	"github.com/NexixAI/nexixai-agentos/internal/tlsconfig"
	"github.com/NexixAI/nexixai-agentos/mcp"
	"github.com/NexixAI/nexixai-agentos/mcp/facets"
	mcptools "github.com/NexixAI/nexixai-agentos/mcp/tools"
	modelpolicy "github.com/NexixAI/nexixai-agentos/modelpolicy"
)

const version = "0.0.1-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "up":
		up(os.Args[2:])
	case "redeploy":
		redeploy(os.Args[2:])
	case "validate":
		validate(os.Args[2:])
	case "status":
		status(os.Args[2:])
	case "nuke":
		nuke(os.Args[2:])
	case "tenants":
		tenantsCmd(os.Args[2:])
	case "bootstrap":
		bootstrap(os.Args[2:])
	case "migrate-storage":
		migrateStorage(os.Args[2:])
	case "version":
		slog.Info("version", "version", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`agentos (Phase 7 multi-tenancy scaffold)
Usage:
  agentos serve <agent-orchestrator|model-policy|federation> [--addr :PORT]
  agentos up [--compose-file PATH] [--project NAME] [--tenant TENANT] [--principal PRINCIPAL]
  agentos redeploy [--compose-file PATH] [--project NAME]
  agentos validate [--agent-orchestrator URL] [--model-policy URL] [--federation URL] [--tenant TENANT] [--principal PRINCIPAL]
  agentos status [--compose-file PATH] [--project NAME]
  agentos nuke [--compose-file PATH] [--project NAME] [--hard]
  agentos bootstrap --tenant-id TENANT_ID [--name NAME] [--key-name KEY_NAME]
  agentos tenants list [--agent-orchestrator URL]
  agentos tenants create --id TENANT_ID [--name NAME] [--plan PLAN] [--agent-orchestrator URL]
  agentos migrate-storage [--from file] [--to postgres] [--dry-run]

Defaults:
  compose file: deploy/local/compose.yaml
  endpoints:
    agent-orchestrator:     http://localhost:50081
    model-policy:           http://localhost:50082
    federation:             http://localhost:50083
`)
}

func repoRoot() string {
	cwd, _ := os.Getwd()
	return cwd
}

func defaultComposeFile() string {
	return filepath.Join(repoRoot(), "deploy", "local", "compose.yaml")
}

func newRunner(composeFile, project string) deploy.ComposeRunner {
	return deploy.ComposeRunner{
		ComposeFile: composeFile,
		ProjectName: project,
		Stdout:      func(s string) { slog.Info(s) },
		Stderr:      func(s string) { slog.Info(s) },
	}
}

func serve(args []string) {
	if err := config.EnsureSafeProfile(); err != nil {
		slog.Error("refusing to start in prod", "error", err)
		os.Exit(1)
	}

	cfg := config.LoadFromEnv()

	// Pre-initialize Prometheus Vec metrics so they appear in /metrics immediately.
	metrics.InitMetrics()

	// Initialize OpenTelemetry tracing.
	tracerCfg := telemetry.LoadTracerConfigFromEnv()
	tracerShutdown, err := telemetry.InitTracer(tracerCfg)
	if err != nil {
		slog.Error("failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := tracerShutdown(shutdownCtx); err != nil {
			slog.Error("tracer shutdown error", "error", err)
		}
	}()

	// Initialize structured logger.
	logger := logging.InitLogger(cfg.LogFormat)

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8081", "listen address (host:port)")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) < 1 {
		usage()
		os.Exit(2)
	}
	target := strings.ToLower(rest[0])

	logger.Info("starting service", "target", target, "addr", *addr)

	if err := config.ValidateServiceConfig(target); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	type shutdowner interface {
		Shutdown(ctx context.Context) error
	}

	var shutdown shutdowner
	var listenAndServe func() error
	var mcpServer *mcp.Server // non-nil when MCP is enabled

	// Load TLS configuration for agent-orchestrator and model-policy.
	tlsCfg, tlsCert, tlsKey, tlsErr := tlsconfig.LoadFromEnv()
	if tlsErr != nil {
		slog.Error("TLS configuration error", "error", tlsErr)
		os.Exit(1)
	}
	if tlsCfg != nil {
		logger.Info("TLS enabled", "cert", tlsCert, "key", tlsKey)
	}

	switch target {
	case "agent-orchestrator":
		// v11.2: resolve the default model id at startup (env override > backend /v1/models).
		// v11.3: plumb through ServerOption + share the resolved value with MCP
		//         so MCP tools see the same default without env-mutation glue.
		resolveCtx, resolveCancel := context.WithTimeout(context.Background(), 35*time.Second)
		resolvedModel, resolveErr := modelpolicy.ResolveDefaultModel(resolveCtx, config.LoadModelConfig(), logger)
		resolveCancel()
		if resolveErr != nil {
			slog.Error("default model resolution failed", "error", resolveErr)
			os.Exit(1)
		}

		var serverOpts []agentorchestrator.ServerOption
		if resolvedModel != "" {
			serverOpts = append(serverOpts, agentorchestrator.WithServerDefaultModel(resolvedModel))
		}
		sp, err := agentorchestrator.NewServer(*addr, version, serverOpts...)
		if err != nil {
			slog.Error("failed to create agent-orchestrator server", "error", err)
			os.Exit(1)
		}
		shutdown = sp
		if tlsCfg != nil {
			sp.HTTP.TLSConfig = tlsCfg
			listenAndServe = func() error { return sp.HTTP.ListenAndServeTLS(tlsCert, tlsKey) }
		} else {
			listenAndServe = sp.HTTP.ListenAndServe
		}

		// Start MCP server if enabled.
		mcpCfg := config.LoadMCPConfig()
		if mcpCfg.Enabled {
			registry := mcp.NewToolRegistry()

			// Register ALL MCP tools.
			modelCfg := config.LoadModelConfig()
			// v11.3: reuse the startup-resolved default so MCP tools and the
			// HTTP handler stay consistent. Env-only callers (AGENTOS_MODEL_DEFAULT
			// set explicitly) will have resolvedModel == their env value per
			// ResolveDefaultModel's precedence rule.
			if resolvedModel != "" {
				modelCfg.DefaultModel = resolvedModel
			}
			routerCfg := agentorchestrator.LoadRouterConfig()

			// Build intent-based Composer for MCP chat tools (shared with HTTP handler via server.go).
			var mcpComposer *agentorchestrator.Composer
			intentPath := envOr("AGENTOS_INTENT_CONFIG_PATH", "/etc/agentos/intent-router.yml")
			if routerCfg.Enabled {
				if _, statErr := os.Stat(intentPath); statErr == nil {
					icfg, icfgErr := agentorchestrator.LoadIntentConfig(intentPath)
					if icfgErr != nil {
						slog.Error("mcp: intent config failed", "error", icfgErr)
						os.Exit(1)
					}
					kbURL := os.Getenv("AGENTOS_KB_URL")
					kbKey := os.Getenv("AGENTOS_KB_API_KEY")
					var kbc *kbclient.KBClient
					if kbURL != "" {
						kbc = kbclient.NewKBClient(kbURL, kbKey)
					}
					mcpComposer = agentorchestrator.NewComposer(icfg, kbc)
					slog.Info("mcp: intent router loaded for chat tools", "version", icfg.Version)
				}
			}

			// Chat tools: chat_completion, list_models, get_active_model, swap_model, get_routing_config, set_thinking_mode
			if err := mcptools.RegisterChatTools(registry, mcptools.ChatToolsConfig{
				ModelConfig:  modelCfg,
				RouterConfig: routerCfg,
				Composer:     mcpComposer,
			}); err != nil {
				slog.Error("mcp: failed to register chat tools", "error", err)
				os.Exit(1)
			}

			// Health tools: get_health
			if err := mcptools.RegisterHealthTools(registry, modelCfg.BaseURL, routerCfg.ClassifierURL); err != nil {
				slog.Error("mcp: failed to register health tools", "error", err)
				os.Exit(1)
			}

			// Code generation tool: generate_implementation
			repoBase := envOr("AGENTOS_REPO_BASE", "/workspace")
			if err := mcptools.RegisterCodegenTools(registry, mcptools.CodegenConfig{
				ModelConfig: modelCfg,
				RepoBase:    repoBase,
			}); err != nil {
				slog.Error("mcp: failed to register codegen tools", "error", err)
				os.Exit(1)
			}

			// Shared audit store — log_decision writes, get_audit_log reads
			auditStore := mcptools.NewInMemoryAuditStore(10000)

			// Governance tools: check_authorization, log_decision, request_elevation, check_elevation_status
			//
			// In-memory clearance store: unknown agents default to ClearanceInternal (T1),
			// which grants access to observation and controlled-action tools but not
			// execute/admin tools. Production should use PostgresClearanceStore with
			// explicit per-agent tiers.
			clearanceStore := mcp.NewInMemoryClearanceStore(mcp.WithDefaultTier(mcp.ClearanceInternal))

			// Seed agent tiers from governance.yml if available.
			// Maps governance clearance strings to MCP ClearanceTier values.
			govPath := envOr("AGENTOS_GOVERNANCE_PATH", "/etc/agentos/governance.yml")
			if govCfg, govErr := governance.LoadConfig(govPath); govErr == nil {
				agentTiers := make(map[string]mcp.ClearanceTier)
				for name, agent := range govCfg.Agents {
					switch agent.Clearance {
					case "execute":
						agentTiers[name] = mcp.ClearanceExecute
					case "admin":
						agentTiers[name] = mcp.ClearanceAdmin
					case "internal":
						agentTiers[name] = mcp.ClearanceInternal
					default:
						agentTiers[name] = mcp.ClearanceInternal
					}
				}
				for name, tier := range agentTiers {
					_ = clearanceStore.SetClearance(name, tier)
				}
				slog.Info("mcp: seeded agent clearance tiers from governance.yml", "agents", len(agentTiers))
			}

			elevationStore := postgres.NewInMemoryElevationStore()
			if err := mcptools.RegisterGovernanceTools(registry, clearanceStore, auditStore, elevationStore); err != nil {
				slog.Error("mcp: failed to register governance tools", "error", err)
				os.Exit(1)
			}

			// Sandbox tools: execute_code, execute_shell
			sandboxExecutor := sandbox.NewDockerExecutor("/var/run/docker.sock")
			if err := mcptools.RegisterSandboxTools(registry, sandboxExecutor); err != nil {
				slog.Error("mcp: failed to register sandbox tools", "error", err)
				os.Exit(1)
			}

			// HTTP tools: fetch_url, http_request, ping_url
			fetcher := httpfetch.NewHTTPFetcher()
			if err := mcptools.RegisterHTTPTools(registry, fetcher); err != nil {
				slog.Error("mcp: failed to register http tools", "error", err)
				os.Exit(1)
			}

			// Memory tools: memory_store, memory_recall, get_thread
			// Connect to Postgres for MCP memory tools.
			mcpDSN := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
				envOr("AGENTOS_DB_HOST", "localhost"),
				envOr("AGENTOS_DB_PORT", "5433"),
				envOr("AGENTOS_DB_NAME", "agentos"),
				envOr("AGENTOS_DB_USER", "agentos"),
				envOr("AGENTOS_DB_PASSWORD", ""),
				envOr("AGENTOS_DB_SSLMODE", "disable"),
			)
			mcpDB, err := sql.Open("postgres", mcpDSN)
			var memoryStore postgres.AgentMemoryStore
			if err != nil {
				slog.Warn("mcp: postgres unavailable — memory tools will fail", "error", err)
			} else if pingErr := mcpDB.Ping(); pingErr != nil {
				slog.Warn("mcp: postgres ping failed — memory tools will fail", "error", pingErr)
			} else {
				memoryStore = postgres.NewPostgresAgentMemoryStore(mcpDB, 10000)
				slog.Info("mcp: memory tools wired to Postgres")
			}
			if err := mcptools.RegisterMemoryTools(registry, memoryStore); err != nil {
				slog.Error("mcp: failed to register memory tools", "error", err)
				os.Exit(1)
			}

			// Knowledge tools: index_document, search_knowledge
			kbCfg := config.LoadKBConfig()
			knowledgeStore := kbclient.NewKBClient(kbCfg.URL, kbCfg.APIKey)
			slog.Info("mcp: knowledge store wired to KB service", "url", kbCfg.URL)
			if err := mcptools.RegisterKnowledgeTools(registry, knowledgeStore); err != nil {
				slog.Error("mcp: failed to register knowledge tools", "error", err)
				os.Exit(1)
			}

			// Observe tools: query_metrics, get_alerts, get_container_status, get_audit_log
			portainerURL := os.Getenv("PORTAINER_API_URL")
			if portainerURL == "" {
				portainerURL = "https://localhost:9443/api"
			}
			portainerKey := os.Getenv("PORTAINER_API_KEY")
			promClient := metrics.NewHTTPPrometheusClient("") // uses AGENTOS_PROMETHEUS_URL or defaults to localhost:9090
			if err := mcptools.RegisterObserveTools(registry, mcptools.ObserveDeps{
				PromClient:   promClient,
				PortainerURL: portainerURL,
				PortainerKey: portainerKey,
				AuditStore:   auditStore,
			}); err != nil {
				slog.Error("mcp: failed to register observe tools", "error", err)
				os.Exit(1)
			}

			// Facet tools: classify_prompt, list_facets, register_facet, unregister_facet
			facetRegistry := facets.NewFacetRegistry()
			if err := mcptools.RegisterFacetTools(registry, facetRegistry, routerCfg.ClassifierURL); err != nil {
				slog.Error("mcp: failed to register facet tools", "error", err)
				os.Exit(1)
			}

			// Dynamic tools: discover_tools, get_tool_schema, invoke_tool, list_capabilities
			if err := mcptools.RegisterDynamicTools(registry, clearanceStore); err != nil {
				slog.Error("mcp: failed to register dynamic tools", "error", err)
				os.Exit(1)
			}

			// Agent tools: spawn_agent, message_agent, get_agent_status
			agentManager := agents.NewInMemoryAgentManager(10)
			if err := mcptools.RegisterAgentTools(registry, agentManager); err != nil {
				slog.Error("mcp: failed to register agent tools", "error", err)
				os.Exit(1)
			}

			// Server uptime tool: server_uptime
			if err := mcptools.RegisterServerUptimeTool(registry); err != nil {
				slog.Error("mcp: failed to register server uptime tool", "error", err)
				os.Exit(1)
			}

			// List env tool: list_env
			if err := mcptools.RegisterListenvTool(registry); err != nil {
				slog.Error("mcp: failed to register list_env tool", "error", err)
				os.Exit(1)
			}

			// Web search tool: web_search (requires SearXNG on SEARXNG_URL)
			if err := mcptools.RegisterWebSearchTool(registry); err != nil {
				slog.Error("mcp: failed to register web_search tool", "error", err)
				os.Exit(1)
			}

			slog.Info("mcp: all tools registered", "count", len(registry.List()))

			// Load governance engine (optional — if governance.yml exists)
			governancePath := envOr("AGENTOS_GOVERNANCE_PATH", "/etc/agentos/governance.yml")
			var govEngine *governance.Engine
			if _, statErr := os.Stat(governancePath); statErr == nil {
				var govErr error
				govEngine, govErr = governance.NewEngine(governancePath)
				if govErr != nil {
					slog.Warn("mcp: governance engine failed to load — running without governance", "error", govErr, "path", governancePath)
				} else {
					slog.Info("mcp: governance engine loaded", "path", governancePath, "agents", len(govEngine.Config().Agents))
				}
			} else {
				slog.Info("mcp: governance.yml not found — running without governance engine", "path", governancePath)
			}

			mcpAddr := fmt.Sprintf(":%d", mcpCfg.Port)
			mcpServer = mcp.NewServer(mcpAddr, version, registry, clearanceStore, govEngine)
			slog.Info("mcp: enabled", "addr", mcpAddr, "governance", govEngine != nil)
		}
	case "model-policy":
		srv := modelpolicy.NewServer(*addr, version)
		shutdown = srv
		if tlsCfg != nil {
			srv.TLSConfig = tlsCfg
			listenAndServe = func() error { return srv.ListenAndServeTLS(tlsCert, tlsKey) }
		} else {
			listenAndServe = srv.ListenAndServe
		}
	case "federation":
		srv := federation.NewServer(*addr, version)
		shutdown = srv
		useTLS := srv.TLSConfig != nil
		if useTLS {
			serverCertPath := strings.TrimSpace(os.Getenv("AGENTOS_FED_SERVER_CERT"))
			serverKeyPath := strings.TrimSpace(os.Getenv("AGENTOS_FED_SERVER_KEY"))
			listenAndServe = func() error { return srv.ListenAndServeTLS(serverCertPath, serverKeyPath) }
		} else {
			listenAndServe = srv.ListenAndServe
		}
	default:
		slog.Error("unknown target", "target", target)
		os.Exit(1)
	}

	slog.Info("serving", "target", target, "addr", *addr)

	errCh := make(chan error, 2)
	go func() {
		errCh <- listenAndServe()
	}()

	// Start MCP server in a separate goroutine if enabled.
	if mcpServer != nil {
		go func() {
			if err := mcpServer.ListenAndServe(); err != nil {
				errCh <- fmt.Errorf("mcp server: %w", err)
			}
		}()
	}

	// Wait for interrupt signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		slog.Error("server error", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// Shut down MCP server first if running.
	if mcpServer != nil {
		if err := mcpServer.Shutdown(ctx); err != nil {
			slog.Error("mcp graceful shutdown error", "error", err)
		}
	}

	if err := shutdown.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown error", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped cleanly")
	os.Exit(0)
}

func up(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	composeFile := fs.String("compose-file", defaultComposeFile(), "compose file path")
	project := fs.String("project", "agentos", "compose project name")
	tenant := fs.String("tenant", "tnt_demo", "tenant id used for validation")
	principal := fs.String("principal", "prn_local", "principal id used for validation")
	_ = fs.Parse(args)

	slog.Info("platform", "os", runtime.GOOS, "arch", runtime.GOARCH)
	r := newRunner(*composeFile, *project)

	reportsDir, err := deploy.EnsureReportsDir(repoRoot())
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}

	slog.Info("up: starting containers (build on first run)")
	if err := r.Up(true); err != nil {
		slog.Error("up failed", "error", err)
		os.Exit(1)
	}

	v := deploy.Validator{
		AgentOrchestrator: "http://localhost:50081",
		ModelPolicy:       "http://localhost:50082",
		Fed:               "http://localhost:50083",
		RepoRoot:          repoRoot(),
		TenantID:          *tenant,
		PrincipalID:       *principal,
	}
	slog.Info("up: validating")
	checks, verr := v.ValidateAll()

	sum := deploy.Summary{
		Mode: "up",
		Endpoints: map[string]string{
			"agent-orchestrator": "http://localhost:50081",
			"model-policy":       "http://localhost:50082",
			"federation":         "http://localhost:50083",
		},
		Checks: checks,
	}
	if err := deploy.WriteSummary(reportsDir, sum); err != nil {
		slog.Warn("failed to write summary", "error", err)
	}

	if verr != nil {
		slog.Error("validation failed", "summary", reportsDir, "error", verr)
		os.Exit(1)
	}

	slog.Info("up complete", "summary", reportsDir)
	printAccess(sum.Endpoints)
}

func redeploy(args []string) {
	fs := flag.NewFlagSet("redeploy", flag.ExitOnError)
	composeFile := fs.String("compose-file", defaultComposeFile(), "compose file path")
	project := fs.String("project", "agentos", "compose project name")
	_ = fs.Parse(args)

	r := newRunner(*composeFile, *project)
	slog.Info("redeploy: rebuilding + restarting")
	if err := r.Up(true); err != nil {
		slog.Error("redeploy failed", "error", err)
		os.Exit(1)
	}
	slog.Info("redeploy complete")
}

func validate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	agentOrchestrator := fs.String("agent-orchestrator", "http://localhost:50081", "Agent Orchestrator base URL")
	modelPolicy := fs.String("model-policy", "http://localhost:50082", "Model Policy base URL")
	fed := fs.String("federation", "http://localhost:50083", "Federation base URL")
	tenant := fs.String("tenant", "tnt_demo", "tenant id")
	principal := fs.String("principal", "prn_local", "principal id")
	_ = fs.Parse(args)

	reportsDir, err := deploy.EnsureReportsDir(repoRoot())
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}

	v := deploy.Validator{
		AgentOrchestrator: *agentOrchestrator,
		ModelPolicy:       *modelPolicy,
		Fed:               *fed,
		RepoRoot:          repoRoot(),
		TenantID:          *tenant,
		PrincipalID:       *principal,
	}
	checks, verr := v.ValidateAll()

	sum := deploy.Summary{
		Mode: "validate",
		Endpoints: map[string]string{
			"agent-orchestrator": *agentOrchestrator,
			"model-policy":       *modelPolicy,
			"federation":         *fed,
		},
		Checks: checks,
	}
	if err := deploy.WriteSummary(reportsDir, sum); err != nil {
		slog.Warn("failed to write summary", "error", err)
	}

	if verr != nil {
		slog.Error("validate failed", "summary", reportsDir)
		os.Exit(1)
	}
	slog.Info("validate ok", "summary", reportsDir)
}

func status(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	composeFile := fs.String("compose-file", defaultComposeFile(), "compose file path")
	project := fs.String("project", "agentos", "compose project name")
	_ = fs.Parse(args)

	r := newRunner(*composeFile, *project)
	_ = r.Ps()
	printAccess(map[string]string{
		"agent-orchestrator": "http://localhost:50081",
		"model-policy":       "http://localhost:50082",
		"federation":         "http://localhost:50083",
	})
}

func nuke(args []string) {
	fs := flag.NewFlagSet("nuke", flag.ExitOnError)
	composeFile := fs.String("compose-file", defaultComposeFile(), "compose file path")
	project := fs.String("project", "agentos", "compose project name")
	hard := fs.Bool("hard", false, "remove volumes as well (destructive)")
	_ = fs.Parse(args)

	r := newRunner(*composeFile, *project)
	slog.Info("nuke: stopping")
	if err := r.Down(*hard); err != nil {
		slog.Error("nuke failed", "error", err)
		os.Exit(1)
	}
	slog.Info("nuke complete")
}

func tenantsCmd(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch strings.ToLower(args[0]) {
	case "list":
		tenantsList(args[1:])
	case "create":
		tenantsCreate(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

func tenantsList(args []string) {
	fs := flag.NewFlagSet("tenants list", flag.ExitOnError)
	agentOrchestrator := fs.String("agent-orchestrator", "http://localhost:50081", "Agent Orchestrator base URL")
	_ = fs.Parse(args)

	url := strings.TrimRight(*agentOrchestrator, "/") + "/v1/admin/tenants"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		slog.Error("failed to create request", "error", err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("list tenants failed", "error", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("failed to read response body", "error", err)
		return
	}
	slog.Info(string(body))
}

func tenantsCreate(args []string) {
	fs := flag.NewFlagSet("tenants create", flag.ExitOnError)
	agentOrchestrator := fs.String("agent-orchestrator", "http://localhost:50081", "Agent Orchestrator base URL")
	id := fs.String("id", "", "tenant id (required)")
	name := fs.String("name", "", "tenant name")
	plan := fs.String("plan", "", "plan tier")
	_ = fs.Parse(args)

	if strings.TrimSpace(*id) == "" {
		slog.Error("tenant id required")
		os.Exit(1)
	}

	payload := map[string]any{
		"tenant_id": *id,
	}
	if *name != "" {
		payload["name"] = *name
	}
	if *plan != "" {
		payload["plan_tier"] = *plan
	}
	b, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal payload", "error", err)
		return
	}
	url := strings.TrimRight(*agentOrchestrator, "/") + "/v1/admin/tenants"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		slog.Error("failed to create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("create tenant failed", "error", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("failed to read response body", "error", err)
		return
	}
	slog.Info(string(body))
}

func migrateStorage(args []string) {
	fs := flag.NewFlagSet("migrate-storage", flag.ExitOnError)
	from := fs.String("from", "file", "source backend (file)")
	to := fs.String("to", "postgres", "target backend (postgres)")
	dryRun := fs.Bool("dry-run", false, "log what would be migrated without writing")
	if err := fs.Parse(args); err != nil {
		slog.Error("failed to parse flags", "error", err)
		os.Exit(2)
	}

	if *from != "file" {
		slog.Error("unsupported source backend", "from", *from)
		os.Exit(1)
	}
	if *to != "postgres" {
		slog.Error("unsupported target backend", "to", *to)
		os.Exit(1)
	}

	cfg := config.LoadFromEnv()

	// Build file stores (source).
	agentDir := strings.TrimSpace(os.Getenv("AGENTOS_AGENT_STORE_DIR"))
	if agentDir == "" {
		agentDir = filepath.Join("data", "agents")
	}
	fileAgents, err := storage.NewFileAgentStore(agentDir)
	if err != nil {
		slog.Error("failed to create file agent store", "error", err)
		os.Exit(1)
	}

	runPath := strings.TrimSpace(os.Getenv("AGENTOS_RUN_STORE_FILE"))
	if runPath == "" {
		runPath = filepath.Join("data", "agent-orchestrator", "runs.json")
	}
	fileRuns, err := storage.NewFileRunStore(runPath)
	if err != nil {
		slog.Error("failed to create file run store", "error", err)
		os.Exit(1)
	}

	memoryDir := strings.TrimSpace(os.Getenv("AGENTOS_MEMORY_STORE_DIR"))
	if memoryDir == "" {
		memoryDir = filepath.Join("data", "memory")
	}
	fileMem, err := storage.NewFileMemoryStore(memoryDir)
	if err != nil {
		slog.Error("failed to create file memory store", "error", err)
		os.Exit(1)
	}

	kvDir := strings.TrimSpace(os.Getenv("AGENTOS_KV_STORE_DIR"))
	if kvDir == "" {
		kvDir = filepath.Join("data", "kv")
	}
	fileKV, err := storage.NewFileKVStore(kvDir, cfg.KVMaxValueSize, cfg.KVMaxKeysPerAgent)
	if err != nil {
		slog.Error("failed to create file kv store", "error", err)
		os.Exit(1)
	}

	eventDataDir := filepath.Join("data", "agent-orchestrator")
	fileEvents, err := storage.NewFileEventLogStore(eventDataDir)
	if err != nil {
		slog.Error("failed to create file event log store", "error", err)
		os.Exit(1)
	}

	// Build postgres stores (target).
	pgCfg := cfg
	pgCfg.Backend = "postgres"

	pgAgents, err := storage.NewAgentStore(pgCfg)
	if err != nil {
		slog.Error("failed to create postgres agent store", "error", err)
		os.Exit(1)
	}
	pgRuns, err := storage.NewRunStore(pgCfg)
	if err != nil {
		slog.Error("failed to create postgres run store", "error", err)
		os.Exit(1)
	}
	pgMem, err := storage.NewMemoryStore(pgCfg)
	if err != nil {
		slog.Error("failed to create postgres memory store", "error", err)
		os.Exit(1)
	}
	pgKV, err := storage.NewKVStore(pgCfg)
	if err != nil {
		slog.Error("failed to create postgres kv store", "error", err)
		os.Exit(1)
	}
	pgEvents, err := storage.NewEventLogStore(pgCfg, "")
	if err != nil {
		slog.Error("failed to create postgres event log store", "error", err)
		os.Exit(1)
	}

	fileStores := storage.MigratorStores{
		Agents: fileAgents,
		Runs:   fileRuns,
		Mem:    fileMem,
		KV:     fileKV,
		Events: fileEvents,
	}
	pgStores := storage.MigratorStores{
		Agents: pgAgents,
		Runs:   pgRuns,
		Mem:    pgMem,
		KV:     pgKV,
		Events: pgEvents,
	}
	paths := storage.MigratorFilePaths{
		AgentDir:  agentDir,
		RunPath:   runPath,
		MemoryDir: memoryDir,
		KVDir:     kvDir,
		EventDir:  filepath.Join(eventDataDir, "event_log"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	slog.Info("migrate-storage: starting", "from", *from, "to", *to, "dry_run", *dryRun)
	migrator := storage.NewStorageMigrator(fileStores, pgStores, paths, *dryRun)
	result, err := migrator.Migrate(ctx)
	if err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	slog.Info("migrate-storage: complete",
		"agents", result.Agents,
		"runs", result.Runs,
		"memory", result.Memory,
		"kv", result.KV,
		"events", result.Events,
		"skipped", result.Skipped,
		"errors", len(result.Errors),
	)
	for _, e := range result.Errors {
		slog.Warn("migration error", "detail", e)
	}

	if len(result.Errors) > 0 {
		os.Exit(1)
	}
}

func printAccess(endpoints map[string]string) {
	slog.Info("access")
	if v, ok := endpoints["agent-orchestrator"]; ok {
		slog.Info("Agent Orchestrator health", "url", v+"/v1/health")
	}
	if v, ok := endpoints["model-policy"]; ok {
		slog.Info("Model Policy health", "url", v+"/v1/health")
	}
	if v, ok := endpoints["federation"]; ok {
		slog.Info("Federation health", "url", v+"/v1/federation/health")
	}
}

// envOr returns the environment variable value or a default.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
