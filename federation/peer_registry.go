package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type PeerInfo struct {
	StackID     string    `json:"stack_id"`
	Environment string    `json:"environment"`
	Region      string    `json:"region"`
	APIVersions []string  `json:"api_versions"`
	Endpoints   Endpoints `json:"endpoints"`
	Build       Build     `json:"build"`
}

type Endpoints struct {
	AgentOrchestratorBaseURL string `json:"agent-orchestrator_base_url"`
	ModelPolicyBaseURL       string `json:"model-policy_base_url"`
}

type Build struct {
	Version   string `json:"version"`
	GitSHA    string `json:"git_sha"`
	Timestamp string `json:"timestamp"`
}

type PeersFile struct {
	Local PeerInfo   `json:"local"`
	Peers []PeerInfo `json:"peers"`
}

// PeerHealth tracks the health status of a federation peer.
type PeerHealth struct {
	Healthy   bool          `json:"healthy"`
	LastCheck time.Time     `json:"last_check"`
	LastError string        `json:"last_error,omitempty"`
	Latency   time.Duration `json:"latency_ms"`
}

// Registry provides a minimal peer lookup by stack_id.
type Registry struct {
	mu     sync.RWMutex
	Local  PeerInfo
	peers  map[string]PeerInfo
	health map[string]*PeerHealth
	stopCh chan struct{}
}

func LoadRegistryFromEnv() (*Registry, error) {
	path := strings.TrimSpace(os.Getenv("AGENTOS_PEERS_FILE"))
	if path == "" {
		// Return empty registry if no peers file configured.
		return &Registry{
			peers:  make(map[string]PeerInfo),
			health: make(map[string]*PeerHealth),
			stopCh: make(chan struct{}),
		}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf PeersFile
	if err := json.Unmarshal(b, &pf); err != nil {
		return nil, err
	}

	// Allow overriding local identity via env (useful for compose multi-node).
	stackID := strings.TrimSpace(os.Getenv("AGENTOS_STACK_ID"))
	env := strings.TrimSpace(os.Getenv("AGENTOS_ENVIRONMENT"))
	region := strings.TrimSpace(os.Getenv("AGENTOS_REGION"))
	if stackID != "" {
		pf.Local.StackID = stackID
	}
	if env != "" {
		pf.Local.Environment = env
	}
	if region != "" {
		pf.Local.Region = region
	}

	peers := make(map[string]PeerInfo, len(pf.Peers))
	for _, p := range pf.Peers {
		if p.StackID == "" {
			continue
		}
		peers[p.StackID] = p
	}

	return &Registry{
		Local:  pf.Local,
		peers:  peers,
		health: make(map[string]*PeerHealth),
		stopCh: make(chan struct{}),
	}, nil
}

func (r *Registry) Get(stackID string) (PeerInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.peers[stackID]
	return p, ok
}

// AddPeer dynamically adds or updates a peer in the registry.
func (r *Registry) AddPeer(peer PeerInfo) {
	if peer.StackID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers[peer.StackID] = peer
	if r.health[peer.StackID] == nil {
		r.health[peer.StackID] = &PeerHealth{}
	}
}

// RemovePeer removes a peer from the registry.
func (r *Registry) RemovePeer(stackID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, stackID)
	delete(r.health, stackID)
}

// List returns all registered peers.
func (r *Registry) List() []PeerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	peers := make([]PeerInfo, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	return peers
}

// HealthyPeers returns only peers that passed their last health check.
func (r *Registry) HealthyPeers() []PeerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var peers []PeerInfo
	for sid, p := range r.peers {
		if h, ok := r.health[sid]; ok && h.Healthy {
			peers = append(peers, p)
		}
	}
	return peers
}

// GetHealth returns the health status of a peer.
func (r *Registry) GetHealth(stackID string) (PeerHealth, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.health[stackID]
	if !ok {
		return PeerHealth{}, false
	}
	return *h, true
}

// StartHealthChecks begins periodic health checking of all peers.
func (r *Registry) StartHealthChecks(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Do an initial check.
		r.checkAllPeers()
		for {
			select {
			case <-ticker.C:
				r.checkAllPeers()
			case <-r.stopCh:
				return
			}
		}
	}()
}

// StopHealthChecks stops the periodic health check goroutine.
func (r *Registry) StopHealthChecks() {
	select {
	case <-r.stopCh:
		// Already stopped.
	default:
		close(r.stopCh)
	}
}

func (r *Registry) checkAllPeers() {
	r.mu.RLock()
	peers := make([]PeerInfo, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	r.mu.RUnlock()

	for _, peer := range peers {
		r.checkPeer(peer)
	}
}

// healthClient is a dedicated HTTP client for peer health checks with
// sensible timeouts, replacing http.DefaultClient (v9.0 L-3).
var healthClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        20,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	},
}

func (r *Registry) checkPeer(peer PeerInfo) {
	healthURL := strings.TrimRight(peer.Endpoints.AgentOrchestratorBaseURL, "/") + "/health"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		r.updateHealth(peer.StackID, false, err.Error(), 0)
		return
	}

	resp, err := healthClient.Do(req)
	latency := time.Since(start)
	if err != nil {
		r.updateHealth(peer.StackID, false, err.Error(), latency)
		slog.Debug("peer health check failed", "stack_id", peer.StackID, "error", err)
		return
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode == http.StatusOK
	errMsg := ""
	if !healthy {
		errMsg = fmt.Sprintf("health check returned status %d", resp.StatusCode)
	}
	r.updateHealth(peer.StackID, healthy, errMsg, latency)
}

func (r *Registry) updateHealth(stackID string, healthy bool, errMsg string, latency time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.health[stackID] == nil {
		r.health[stackID] = &PeerHealth{}
	}
	h := r.health[stackID]
	h.Healthy = healthy
	h.LastCheck = time.Now()
	h.LastError = errMsg
	h.Latency = latency
}
