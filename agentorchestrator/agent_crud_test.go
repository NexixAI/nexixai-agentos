package agentorchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func createTestTenant(t *testing.T, srv *Server, tenantID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"`+tenantID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", tenantID)
	req.Header.Set("X-Scopes", "tenants:admin")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
}

func TestAgentCreate(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_crud")

	body := `{"agent_id":"agt_new","name":"New Agent","description":"test","config":{"system_prompt":"You are helpful."}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_crud")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp types.AgentCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Agent.AgentID != "agt_new" {
		t.Errorf("expected agt_new, got %s", resp.Agent.AgentID)
	}
	if resp.Agent.Version != "1" {
		t.Errorf("expected version 1, got %s", resp.Agent.Version)
	}
	if resp.Agent.Status != "active" {
		t.Errorf("expected active, got %s", resp.Agent.Status)
	}

	// Verify GET returns the created agent.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/agents/agt_new", nil)
	getReq.Header.Set("Authorization", "Bearer test-token")
	getReq.Header.Set("X-Tenant-Id", "tnt_crud")
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
}

func TestAgentCreate_DuplicateReturns409(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_dup")

	body := `{"agent_id":"agt_dup","name":"Agent"}`
	for i, expectedCode := range []int{http.StatusCreated, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/v1/agents/", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("X-Tenant-Id", "tnt_dup")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != expectedCode {
			t.Fatalf("attempt %d: expected %d, got %d: %s", i, expectedCode, rec.Code, rec.Body.String())
		}
	}
}

func TestAgentCreate_InvalidID(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_invalid")

	body := `{"agent_id":"ab","name":"Agent"}` // too short
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_invalid")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentUpdate(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_upd")

	// Create agent.
	createBody := `{"agent_id":"agt_upd","name":"Original"}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/agents/", bytes.NewBufferString(createBody))
	createReq.Header.Set("Authorization", "Bearer test-token")
	createReq.Header.Set("X-Tenant-Id", "tnt_upd")
	createRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", createRec.Code)
	}

	// Update agent.
	updateBody := `{"name":"Updated","description":"new desc"}`
	updateReq := httptest.NewRequest(http.MethodPut, "/v1/agents/agt_upd", bytes.NewBufferString(updateBody))
	updateReq.Header.Set("Authorization", "Bearer test-token")
	updateReq.Header.Set("X-Tenant-Id", "tnt_upd")
	updateRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	var resp types.AgentGetResponse
	json.Unmarshal(updateRec.Body.Bytes(), &resp)
	if resp.Agent.Name != "Updated" {
		t.Errorf("expected name 'Updated', got %q", resp.Agent.Name)
	}
	if resp.Agent.Version != "2" {
		t.Errorf("expected version 2, got %s", resp.Agent.Version)
	}
}

func TestAgentUpdate_NotFoundReturns404(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_upd404")

	updateBody := `{"name":"Test"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/agents/agt_nonexist", bytes.NewBufferString(updateBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_upd404")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAgentDelete(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_del")

	// Create agent.
	createBody := `{"agent_id":"agt_del","name":"To Delete"}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/agents/", bytes.NewBufferString(createBody))
	createReq.Header.Set("Authorization", "Bearer test-token")
	createReq.Header.Set("X-Tenant-Id", "tnt_del")
	createRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRec, createReq)

	// Delete agent.
	delReq := httptest.NewRequest(http.MethodDelete, "/v1/agents/agt_del", nil)
	delReq.Header.Set("Authorization", "Bearer test-token")
	delReq.Header.Set("X-Tenant-Id", "tnt_del")
	delRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", delRec.Code, delRec.Body.String())
	}

	// GET should now return 404.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/agents/agt_del", nil)
	getReq.Header.Set("Authorization", "Bearer test-token")
	getReq.Header.Set("X-Tenant-Id", "tnt_del")
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getRec.Code)
	}
}

func TestAgentDelete_NotFoundReturns404(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_del404")

	req := httptest.NewRequest(http.MethodDelete, "/v1/agents/agt_nonexist", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_del404")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAgentTenantIsolation(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_iso_a")
	createTestTenant(t, srv, "tnt_iso_b")

	// Create agent for tenant A.
	agent := types.Agent{
		AgentID: "agt_iso", TenantID: "tnt_iso_a",
		Name: "Iso Agent", Version: "1", Status: "active",
		CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z",
	}
	srv.agents.Create(context.Background(), agent)

	// Tenant B cannot see/modify tenant A's agent.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/agents/agt_iso", nil)
	getReq.Header.Set("Authorization", "Bearer test-token")
	getReq.Header.Set("X-Tenant-Id", "tnt_iso_b")
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant GET, got %d", getRec.Code)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/v1/agents/agt_iso", nil)
	delReq.Header.Set("Authorization", "Bearer test-token")
	delReq.Header.Set("X-Tenant-Id", "tnt_iso_b")
	delRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant DELETE, got %d", delRec.Code)
	}
}
