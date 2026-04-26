package federation

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/logging"
	"github.com/NexixAI/nexixai-agentos/internal/auth"
	healthpkg "github.com/NexixAI/nexixai-agentos/internal/health"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/middleware"
)

type Server struct {
	version string

	registry    *Registry
	forward     *Forwarder
	proxy       *SSEProxy
	index       *forwardIndex
	events      *eventStore
	jwtVerifier *JWTVerifier

	audit  audit.Logger
	health *healthpkg.Checker
}

func New(version string) *Server {
	reg, _ := LoadRegistryFromEnv()
	idxPath := os.Getenv("AGENTOS_FED_FORWARD_INDEX_FILE")
	if idxPath == "" {
		idxPath = "data/federation/forward-index.json"
	}
	hc := healthpkg.NewChecker(version)
	jwtV, err := NewJWTVerifier()
	if err != nil {
		slog.Warn("Federation JWT verifier not available — requests will be rejected unless AGENTOS_FED_AUTH_DISABLED=1 is set", "error", err)
	}
	return &Server{
		version:     version,
		registry:    reg,
		forward:     NewForwarder(),
		proxy:       NewSSEProxy(),
		index:       newForwardIndexPersistent(idxPath),
		events:      newEventStore(),
		audit:       audit.NewFromEnv(),
		jwtVerifier: jwtV,
		health:      hc,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Required by Federation OpenAPI
	mux.HandleFunc("/v1/federation/health", s.health.Handler())
	mux.HandleFunc("/v1/federation/peer", s.handlePeerInfo)
	mux.HandleFunc("/v1/federation/peer/capabilities", s.handlePeerCapabilities)
	mux.HandleFunc("/v1/federation/peers", s.handlePeers)
	mux.HandleFunc("/v1/federation/runs:forward", s.handleForwardRun)
	mux.HandleFunc("/v1/federation/runs/", s.handleRunEvents) // /v1/federation/runs/{run_id}/events
	mux.HandleFunc("/v1/federation/events:ingest", s.handleEventsIngest)
	mux.Handle("/metrics", middleware.ProtectMetrics(metrics.Handler()))

	h := middleware.WithAuth(mux)
	h = JWTMiddleware(s.jwtVerifier, h) // Add JWT verification
	h = middleware.EnsureRequestID(h)
	h = logging.RequestContextMiddleware(h)
	h = metrics.Instrument("federation", h)
	return h
}

func (s *Server) handlePeerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}
	if s.registry == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "peer registry not configured (AGENTOS_PEERS_FILE)", httpx.CorrelationID(r), true)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"peer": s.registry.Local})
}

func (s *Server) handlePeerCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}
	resp := map[string]any{
		"peer_id":  strings.TrimSpace(os.Getenv("AGENTOS_STACK_ID")),
		"protocol": "1.0",
		"capabilities": []string{
			"runs.forward",
			"events.ingest",
			"events.sse_proxy",
		},
		"event_backhaul": map[string]any{"mode": "sse_proxy"},
	}
	if resp["peer_id"] == "" {
		resp["peer_id"] = "stk_local"
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleForwardRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}
	if s.registry == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "peer registry not configured (AGENTOS_PEERS_FILE)", httpx.CorrelationID(r), true)
		return
	}

	ac, _ := auth.Get(r.Context())
	tenantHdr, err := auth.RequireTenant(ac)
	if err != nil {
		if errors.Is(err, auth.ErrTenantMismatch) {
			httpx.Error(w, http.StatusBadRequest, "tenant_mismatch", err.Error(), httpx.CorrelationID(r), false)
			return
		}
		tenantHdr = "" // allow payload to supply tenant when header/default missing
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}

	forwardObj, _ := req["forward"].(map[string]any)
	if forwardObj == nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "missing forward object", httpx.CorrelationID(r), false)
		return
	}

	selector, _ := forwardObj["target_selector"].(map[string]any)
	authObj, _ := forwardObj["auth"].(map[string]any)
	runReq, _ := forwardObj["run_request"].(map[string]any)
	if selector == nil || authObj == nil || runReq == nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "missing required forward fields", httpx.CorrelationID(r), false)
		return
	}

	targetStackID, _ := selector["stack_id"].(string)
	if targetStackID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "target_selector.stack_id required", httpx.CorrelationID(r), false)
		return
	}

	tenantPayload, _ := authObj["tenant_id"].(string)
	principalPayload, _ := authObj["principal_id"].(string)

	tenantID := tenantPayload
	if tenantID == "" {
		tenantID = tenantHdr
	}
	if tenantID == "" {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "tenant_id required", httpx.CorrelationID(r), false)
		return
	}
	if tenantHdr != "" && tenantPayload != "" && strings.TrimSpace(tenantHdr) != strings.TrimSpace(tenantPayload) {
		httpx.Error(w, http.StatusBadRequest, "tenant_mismatch", "tenant_id mismatch between header and payload auth", httpx.CorrelationID(r), false)
		return
	}

	peer, ok := s.registry.Get(targetStackID)
	if !ok {
		httpx.Error(w, http.StatusNotFound, "peer_not_found", "target peer not found", httpx.CorrelationID(r), false)
		return
	}

	agentID, _ := runReq["agent_id"].(string)
	if agentID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "run_request.agent_id required", httpx.CorrelationID(r), false)
		return
	}

	runCreate := map[string]any{
		"input":           runReq["input"],
		"context":         runReq["context"],
		"tooling":         runReq["tooling"],
		"run_options":     runReq["run_options"],
		"idempotency_key": runReq["idempotency_key"],
	}

	bearer := bearerToken(r.Header.Get("Authorization"))

	remoteRunID, remoteEventsURL, status, err := s.forward.ForwardRun(peer.Endpoints.AgentOrchestratorBaseURL, agentID, tenantID, principalPayload, bearer, runCreate)
	if err != nil {
		metrics.IncFederationForwardFailure("federation", "forward_run_failed")
		httpx.Error(w, http.StatusBadGateway, "forward_failed", err.Error(), httpx.CorrelationID(r), true)
		return
	}

	s.index.Set(tenantID, remoteRunID, peer.StackID, remoteEventsURL)

	resp := map[string]any{
		"forwarded": map[string]any{
			"tenant_id":         tenantID,
			"remote_stack_id":   peer.StackID,
			"remote_run_id":     remoteRunID,
			"remote_events_url": remoteEventsURL,
			"status":            status,
		},
		"correlation_id": httpx.CorrelationID(r),
	}

	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: principalPayload, Action: "federation.runs.forward", Resource: "run/" + remoteRunID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		Meta: map[string]any{"target_stack_id": peer.StackID},
	})

	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleEventsIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	ac, _ := auth.Get(r.Context())
	tenantHdr, err := auth.RequireTenant(ac)
	if err != nil {
		if errors.Is(err, auth.ErrTenantMismatch) {
			httpx.Error(w, http.StatusBadRequest, "tenant_mismatch", err.Error(), httpx.CorrelationID(r), false)
			return
		}
		tenantHdr = ""
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}

	peerID, _ := req["peer_id"].(string)
	authObj, _ := req["auth"].(map[string]any)
	eventsArr, _ := req["events"].([]any)
	if authObj == nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "missing auth", httpx.CorrelationID(r), false)
		return
	}

	tenantPayload, _ := authObj["tenant_id"].(string)
	principalPayload, _ := authObj["principal_id"].(string)

	tenantID := tenantPayload
	if tenantID == "" {
		tenantID = tenantHdr
	}
	if tenantID == "" {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "tenant_id required", httpx.CorrelationID(r), false)
		return
	}
	if tenantHdr != "" && tenantPayload != "" && strings.TrimSpace(tenantHdr) != strings.TrimSpace(tenantPayload) {
		httpx.Error(w, http.StatusBadRequest, "tenant_mismatch", "tenant_id mismatch between header and payload auth", httpx.CorrelationID(r), false)
		return
	}

	acceptedTotal := 0
	rejectedTotal := 0

	for _, ev := range eventsArr {
		env, ok := ev.(map[string]any)
		if !ok {
			rejectedTotal++
			continue
		}
		eventObj, _ := env["event"].(map[string]any)
		runID, _ := eventObj["run_id"].(string)
		if runID == "" {
			rejectedTotal++
			continue
		}
		a, rj := s.events.Ingest(tenantID, runID, []map[string]any{env})
		acceptedTotal += a
		rejectedTotal += rj
	}

	resp := map[string]any{
		"accepted":       acceptedTotal,
		"rejected":       rejectedTotal,
		"correlation_id": httpx.CorrelationID(r),
	}

	s.audit.Log(audit.Entry{
		TenantID: tenantID, PrincipalID: principalPayload, Action: "federation.events.ingest", Resource: "peer/" + peerID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		Meta: map[string]any{"accepted": acceptedTotal, "rejected": rejectedTotal},
	})

	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}

	ac, _ := auth.Get(r.Context())
	tenantID, ok := resolveTenant(w, r, ac)
	if !ok {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/federation/runs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "events" {
		httpx.Error(w, http.StatusNotFound, "not_found", "not found", httpx.CorrelationID(r), false)
		return
	}
	runID := parts[0]

	fromSeq := 0
	if v := r.URL.Query().Get("from_sequence"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			fromSeq = parsed
		}
	}

	// Prefer SSE proxy if forwarded.
	if tgt, ok := s.index.Get(tenantID, runID); ok && tgt.RemoteEventsURL != "" {
		bearer := bearerToken(r.Header.Get("Authorization"))
		if err := s.proxy.Proxy(w, tgt.RemoteEventsURL, tenantID, ac.PrincipalID, bearer, fromSeq); err != nil {
			metrics.IncFederationForwardFailure("federation", "events_proxy_failed")
			httpx.Error(w, http.StatusBadGateway, "events_proxy_failed", err.Error(), httpx.CorrelationID(r), true)
		}
		return
	}

	// Otherwise, stream ingested events (push mode).
	if envs, ok := s.events.ListFromSequence(tenantID, runID, fromSeq); ok {
		if err := StreamStoredEvents(w, envs); err != nil {
			slog.Error("failed to stream stored events", "run_id", runID, "tenant_id", tenantID, "error", err)
		}
		return
	}

	httpx.Error(w, http.StatusNotFound, "not_found", "run not found", httpx.CorrelationID(r), false)
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", httpx.CorrelationID(r), false)
		return
	}
	if s.registry == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "peer registry not configured", httpx.CorrelationID(r), true)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var peer PeerInfo
	if err := json.NewDecoder(r.Body).Decode(&peer); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "invalid json body", httpx.CorrelationID(r), false)
		return
	}
	if peer.StackID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "stack_id is required", httpx.CorrelationID(r), false)
		return
	}

	s.registry.AddPeer(peer)

	slog.Info("peer registered via API", "stack_id", peer.StackID, "region", peer.Region)

	ac, _ := auth.Get(r.Context())
	s.audit.Log(audit.Entry{
		TenantID: "", PrincipalID: ac.PrincipalID, Action: "federation.peers.register", Resource: "peer/" + peer.StackID, Outcome: "allowed",
		CorrelationID: httpx.CorrelationID(r), RequestID: r.Header.Get("X-Request-Id"),
		Meta: map[string]any{"stack_id": peer.StackID, "region": peer.Region},
	})

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"peer":           peer,
		"correlation_id": httpx.CorrelationID(r),
	})
}

// resolveTenant delegates to the shared auth.ResolveTenantHTTP helper (v9.0 L-8).
func resolveTenant(w http.ResponseWriter, r *http.Request, ac auth.AuthContext) (string, bool) {
	return auth.ResolveTenantHTTP(w, r, ac)
}

func bearerToken(header string) string {
	h := strings.TrimSpace(header)
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[len("bearer "):])
	}
	return ""
}
