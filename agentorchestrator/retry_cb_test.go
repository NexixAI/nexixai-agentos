package agentorchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// helper: create a tenant and a run, then shut down executor and set run to failed.
func createFailedRun(t *testing.T, srv *Server, tenantID string) types.Run {
	t.Helper()

	// Create tenant
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"`+tenantID+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", tenantID)
	req.Header.Set("X-Scopes", "tenants:admin")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	// Create a run
	createReq := httptest.NewRequest(http.MethodPost, "/v1/agents/agt_test/runs",
		bytes.NewBufferString(`{"input":{"type":"text","text":"hello"}}`))
	createReq.Header.Set("Authorization", "Bearer test-token")
	createReq.Header.Set("X-Tenant-Id", tenantID)
	createRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}

	var resp types.RunCreateResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Shut down executor to prevent races, then nil it out so retry
	// does not attempt to submit to a closed queue.
	if srv.executor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		srv.executor.Shutdown(ctx)
		cancel()
		srv.executor = nil
	}

	// Set the run to failed.
	run := resp.Run
	run.Status = "failed"
	run.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	run.Error = &types.RunError{Code: "provider_error", Message: "test failure"}
	if err := srv.runs.Save(context.Background(), run); err != nil {
		t.Fatalf("save failed run: %v", err)
	}

	return run
}

func TestRetryFailedRunReturns201(t *testing.T) {
	srv := newTestServer(t)
	run := createFailedRun(t, srv, "tnt_retry1")

	retryReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+run.RunID+"/retry", nil)
	retryReq.Header.Set("Authorization", "Bearer test-token")
	retryReq.Header.Set("X-Tenant-Id", "tnt_retry1")
	retryRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(retryRec, retryReq)

	if retryRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", retryRec.Code, retryRec.Body.String())
	}

	var retryResp types.RunCreateResponse
	if err := json.Unmarshal(retryRec.Body.Bytes(), &retryResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if retryResp.Run.RetryOf != run.RunID {
		t.Fatalf("expected retry_of=%s, got %s", run.RunID, retryResp.Run.RetryOf)
	}
	if retryResp.Run.Status != "queued" {
		t.Fatalf("expected status=queued, got %s", retryResp.Run.Status)
	}
	if retryResp.Run.AgentID != run.AgentID {
		t.Fatalf("expected agent_id=%s, got %s", run.AgentID, retryResp.Run.AgentID)
	}
	if retryResp.Run.TenantID != run.TenantID {
		t.Fatalf("expected tenant_id=%s, got %s", run.TenantID, retryResp.Run.TenantID)
	}
	if retryResp.Run.Input.Text != run.Input.Text {
		t.Fatalf("expected input text=%s, got %s", run.Input.Text, retryResp.Run.Input.Text)
	}
}

func TestRetryRunningRunReturns409(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_retry2"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_retry2")
	req.Header.Set("X-Scopes", "tenants:admin")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	// Create a run
	createReq := httptest.NewRequest(http.MethodPost, "/v1/agents/agt_test/runs",
		bytes.NewBufferString(`{"input":{"type":"text","text":"hello"}}`))
	createReq.Header.Set("Authorization", "Bearer test-token")
	createReq.Header.Set("X-Tenant-Id", "tnt_retry2")
	createRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRec, createReq)

	var resp types.RunCreateResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Shut down executor, nil it out.
	if srv.executor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		srv.executor.Shutdown(ctx)
		cancel()
		srv.executor = nil
	}

	// Set to running (not terminal).
	run := resp.Run
	run.Status = "running"
	if err := srv.runs.Save(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+run.RunID+"/retry", nil)
	retryReq.Header.Set("Authorization", "Bearer test-token")
	retryReq.Header.Set("X-Tenant-Id", "tnt_retry2")
	retryRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(retryRec, retryReq)

	if retryRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", retryRec.Code, retryRec.Body.String())
	}
}

func TestRetryNonexistentRunReturns404(t *testing.T) {
	srv := newTestServer(t)

	// Create tenant
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewBufferString(`{"tenant_id":"tnt_retry3"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_retry3")
	req.Header.Set("X-Scopes", "tenants:admin")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	retryReq := httptest.NewRequest(http.MethodPost, "/v1/runs/run_nonexistent/retry", nil)
	retryReq.Header.Set("Authorization", "Bearer test-token")
	retryReq.Header.Set("X-Tenant-Id", "tnt_retry3")
	retryRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(retryRec, retryReq)

	if retryRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", retryRec.Code, retryRec.Body.String())
	}
}

func TestRetryWithGetMethodReturns405(t *testing.T) {
	srv := newTestServer(t)
	run := createFailedRun(t, srv, "tnt_retry4")

	retryReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+run.RunID+"/retry", nil)
	retryReq.Header.Set("Authorization", "Bearer test-token")
	retryReq.Header.Set("X-Tenant-Id", "tnt_retry4")
	retryRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(retryRec, retryReq)

	if retryRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", retryRec.Code, retryRec.Body.String())
	}
}

func TestCircuitBreakerStatusNotConfigured(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/circuit-breaker", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Scopes", "tenants:admin")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	state, ok := resp["state"].(string)
	if !ok {
		t.Fatalf("expected string state, got %T", resp["state"])
	}
	// The stub provider doesn't implement CircuitBreakerStatus, so we expect not_configured.
	if state != "not_configured" {
		t.Fatalf("expected state=not_configured, got %s", state)
	}
}

func TestCircuitBreakerStatusForbiddenWithoutAdmin(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/circuit-breaker", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	// No admin scope
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
