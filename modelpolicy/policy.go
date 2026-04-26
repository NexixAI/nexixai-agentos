package modelpolicy

import (
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

type policyEngine struct{}

func newPolicyEngine() *policyEngine {
	return &policyEngine{}
}

func (p *policyEngine) Evaluate(tenantID string, ac auth.AuthContext, req types.ModelInvokeRequest, policy *types.TenantPolicy) (string, []string) {
	if denies := denyReasons(req); len(denies) > 0 {
		return "deny", denies
	}
	if len(ac.Scopes) > 0 && !hasScope(ac.Scopes, "models:invoke") {
		return "deny", []string{"scope_missing:models:invoke"}
	}
	if tenantID == "" {
		return "deny", []string{"tenant_missing"}
	}

	// Check tenant policy for model access
	if policy != nil {
		// Deny list takes precedence
		if len(policy.DeniedModels) > 0 && containsModel(policy.DeniedModels, req.ModelID) {
			return "deny", []string{"model_denied:" + req.ModelID}
		}
		// Allow list: if non-empty, model must be in the list
		if len(policy.AllowedModels) > 0 && !containsModel(policy.AllowedModels, req.ModelID) {
			return "deny", []string{"model_not_allowed:" + req.ModelID}
		}
	}

	return "allow", []string{"policy_allow"}
}

// containsModel checks if modelID is in the list (case-insensitive).
func containsModel(list []string, modelID string) bool {
	for _, m := range list {
		if strings.EqualFold(m, modelID) {
			return true
		}
	}
	return false
}

func (p *policyEngine) EvaluatePolicyCheck(tenantID string, ac auth.AuthContext, req types.PolicyCheckRequest) (string, []string) {
	if tenantID == "" {
		return "deny", []string{"tenant_missing"}
	}
	if strings.EqualFold(req.Action, "deny") {
		return "deny", []string{"explicit_deny_action"}
	}
	if len(ac.Scopes) > 0 && !hasScope(ac.Scopes, "policy:check") {
		return "deny", []string{"scope_missing:policy:check"}
	}
	return "allow", []string{"policy_allow"}
}

func denyReasons(req types.ModelInvokeRequest) []string {
	reasons := []string{}
	if val, ok := req.Options["deny"].(bool); ok && val {
		reasons = append(reasons, "option_deny=true")
	}
	if strings.EqualFold(req.Operation, "deny") || strings.EqualFold(req.Operation, "block") {
		reasons = append(reasons, "operation_blocked")
	}
	return reasons
}

func hasScope(scopes []string, target string) bool {
	for _, s := range scopes {
		if strings.EqualFold(strings.TrimSpace(s), target) {
			return true
		}
	}
	return false
}
