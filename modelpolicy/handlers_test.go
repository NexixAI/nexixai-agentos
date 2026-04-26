package modelpolicy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// --- Policy engine unit tests ---

func TestPolicyEngineAllowValidRequest(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1", PrincipalID: "usr_1"}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
		Input:     map[string]any{"text": "hello"},
	}

	decision, reasons := pe.Evaluate("tnt_1", ac, req, nil)
	if decision != "allow" {
		t.Fatalf("expected allow, got %s reasons=%v", decision, reasons)
	}
}

func TestPolicyEngineDenyMissingTenant(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
	}

	decision, reasons := pe.Evaluate("", ac, req, nil)
	if decision != "deny" {
		t.Fatalf("expected deny for missing tenant, got %s", decision)
	}
	found := false
	for _, r := range reasons {
		if r == "tenant_missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tenant_missing reason, got %v", reasons)
	}
}

func TestPolicyEngineDenyOptionDeny(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1"}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
		Options:   map[string]any{"deny": true},
	}

	decision, _ := pe.Evaluate("tnt_1", ac, req, nil)
	if decision != "deny" {
		t.Fatal("expected deny when options.deny=true")
	}
}

func TestPolicyEngineDenyBlockedOperation(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1"}
	req := types.ModelInvokeRequest{
		Operation: "block",
		ModelID:   "gpt-4",
	}

	decision, _ := pe.Evaluate("tnt_1", ac, req, nil)
	if decision != "deny" {
		t.Fatal("expected deny for blocked operation")
	}
}

func TestPolicyEngineDenyMissingScope(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{
		TenantID: "tnt_1",
		Scopes:   []string{"other:scope"},
	}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
	}

	decision, reasons := pe.Evaluate("tnt_1", ac, req, nil)
	if decision != "deny" {
		t.Fatalf("expected deny for missing scope, got %s", decision)
	}
	found := false
	for _, r := range reasons {
		if r == "scope_missing:models:invoke" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scope_missing reason, got %v", reasons)
	}
}

func TestPolicyEngineAllowWithCorrectScope(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{
		TenantID: "tnt_1",
		Scopes:   []string{"models:invoke"},
	}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
	}

	decision, _ := pe.Evaluate("tnt_1", ac, req, nil)
	if decision != "allow" {
		t.Fatal("expected allow with correct scope")
	}
}

func TestPolicyEngineDenyModelOnDenyList(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1"}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
	}
	policy := &types.TenantPolicy{
		DeniedModels: []string{"gpt-4"},
	}

	decision, reasons := pe.Evaluate("tnt_1", ac, req, policy)
	if decision != "deny" {
		t.Fatalf("expected deny for denied model, got %s", decision)
	}
	if len(reasons) == 0 || reasons[0] != "model_denied:gpt-4" {
		t.Fatalf("expected model_denied reason, got %v", reasons)
	}
}

func TestPolicyEngineDenyModelNotInAllowList(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1"}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
	}
	policy := &types.TenantPolicy{
		AllowedModels: []string{"claude-3"},
	}

	decision, reasons := pe.Evaluate("tnt_1", ac, req, policy)
	if decision != "deny" {
		t.Fatalf("expected deny for model not in allow list, got %s", decision)
	}
	if len(reasons) == 0 || reasons[0] != "model_not_allowed:gpt-4" {
		t.Fatalf("expected model_not_allowed reason, got %v", reasons)
	}
}

func TestPolicyEngineAllowModelInAllowList(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1"}
	req := types.ModelInvokeRequest{
		Operation: "chat",
		ModelID:   "gpt-4",
	}
	policy := &types.TenantPolicy{
		AllowedModels: []string{"gpt-4", "claude-3"},
	}

	decision, _ := pe.Evaluate("tnt_1", ac, req, policy)
	if decision != "allow" {
		t.Fatal("expected allow for model in allow list")
	}
}

// --- Policy check endpoint unit tests ---

func TestPolicyCheckEvaluateAllow(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1"}
	req := types.PolicyCheckRequest{
		Action:   "invoke",
		Resource: map[string]any{"model_id": "gpt-4"},
	}

	decision, _ := pe.EvaluatePolicyCheck("tnt_1", ac, req)
	if decision != "allow" {
		t.Fatal("expected allow for valid policy check")
	}
}

func TestPolicyCheckEvaluateDenyExplicit(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{TenantID: "tnt_1"}
	req := types.PolicyCheckRequest{
		Action: "deny",
	}

	decision, reasons := pe.EvaluatePolicyCheck("tnt_1", ac, req)
	if decision != "deny" {
		t.Fatal("expected deny for explicit deny action")
	}
	found := false
	for _, r := range reasons {
		if r == "explicit_deny_action" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected explicit_deny_action reason, got %v", reasons)
	}
}

func TestPolicyCheckEvaluateDenyMissingTenant(t *testing.T) {
	pe := newPolicyEngine()
	ac := auth.AuthContext{}
	req := types.PolicyCheckRequest{Action: "invoke"}

	decision, _ := pe.EvaluatePolicyCheck("", ac, req)
	if decision != "deny" {
		t.Fatal("expected deny for missing tenant")
	}
}

// --- Usage meter unit tests ---

func TestUsageMeterRecordAndGetUsage(t *testing.T) {
	m := newUsageMeter()

	m.Record("tnt_1", map[string]any{
		"prompt_tokens":     10,
		"completion_tokens": 20,
		"total_tokens":      30,
	})

	hourly, daily := m.GetUsage("tnt_1")
	if hourly != 30 {
		t.Fatalf("expected hourly usage 30, got %d", hourly)
	}
	if daily != 30 {
		t.Fatalf("expected daily usage 30, got %d", daily)
	}
}

func TestUsageMeterRecordAccumulates(t *testing.T) {
	m := newUsageMeter()

	m.Record("tnt_1", map[string]any{"total_tokens": 10})
	m.Record("tnt_1", map[string]any{"total_tokens": 25})

	hourly, daily := m.GetUsage("tnt_1")
	if hourly != 35 {
		t.Fatalf("expected accumulated hourly 35, got %d", hourly)
	}
	if daily != 35 {
		t.Fatalf("expected accumulated daily 35, got %d", daily)
	}
}

func TestUsageMeterRecordIgnoresEmptyTenant(t *testing.T) {
	m := newUsageMeter()
	m.Record("", map[string]any{"total_tokens": 10})

	// Should not panic and should have no data
	hourly, daily := m.GetUsage("")
	if hourly != 0 || daily != 0 {
		t.Fatalf("expected zero usage for empty tenant, got hourly=%d daily=%d", hourly, daily)
	}
}

func TestUsageMeterRecordIgnoresNilUsage(t *testing.T) {
	m := newUsageMeter()
	m.Record("tnt_1", nil)

	hourly, daily := m.GetUsage("tnt_1")
	if hourly != 0 || daily != 0 {
		t.Fatalf("expected zero usage for nil usage map, got hourly=%d daily=%d", hourly, daily)
	}
}

func TestUsageMeterCheckBudgetNilBudget(t *testing.T) {
	m := newUsageMeter()
	allowed, reason := m.CheckBudget("tnt_1", nil)
	if !allowed {
		t.Fatalf("expected allowed with nil budget, got reason=%s", reason)
	}
}

func TestUsageMeterCheckBudgetWithinLimits(t *testing.T) {
	m := newUsageMeter()
	m.Record("tnt_1", map[string]any{"total_tokens": 50})

	allowed, _ := m.CheckBudget("tnt_1", &types.TokenBudget{
		MaxTokensPerHour: 1000,
		MaxTokensPerDay:  10000,
	})
	if !allowed {
		t.Fatal("expected allowed within budget")
	}
}

func TestUsageMeterCheckBudgetHourlyExceeded(t *testing.T) {
	m := newUsageMeter()
	m.Record("tnt_1", map[string]any{"total_tokens": 100})

	allowed, reason := m.CheckBudget("tnt_1", &types.TokenBudget{
		MaxTokensPerHour: 50,
		MaxTokensPerDay:  10000,
	})
	if allowed {
		t.Fatal("expected denied for hourly budget exceeded")
	}
	if reason != "hourly_token_budget_exceeded" {
		t.Fatalf("expected hourly reason, got %s", reason)
	}
}

func TestUsageMeterCheckBudgetDailyExceeded(t *testing.T) {
	m := newUsageMeter()
	m.Record("tnt_1", map[string]any{"total_tokens": 500})

	allowed, reason := m.CheckBudget("tnt_1", &types.TokenBudget{
		MaxTokensPerHour: 10000,
		MaxTokensPerDay:  100,
	})
	if allowed {
		t.Fatal("expected denied for daily budget exceeded")
	}
	if reason != "daily_token_budget_exceeded" {
		t.Fatalf("expected daily reason, got %s", reason)
	}
}

// --- Handler-level tests ---

func TestPolicyCheckHandlerValid(t *testing.T) {
	s := New("test")
	body := types.PolicyCheckRequest{
		Action:   "invoke",
		Resource: map[string]any{"model_id": "gpt-4"},
	}
	payload, _ := json.Marshal(body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/policy:check", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp types.PolicyCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Decision != "allow" {
		t.Fatalf("expected allow decision, got %s", resp.Decision)
	}
}

func TestPolicyCheckHandlerDenyAction(t *testing.T) {
	s := New("test")
	body := types.PolicyCheckRequest{
		Action: "deny",
	}
	payload, _ := json.Marshal(body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/policy:check", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp types.PolicyCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Decision != "deny" {
		t.Fatalf("expected deny decision, got %s", resp.Decision)
	}
}

func TestPolicyCheckHandlerInvalidJSON(t *testing.T) {
	s := New("test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/policy:check", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPolicyCheckHandlerMethodNotAllowed(t *testing.T) {
	s := New("test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/policy:check", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", rec.Code)
	}
}

func TestModelsListEndpoint(t *testing.T) {
	s := New("test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Tenant-Id", "tnt_test")
	req.Header.Set("X-Principal-Id", "usr_test")

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for models list, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp types.ModelsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Should have at least the stub model
	if len(resp.Models) == 0 {
		t.Fatal("expected at least one model in list")
	}
}

func TestHealthEndpointModelPolicy(t *testing.T) {
	s := New("test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for health, got %d body=%s", rec.Code, rec.Body.String())
	}
}
