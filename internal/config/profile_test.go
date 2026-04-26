package config

import "testing"

func TestEnsureSafeProfileBlocksDefaultTenantInProd(t *testing.T) {
	t.Setenv("AGENTOS_PROFILE", "prod")
	t.Setenv("AGENTOS_DEFAULT_TENANT", "tnt_demo")
	if err := EnsureSafeProfile(); err == nil {
		t.Fatalf("expected error for default tenant in prod")
	}
}

func TestEnsureSafeProfileAllowsDev(t *testing.T) {
	t.Setenv("AGENTOS_PROFILE", "dev")
	t.Setenv("AGENTOS_DEFAULT_TENANT", "tnt_demo")
	if err := EnsureSafeProfile(); err != nil {
		t.Fatalf("unexpected error in dev: %v", err)
	}
}
