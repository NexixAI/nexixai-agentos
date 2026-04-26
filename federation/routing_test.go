package federation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	// Ensure dev mode so JWT middleware passes through.
	os.Setenv("AGENTOS_FED_AUTH_DISABLED", "1")
	t.Cleanup(func() { os.Unsetenv("AGENTOS_FED_AUTH_DISABLED") })

	return New("test")
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/federation/health", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for health, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPeerInfoEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/federation/peer", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for peer info, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resp["peer"]; !ok {
		t.Fatal("expected 'peer' key in response")
	}
}

func TestPeerInfoMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/federation/peer", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /peer, got %d", rec.Code)
	}
}

func TestCapabilitiesEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/federation/peer/capabilities", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for capabilities, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["protocol"] != "1.0" {
		t.Fatalf("expected protocol 1.0, got %v", resp["protocol"])
	}
	caps, ok := resp["capabilities"].([]any)
	if !ok || len(caps) == 0 {
		t.Fatal("expected non-empty capabilities array")
	}
}

func TestCapabilitiesMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/federation/peer/capabilities", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /capabilities, got %d", rec.Code)
	}
}

func TestPeersRegisterEndpoint(t *testing.T) {
	s := newTestServer(t)

	peer := PeerInfo{
		StackID:     "stk_new",
		Environment: "test",
		Region:      "us-west-2",
	}
	payload, _ := json.Marshal(peer)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/federation/peers", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for peer registration, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	peerResp, ok := resp["peer"].(map[string]any)
	if !ok {
		t.Fatal("expected 'peer' key in response")
	}
	if peerResp["stack_id"] != "stk_new" {
		t.Fatalf("expected stack_id stk_new, got %v", peerResp["stack_id"])
	}
}

func TestPeersRegisterMissingStackID(t *testing.T) {
	s := newTestServer(t)

	payload, _ := json.Marshal(PeerInfo{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/federation/peers", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing stack_id, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPeersRegisterMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/federation/peers", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /peers, got %d", rec.Code)
	}
}

func TestForwardRunMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/federation/runs:forward", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /runs:forward, got %d", rec.Code)
	}
}

func TestEventsIngestMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/federation/events:ingest", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /events:ingest, got %d", rec.Code)
	}
}

func TestEventsIngestInvalidJSON(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/federation/events:ingest", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}
