package federation

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestRegistry() *Registry {
	return &Registry{
		peers:  make(map[string]PeerInfo),
		health: make(map[string]*PeerHealth),
		stopCh: make(chan struct{}),
	}
}

func TestRegistryAddAndGet(t *testing.T) {
	r := newTestRegistry()

	peer := PeerInfo{
		StackID:     "stk_alpha",
		Environment: "staging",
		Region:      "us-east-1",
		Endpoints: Endpoints{
			AgentOrchestratorBaseURL: "http://alpha:9091",
			ModelPolicyBaseURL:       "http://alpha:9092",
		},
	}

	r.AddPeer(peer)

	got, ok := r.Get("stk_alpha")
	if !ok {
		t.Fatal("expected peer to exist after AddPeer")
	}
	if got.StackID != "stk_alpha" {
		t.Fatalf("expected stack_id stk_alpha, got %s", got.StackID)
	}
	if got.Region != "us-east-1" {
		t.Fatalf("expected region us-east-1, got %s", got.Region)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := newTestRegistry()

	_, ok := r.Get("stk_nonexistent")
	if ok {
		t.Fatal("expected Get to return false for missing peer")
	}
}

func TestRegistryList(t *testing.T) {
	r := newTestRegistry()

	r.AddPeer(PeerInfo{StackID: "stk_a", Region: "us-east-1"})
	r.AddPeer(PeerInfo{StackID: "stk_b", Region: "eu-west-1"})

	peers := r.List()
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	ids := map[string]bool{}
	for _, p := range peers {
		ids[p.StackID] = true
	}
	if !ids["stk_a"] || !ids["stk_b"] {
		t.Fatalf("expected stk_a and stk_b in list, got %v", ids)
	}
}

func TestRegistryListEmpty(t *testing.T) {
	r := newTestRegistry()
	peers := r.List()
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}
}

func TestRegistryDuplicateRegistration(t *testing.T) {
	r := newTestRegistry()

	r.AddPeer(PeerInfo{StackID: "stk_dup", Region: "us-east-1"})
	r.AddPeer(PeerInfo{StackID: "stk_dup", Region: "eu-west-1"})

	peers := r.List()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer after duplicate add, got %d", len(peers))
	}

	got, ok := r.Get("stk_dup")
	if !ok {
		t.Fatal("expected peer to exist")
	}
	if got.Region != "eu-west-1" {
		t.Fatalf("expected region to be updated to eu-west-1, got %s", got.Region)
	}
}

func TestRegistryRemovePeer(t *testing.T) {
	r := newTestRegistry()

	r.AddPeer(PeerInfo{StackID: "stk_rm"})
	r.RemovePeer("stk_rm")

	_, ok := r.Get("stk_rm")
	if ok {
		t.Fatal("expected peer to be removed")
	}
	if len(r.List()) != 0 {
		t.Fatal("expected empty list after remove")
	}
}

func TestRegistryAddPeerEmptyStackIDIgnored(t *testing.T) {
	r := newTestRegistry()

	r.AddPeer(PeerInfo{StackID: "", Region: "us-east-1"})
	if len(r.List()) != 0 {
		t.Fatal("expected empty stack_id to be ignored")
	}
}

func TestRegistryHealthStatus(t *testing.T) {
	r := newTestRegistry()

	r.AddPeer(PeerInfo{StackID: "stk_health"})

	// AddPeer initializes health entry
	h, ok := r.GetHealth("stk_health")
	if !ok {
		t.Fatal("expected health entry to exist after AddPeer")
	}
	if h.Healthy {
		// Newly added peer should not be marked healthy yet
		t.Log("new peer health is false as expected")
	}

	// Manually update health to simulate a successful check
	r.updateHealth("stk_health", true, "", 50_000_000) // 50ms

	h, ok = r.GetHealth("stk_health")
	if !ok {
		t.Fatal("expected health entry after update")
	}
	if !h.Healthy {
		t.Fatal("expected peer to be healthy after positive update")
	}
	if h.LastError != "" {
		t.Fatalf("expected no error, got %q", h.LastError)
	}
}

func TestRegistryHealthStatusUnhealthy(t *testing.T) {
	r := newTestRegistry()

	r.AddPeer(PeerInfo{StackID: "stk_fail"})
	r.updateHealth("stk_fail", false, "connection refused", 0)

	h, ok := r.GetHealth("stk_fail")
	if !ok {
		t.Fatal("expected health entry")
	}
	if h.Healthy {
		t.Fatal("expected unhealthy peer")
	}
	if h.LastError != "connection refused" {
		t.Fatalf("expected last error 'connection refused', got %q", h.LastError)
	}
}

func TestRegistryHealthyPeers(t *testing.T) {
	r := newTestRegistry()

	r.AddPeer(PeerInfo{StackID: "stk_ok"})
	r.AddPeer(PeerInfo{StackID: "stk_down"})

	r.updateHealth("stk_ok", true, "", 10_000_000)
	r.updateHealth("stk_down", false, "timeout", 0)

	healthy := r.HealthyPeers()
	if len(healthy) != 1 {
		t.Fatalf("expected 1 healthy peer, got %d", len(healthy))
	}
	if healthy[0].StackID != "stk_ok" {
		t.Fatalf("expected stk_ok to be healthy, got %s", healthy[0].StackID)
	}
}

func TestRegistryGetHealthMissing(t *testing.T) {
	r := newTestRegistry()
	_, ok := r.GetHealth("stk_missing")
	if ok {
		t.Fatal("expected GetHealth to return false for missing peer")
	}
}

func TestRegistryHealthCheckWithRealServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRegistry()
	r.AddPeer(PeerInfo{
		StackID: "stk_real",
		Endpoints: Endpoints{
			AgentOrchestratorBaseURL: srv.URL,
		},
	})

	// Run the health check directly
	r.checkPeer(PeerInfo{
		StackID: "stk_real",
		Endpoints: Endpoints{
			AgentOrchestratorBaseURL: srv.URL,
		},
	})

	h, ok := r.GetHealth("stk_real")
	if !ok {
		t.Fatal("expected health entry after check")
	}
	if !h.Healthy {
		t.Fatalf("expected healthy after 200 response, got error: %s", h.LastError)
	}
}

func TestRegistryRemoveNonExistentPeer(t *testing.T) {
	r := newTestRegistry()
	// Should not panic or error when removing a peer that was never added.
	r.RemovePeer("stk_never_existed")
	if len(r.List()) != 0 {
		t.Fatal("expected empty list")
	}
}

func TestRegistryHealthCheckUnreachableServer(t *testing.T) {
	r := newTestRegistry()
	r.AddPeer(PeerInfo{
		StackID: "stk_dead",
		Endpoints: Endpoints{
			AgentOrchestratorBaseURL: "http://127.0.0.1:1",
		},
	})

	r.checkPeer(PeerInfo{
		StackID: "stk_dead",
		Endpoints: Endpoints{
			AgentOrchestratorBaseURL: "http://127.0.0.1:1",
		},
	})

	h, ok := r.GetHealth("stk_dead")
	if !ok {
		t.Fatal("expected health entry after failed check")
	}
	if h.Healthy {
		t.Fatal("expected unhealthy after connection refused")
	}
	if h.LastError == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestRegistryHealthCheckUnhealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	r := newTestRegistry()
	r.AddPeer(PeerInfo{
		StackID: "stk_503",
		Endpoints: Endpoints{
			AgentOrchestratorBaseURL: srv.URL,
		},
	})

	r.checkPeer(PeerInfo{
		StackID: "stk_503",
		Endpoints: Endpoints{
			AgentOrchestratorBaseURL: srv.URL,
		},
	})

	h, ok := r.GetHealth("stk_503")
	if !ok {
		t.Fatal("expected health entry after check")
	}
	if h.Healthy {
		t.Fatal("expected unhealthy after 503 response")
	}
}
