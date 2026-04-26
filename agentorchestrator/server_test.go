package agentorchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// newTestServer creates a server with isolated temp storage for test isolation.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENTOS_DEV_MODE", "true") // allow nil-store pass-through in tests
	t.Setenv("AGENTOS_RUN_STORE_FILE", filepath.Join(dir, "runs.json"))
	t.Setenv("AGENTOS_AGENT_STORE_DIR", filepath.Join(dir, "agents"))
	t.Setenv("AGENTOS_MEMORY_STORE_DIR", filepath.Join(dir, "memory"))
	t.Setenv("AGENTOS_KV_STORE_DIR", filepath.Join(dir, "kv"))
	t.Setenv("AGENTOS_MODEL_PROVIDER", "stub")
	srv, err := New("test")
	if err != nil {
		t.Fatalf("New server error: %v", err)
	}
	t.Cleanup(func() {
		if srv.executor != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			srv.executor.Shutdown(ctx)
		}
	})
	return srv
}

func TestCancelQueuedRunSucceeds(t *testing.T) {
	srv := newTestServer(t)

	// Create a tenant first (requires tenants:admin scope)
	createTenantReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_test"}`))
	createTenantReq.Header.Set("Authorization", "Bearer test-token")
	createTenantReq.Header.Set("X-Tenant-Id", "tnt_test")
	createTenantReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantRec, createTenantReq)

	// Create a run
	createRunReq := httptest.NewRequest(http.MethodPost, "/v1/agents/agt_test/runs", bytes.NewBufferString(`{"input":{"type":"text","text":"hello"}}`))
	createRunReq.Header.Set("Authorization", "Bearer test-token")
	createRunReq.Header.Set("X-Tenant-Id", "tnt_test")
	createRunRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRunRec, createRunReq)

	if createRunRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for run creation, got %d: %s", createRunRec.Code, createRunRec.Body.String())
	}

	var createResp types.RunCreateResponse
	if err := json.Unmarshal(createRunRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	runID := createResp.Run.RunID

	if createResp.Run.Status != "queued" {
		t.Fatalf("expected status queued, got %s", createResp.Run.Status)
	}

	// Cancel the run. With async execution, the run may have already
	// completed by the time cancel arrives, so accept either 200 (canceled)
	// or 409 (already in terminal state).
	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+":cancel", nil)
	cancelReq.Header.Set("Authorization", "Bearer test-token")
	cancelReq.Header.Set("X-Tenant-Id", "tnt_test")
	cancelRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRec, cancelReq)

	switch cancelRec.Code {
	case http.StatusOK:
		var cancelResp types.RunCancelResponse
		if err := json.Unmarshal(cancelRec.Body.Bytes(), &cancelResp); err != nil {
			t.Fatalf("unmarshal cancel response: %v", err)
		}
		if cancelResp.Run.Status != "canceled" {
			t.Fatalf("expected status canceled, got %s", cancelResp.Run.Status)
		}
		if cancelResp.Run.CompletedAt == "" {
			t.Fatalf("expected completed_at to be set")
		}
	case http.StatusConflict:
		// Run already completed or failed before cancel arrived; acceptable.
		t.Logf("cancel returned 409 (run already in terminal state) — acceptable race")
	default:
		t.Fatalf("expected 200 or 409 for cancel, got %d: %s", cancelRec.Code, cancelRec.Body.String())
	}
}

func TestCancelCompletedRunReturns409(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant (requires tenants:admin scope)
	createTenantReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_test2"}`))
	createTenantReq.Header.Set("Authorization", "Bearer test-token")
	createTenantReq.Header.Set("X-Tenant-Id", "tnt_test2")
	createTenantReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantRec, createTenantReq)

	// Create a run
	createRunReq := httptest.NewRequest(http.MethodPost, "/v1/agents/agt_test/runs", bytes.NewBufferString(`{"input":{"type":"text","text":"hello"}}`))
	createRunReq.Header.Set("Authorization", "Bearer test-token")
	createRunReq.Header.Set("X-Tenant-Id", "tnt_test2")
	createRunRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRunRec, createRunReq)

	var createResp types.RunCreateResponse
	if err := json.Unmarshal(createRunRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	runID := createResp.Run.RunID

	// Shut down executor so it doesn't race with our manual status update.
	if srv.executor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		srv.executor.Shutdown(ctx)
		cancel()
	}

	// Manually set the run to completed state in storage.
	run := createResp.Run
	run.Status = "completed"
	run.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	if err := srv.runs.Save(context.Background(), run); err != nil {
		t.Fatalf("save completed run: %v", err)
	}

	// Try to cancel the completed run
	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+":cancel", nil)
	cancelReq.Header.Set("Authorization", "Bearer test-token")
	cancelReq.Header.Set("X-Tenant-Id", "tnt_test2")
	cancelRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for cancel of completed run, got %d: %s", cancelRec.Code, cancelRec.Body.String())
	}
}

func TestCancelRunFromDifferentTenantReturns404(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant A (requires tenants:admin scope)
	createTenantAReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_alpha"}`))
	createTenantAReq.Header.Set("Authorization", "Bearer test-token")
	createTenantAReq.Header.Set("X-Tenant-Id", "tnt_alpha")
	createTenantAReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantARec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantARec, createTenantAReq)

	// Create tenant B (requires tenants:admin scope)
	createTenantBReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_beta"}`))
	createTenantBReq.Header.Set("Authorization", "Bearer test-token")
	createTenantBReq.Header.Set("X-Tenant-Id", "tnt_beta")
	createTenantBReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantBRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantBRec, createTenantBReq)

	// Create a run in tenant A
	createRunReq := httptest.NewRequest(http.MethodPost, "/v1/agents/agt_test/runs", bytes.NewBufferString(`{"input":{"type":"text","text":"hello"}}`))
	createRunReq.Header.Set("Authorization", "Bearer test-token")
	createRunReq.Header.Set("X-Tenant-Id", "tnt_alpha")
	createRunRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRunRec, createRunReq)

	var createResp types.RunCreateResponse
	if err := json.Unmarshal(createRunRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	runID := createResp.Run.RunID

	// Try to cancel from tenant B - should get 404
	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+":cancel", nil)
	cancelReq.Header.Set("Authorization", "Bearer test-token")
	cancelReq.Header.Set("X-Tenant-Id", "tnt_beta")
	cancelRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cancel from different tenant, got %d: %s", cancelRec.Code, cancelRec.Body.String())
	}
}

func TestCancelAlreadyCanceledRunReturns409(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant (requires tenants:admin scope)
	createTenantReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_test3"}`))
	createTenantReq.Header.Set("Authorization", "Bearer test-token")
	createTenantReq.Header.Set("X-Tenant-Id", "tnt_test3")
	createTenantReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantRec, createTenantReq)

	// Create a run
	createRunReq := httptest.NewRequest(http.MethodPost, "/v1/agents/agt_test/runs", bytes.NewBufferString(`{"input":{"type":"text","text":"hello"}}`))
	createRunReq.Header.Set("Authorization", "Bearer test-token")
	createRunReq.Header.Set("X-Tenant-Id", "tnt_test3")
	createRunRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRunRec, createRunReq)

	var createResp types.RunCreateResponse
	if err := json.Unmarshal(createRunRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	runID := createResp.Run.RunID

	// Cancel the run first time. With async execution, the run may have
	// already completed, so the first cancel might return 409 too.
	cancelReq1 := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+":cancel", nil)
	cancelReq1.Header.Set("Authorization", "Bearer test-token")
	cancelReq1.Header.Set("X-Tenant-Id", "tnt_test3")
	cancelRec1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRec1, cancelReq1)

	if cancelRec1.Code != http.StatusOK && cancelRec1.Code != http.StatusConflict {
		t.Fatalf("expected 200 or 409 for first cancel, got %d", cancelRec1.Code)
	}

	// Try to cancel again - should get 409
	cancelReq2 := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+":cancel", nil)
	cancelReq2.Header.Set("Authorization", "Bearer test-token")
	cancelReq2.Header.Set("X-Tenant-Id", "tnt_test3")
	cancelRec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRec2, cancelReq2)

	if cancelRec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 for second cancel, got %d: %s", cancelRec2.Code, cancelRec2.Body.String())
	}
}

func TestListAgentsReturnsOnlyTenantAgents(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant A (requires tenants:admin scope)
	createTenantAReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_agent_a"}`))
	createTenantAReq.Header.Set("Authorization", "Bearer test-token")
	createTenantAReq.Header.Set("X-Tenant-Id", "tnt_agent_a")
	createTenantAReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantARec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantARec, createTenantAReq)

	// Create tenant B (requires tenants:admin scope)
	createTenantBReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_agent_b"}`))
	createTenantBReq.Header.Set("Authorization", "Bearer test-token")
	createTenantBReq.Header.Set("X-Tenant-Id", "tnt_agent_b")
	createTenantBReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantBRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantBRec, createTenantBReq)

	// Manually create an agent for tenant A
	agentA := types.Agent{
		AgentID:   "agt_tenant_a",
		TenantID:  "tnt_agent_a",
		Name:      "Agent A",
		Version:   "1.0",
		Status:    "active",
		CreatedAt: "2025-01-01T00:00:00Z",
		UpdatedAt: "2025-01-01T00:00:00Z",
	}
	if err := srv.agents.Create(context.Background(), agentA); err != nil {
		t.Fatalf("create agent A error: %v", err)
	}

	// Manually create an agent for tenant B
	agentB := types.Agent{
		AgentID:   "agt_tenant_b",
		TenantID:  "tnt_agent_b",
		Name:      "Agent B",
		Version:   "1.0",
		Status:    "active",
		CreatedAt: "2025-01-01T00:00:00Z",
		UpdatedAt: "2025-01-01T00:00:00Z",
	}
	if err := srv.agents.Create(context.Background(), agentB); err != nil {
		t.Fatalf("create agent B error: %v", err)
	}

	// List agents for tenant A - should only get tenant A's agent
	listReqA := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	listReqA.Header.Set("Authorization", "Bearer test-token")
	listReqA.Header.Set("X-Tenant-Id", "tnt_agent_a")
	listRecA := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRecA, listReqA)

	if listRecA.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listRecA.Code, listRecA.Body.String())
	}

	var respA types.AgentListResponse
	if err := json.Unmarshal(listRecA.Body.Bytes(), &respA); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(respA.Agents) != 1 {
		t.Fatalf("expected 1 agent for tenant A, got %d", len(respA.Agents))
	}
	if respA.Agents[0].AgentID != "agt_tenant_a" {
		t.Fatalf("expected agt_tenant_a, got %s", respA.Agents[0].AgentID)
	}

	// List agents for tenant B - should only get tenant B's agent
	listReqB := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	listReqB.Header.Set("Authorization", "Bearer test-token")
	listReqB.Header.Set("X-Tenant-Id", "tnt_agent_b")
	listRecB := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRecB, listReqB)

	if listRecB.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listRecB.Code, listRecB.Body.String())
	}

	var respB types.AgentListResponse
	if err := json.Unmarshal(listRecB.Body.Bytes(), &respB); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(respB.Agents) != 1 {
		t.Fatalf("expected 1 agent for tenant B, got %d", len(respB.Agents))
	}
	if respB.Agents[0].AgentID != "agt_tenant_b" {
		t.Fatalf("expected agt_tenant_b, got %s", respB.Agents[0].AgentID)
	}
}

func TestGetAgentFromDifferentTenantReturns404(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant A (requires tenants:admin scope)
	createTenantAReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_get_a"}`))
	createTenantAReq.Header.Set("Authorization", "Bearer test-token")
	createTenantAReq.Header.Set("X-Tenant-Id", "tnt_get_a")
	createTenantAReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantARec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantARec, createTenantAReq)

	// Create tenant B (requires tenants:admin scope)
	createTenantBReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_get_b"}`))
	createTenantBReq.Header.Set("Authorization", "Bearer test-token")
	createTenantBReq.Header.Set("X-Tenant-Id", "tnt_get_b")
	createTenantBReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantBRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantBRec, createTenantBReq)

	// Create an agent for tenant A
	agentA := types.Agent{
		AgentID:   "agt_private",
		TenantID:  "tnt_get_a",
		Name:      "Private Agent",
		Version:   "1.0",
		Status:    "active",
		CreatedAt: "2025-01-01T00:00:00Z",
		UpdatedAt: "2025-01-01T00:00:00Z",
	}
	if err := srv.agents.Create(context.Background(), agentA); err != nil {
		t.Fatalf("create agent error: %v", err)
	}

	// Tenant A can get the agent
	getReqA := httptest.NewRequest(http.MethodGet, "/v1/agents/agt_private", nil)
	getReqA.Header.Set("Authorization", "Bearer test-token")
	getReqA.Header.Set("X-Tenant-Id", "tnt_get_a")
	getRecA := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRecA, getReqA)

	if getRecA.Code != http.StatusOK {
		t.Fatalf("expected 200 for tenant A, got %d: %s", getRecA.Code, getRecA.Body.String())
	}

	var respA types.AgentGetResponse
	if err := json.Unmarshal(getRecA.Body.Bytes(), &respA); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if respA.Agent.AgentID != "agt_private" {
		t.Fatalf("expected agt_private, got %s", respA.Agent.AgentID)
	}

	// Tenant B should get 404 when trying to get tenant A's agent
	getReqB := httptest.NewRequest(http.MethodGet, "/v1/agents/agt_private", nil)
	getReqB.Header.Set("Authorization", "Bearer test-token")
	getReqB.Header.Set("X-Tenant-Id", "tnt_get_b")
	getRecB := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRecB, getReqB)

	if getRecB.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for tenant B accessing tenant A's agent, got %d: %s", getRecB.Code, getRecB.Body.String())
	}
}

func TestGetNonExistentAgentReturns404(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant (requires tenants:admin scope)
	createTenantReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_nonexist"}`))
	createTenantReq.Header.Set("Authorization", "Bearer test-token")
	createTenantReq.Header.Set("X-Tenant-Id", "tnt_nonexist")
	createTenantReq.Header.Set("X-Scopes", "tenants:admin")
	createTenantRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createTenantRec, createTenantReq)

	// Try to get non-existent agent
	getReq := httptest.NewRequest(http.MethodGet, "/v1/agents/agt_does_not_exist", nil)
	getReq.Header.Set("Authorization", "Bearer test-token")
	getReq.Header.Set("X-Tenant-Id", "tnt_nonexist")
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent agent, got %d: %s", getRec.Code, getRec.Body.String())
	}
}
