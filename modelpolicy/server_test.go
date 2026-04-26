package modelpolicy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestInvokePolicyDeniedWhenOptionDeny(t *testing.T) {
	s := New("test")
	rec := httptest.NewRecorder()
	reqBody := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "local-stub-llm",
		Input:     map[string]any{"text": "hello"},
		Options:   map[string]any{"deny": true},
	}
	payload, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/models:invoke", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInvokeReturnsUsage(t *testing.T) {
	s := New("test")
	rec := httptest.NewRecorder()
	reqBody := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "local-stub-llm",
		Input:     map[string]any{"text": "hello world"},
		Options:   map[string]any{},
	}
	payload, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/models:invoke", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp types.ModelInvokeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Usage == nil || resp.Usage["total_tokens"] == nil {
		t.Fatalf("expected usage in response, got %+v", resp)
	}
}

func TestInvokeDeniedModelReturns403(t *testing.T) {
	s := New("test")

	// Create tenant with denied model policy
	tenant := types.Tenant{
		TenantID: "tnt_policy_deny",
		Policy: &types.TenantPolicy{
			DeniedModels: []string{"local-stub-llm"},
		},
	}
	_ = s.tenants.Create(tenant)

	rec := httptest.NewRecorder()
	reqBody := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "local-stub-llm",
		Input:     map[string]any{"text": "hello"},
		Options:   map[string]any{},
	}
	payload, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/models:invoke", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_policy_deny")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for denied model, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInvokeModelNotInAllowListReturns403(t *testing.T) {
	s := New("test")

	// Create tenant with allowed model policy that doesn't include the requested model
	tenant := types.Tenant{
		TenantID: "tnt_policy_allow",
		Policy: &types.TenantPolicy{
			AllowedModels: []string{"other-model"},
		},
	}
	_ = s.tenants.Create(tenant)

	rec := httptest.NewRecorder()
	reqBody := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "local-stub-llm",
		Input:     map[string]any{"text": "hello"},
		Options:   map[string]any{},
	}
	payload, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/models:invoke", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_policy_allow")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for model not in allow list, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInvokeExceedingTokenBudgetReturns403(t *testing.T) {
	s := New("test")

	// Create tenant with very low token budget
	tenant := types.Tenant{
		TenantID: "tnt_budget_test",
		Policy: &types.TenantPolicy{
			TokenBudget: &types.TokenBudget{
				MaxTokensPerHour: 1, // Very low budget
				MaxTokensPerDay:  1,
			},
		},
	}
	_ = s.tenants.Create(tenant)

	// First request should succeed and consume budget
	rec1 := httptest.NewRecorder()
	reqBody := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "local-stub-llm",
		Input:     map[string]any{"text": "hello"},
		Options:   map[string]any{},
	}
	payload, _ := json.Marshal(reqBody)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/models:invoke", bytes.NewReader(payload))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Tenant-Id", "tnt_budget_test")
	req1.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 for first request, got %d body=%s", rec1.Code, rec1.Body.String())
	}

	// Second request should fail due to budget exhaustion
	rec2 := httptest.NewRecorder()
	payload2, _ := json.Marshal(reqBody)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/models:invoke", bytes.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Tenant-Id", "tnt_budget_test")
	req2.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for budget exceeded, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestInvokeAllowedModelWithBudgetSucceeds(t *testing.T) {
	s := New("test")

	// Create tenant with policy that allows the model and has high budget
	tenant := types.Tenant{
		TenantID: "tnt_allowed",
		Policy: &types.TenantPolicy{
			AllowedModels: []string{"local-stub-llm"},
			TokenBudget: &types.TokenBudget{
				MaxTokensPerHour: 100000,
				MaxTokensPerDay:  1000000,
			},
		},
	}
	_ = s.tenants.Create(tenant)

	rec := httptest.NewRecorder()
	reqBody := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "local-stub-llm",
		Input:     map[string]any{"text": "hello"},
		Options:   map[string]any{},
	}
	payload, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/models:invoke", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_allowed")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for allowed model with budget, got %d body=%s", rec.Code, rec.Body.String())
	}
}
