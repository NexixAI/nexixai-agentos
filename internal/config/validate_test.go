package config

import (
	"os"
	"strings"
	"testing"
)

func TestValidateServiceConfigNonProdNoop(t *testing.T) {
	t.Setenv("AGENTOS_PROFILE", "dev")
	t.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "")
	if err := ValidateServiceConfig("federation"); err != nil {
		t.Fatalf("expected no validation in non-prod, got %v", err)
	}
}

func TestValidateServiceConfigRequiresMetricsAuthInProd(t *testing.T) {
	t.Setenv("AGENTOS_PROFILE", "prod")
	t.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "")
	err := ValidateServiceConfig("agent-orchestrator")
	if err == nil || !strings.Contains(err.Error(), "AGENTOS_METRICS_REQUIRE_AUTH") {
		t.Fatalf("expected metrics auth failure, got %v", err)
	}
}

func TestValidateServiceConfigRequiresPeersFileInProd(t *testing.T) {
	t.Setenv("AGENTOS_PROFILE", "prod")
	t.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	t.Setenv("AGENTOS_PEERS_FILE", "")

	err := ValidateServiceConfig("federation")
	if err == nil || !strings.Contains(err.Error(), "AGENTOS_PEERS_FILE is required") {
		t.Fatalf("expected peers file requirement, got %v", err)
	}
}

func TestValidateServiceConfigPeersFileMustExist(t *testing.T) {
	t.Setenv("AGENTOS_PROFILE", "prod")
	t.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	t.Setenv("AGENTOS_PEERS_FILE", "/no/such/file.json")

	err := ValidateServiceConfig("federation")
	if err == nil || !strings.Contains(err.Error(), "AGENTOS_PEERS_FILE") {
		t.Fatalf("expected peers file existence error, got %v", err)
	}
}

func TestValidateServiceConfigPeersFileReadable(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "peers-*.json")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	_ = tmp.Close()

	t.Setenv("AGENTOS_PROFILE", "prod")
	t.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", "1")
	t.Setenv("AGENTOS_PEERS_FILE", tmp.Name())

	if err := ValidateServiceConfig("federation"); err != nil {
		t.Fatalf("expected valid configuration, got %v", err)
	}
}
