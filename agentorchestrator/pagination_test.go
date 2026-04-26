package agentorchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestAgentListPagination_DefaultLimit(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_page_default")

	// Create 5 agents.
	for i := 0; i < 5; i++ {
		agent := types.Agent{
			AgentID: fmt.Sprintf("agt_pg_%03d", i), TenantID: "tnt_page_default",
			Name: fmt.Sprintf("Agent %d", i), Version: "1", Status: "active",
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		}
		if err := srv.agents.Create(context.Background(), agent); err != nil {
			t.Fatalf("create agent %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_page_default")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp types.AgentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Agents) != 5 {
		t.Fatalf("expected 5 agents, got %d", len(resp.Agents))
	}
	if resp.HasMore {
		t.Fatal("expected has_more=false with 5 agents and default limit 50")
	}
}

func TestAgentListPagination_LimitAndCursor(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_page_cursor")

	// Create 10 agents with sortable IDs.
	for i := 0; i < 10; i++ {
		agent := types.Agent{
			AgentID: fmt.Sprintf("agt_cur_%03d", i), TenantID: "tnt_page_cursor",
			Name: fmt.Sprintf("Agent %d", i), Version: "1", Status: "active",
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		}
		if err := srv.agents.Create(context.Background(), agent); err != nil {
			t.Fatalf("create agent %d: %v", i, err)
		}
	}

	// Page 1: limit=3
	req1 := httptest.NewRequest(http.MethodGet, "/v1/agents/?limit=3", nil)
	req1.Header.Set("Authorization", "Bearer test-token")
	req1.Header.Set("X-Tenant-Id", "tnt_page_cursor")
	rec1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec1, req1)

	var page1 types.AgentListResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &page1); err != nil {
		t.Fatalf("unmarshal page 1: %v", err)
	}

	if len(page1.Agents) != 3 {
		t.Fatalf("page 1: expected 3 agents, got %d", len(page1.Agents))
	}
	if !page1.HasMore {
		t.Fatal("page 1: expected has_more=true")
	}

	// Page 2: after=last agent ID from page 1
	cursor := page1.Agents[len(page1.Agents)-1].AgentID
	req2 := httptest.NewRequest(http.MethodGet, "/v1/agents/?limit=3&after="+cursor, nil)
	req2.Header.Set("Authorization", "Bearer test-token")
	req2.Header.Set("X-Tenant-Id", "tnt_page_cursor")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)

	var page2 types.AgentListResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("unmarshal page 2: %v", err)
	}

	if len(page2.Agents) != 3 {
		t.Fatalf("page 2: expected 3 agents, got %d", len(page2.Agents))
	}
	if !page2.HasMore {
		t.Fatal("page 2: expected has_more=true")
	}

	// No overlap.
	for _, a1 := range page1.Agents {
		for _, a2 := range page2.Agents {
			if a1.AgentID == a2.AgentID {
				t.Fatalf("overlap: %s appears in both pages", a1.AgentID)
			}
		}
	}

	// Page 2 IDs should be greater than page 1 IDs.
	if page2.Agents[0].AgentID <= cursor {
		t.Fatalf("page 2 first agent %s should be > cursor %s", page2.Agents[0].AgentID, cursor)
	}
}

func TestAgentListPagination_MaxLimit(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_page_max")

	// Request with limit > 200 should be capped at 200.
	req := httptest.NewRequest(http.MethodGet, "/v1/agents/?limit=500", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_page_max")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp types.AgentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// No error — the limit is just capped internally.
	if resp.HasMore {
		t.Fatal("expected has_more=false for empty tenant with capped limit")
	}
}

func TestAgentListPagination_EmptyResult(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_page_empty")

	req := httptest.NewRequest(http.MethodGet, "/v1/agents/", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_page_empty")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var resp types.AgentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(resp.Agents))
	}
	if resp.HasMore {
		t.Fatal("expected has_more=false for empty result")
	}
}

func TestAgentListPagination_CursorBeyondEnd(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_page_beyond")

	agent := types.Agent{
		AgentID: "agt_only", TenantID: "tnt_page_beyond",
		Name: "Only", Version: "1", Status: "active",
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	if err := srv.agents.Create(context.Background(), agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/agents/?after=zzz_beyond_all", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_page_beyond")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var resp types.AgentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Agents) != 0 {
		t.Fatalf("expected 0 agents for cursor beyond end, got %d", len(resp.Agents))
	}
	if resp.HasMore {
		t.Fatal("expected has_more=false for cursor beyond end")
	}
}

func TestAgentListPagination_ExactBoundary(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_page_exact")

	// Create exactly 3 agents with limit=3: has_more should be false.
	for i := 0; i < 3; i++ {
		agent := types.Agent{
			AgentID: fmt.Sprintf("agt_ex_%03d", i), TenantID: "tnt_page_exact",
			Name: fmt.Sprintf("Agent %d", i), Version: "1", Status: "active",
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		}
		if err := srv.agents.Create(context.Background(), agent); err != nil {
			t.Fatalf("create agent %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/agents/?limit=3", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_page_exact")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var resp types.AgentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(resp.Agents))
	}
	if resp.HasMore {
		t.Fatal("expected has_more=false when count == limit")
	}
}

func TestAPIKeyList_NilStore(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_apikey")

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/api-keys", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_apikey")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	keys, ok := resp["api_keys"].([]any)
	if !ok {
		t.Fatal("expected api_keys to be an array")
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys for nil store, got %d", len(keys))
	}
	if hasMore, ok := resp["has_more"].(bool); !ok || hasMore {
		t.Fatal("expected has_more=false")
	}
}

func TestAPIKeyCreate_NoStore_Returns501(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_apikey_post")

	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/api-keys", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_apikey_post")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestAPIKeyPatch_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	createTestTenant(t, srv, "tnt_apikey_patch")

	req := httptest.NewRequest(http.MethodPatch, "/v1/tenants/api-keys", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_apikey_patch")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestAuditLog_RequiresAdminScope(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/audit-log", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	// No admin scope.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuditLog_EmptyResult(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/audit-log", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Tenant-Id", "tnt_test")
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

	entries, ok := resp["entries"].([]any)
	if !ok {
		t.Fatal("expected entries to be an array")
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestAuditLog_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/audit-log", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Scopes", "tenants:admin")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
