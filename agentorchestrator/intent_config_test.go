package agentorchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIntentConfig_Valid(t *testing.T) {
	cfg, err := LoadIntentConfig("../configs/intent-router.yml")
	if err != nil {
		t.Fatalf("LoadIntentConfig failed: %v", err)
	}
	if cfg.Version != "3.1" {
		t.Errorf("expected version 3.1, got %q", cfg.Version)
	}
	if len(cfg.Intents) != 6 {
		t.Errorf("expected 6 intents, got %d", len(cfg.Intents))
	}
	for _, name := range knownIntents {
		if _, ok := cfg.Intents[name]; !ok {
			t.Errorf("missing required intent %q", name)
		}
	}
}

func TestLoadIntentConfig_ResolvedDefaults(t *testing.T) {
	cfg, err := LoadIntentConfig("../configs/intent-router.yml")
	if err != nil {
		t.Fatalf("LoadIntentConfig failed: %v", err)
	}

	// General intent has no temperature set — should inherit nil (model default)
	gen := cfg.ResolvedIntent(IntentGeneral)
	if gen.Temperature != nil {
		t.Errorf("general intent should have nil temperature, got %v", *gen.Temperature)
	}

	// Code intent should have 0.3
	code := cfg.ResolvedIntent(IntentCode)
	if code.Temperature == nil || *code.Temperature != 0.3 {
		t.Errorf("code intent should have temperature 0.3")
	}

	// Reasoning should have thinking enabled
	reasoning := cfg.ResolvedIntent(IntentReasoning)
	if reasoning.EnableThinking == nil || !*reasoning.EnableThinking {
		t.Error("reasoning intent should have enable_thinking=true")
	}

	// Agent should have destination=agent
	agent := cfg.ResolvedIntent(IntentAgent)
	if agent.Destination != "agent" {
		t.Errorf("agent intent should have destination=agent, got %q", agent.Destination)
	}

	// Ops should have KB enabled with limit 10
	ops := cfg.ResolvedIntent(IntentOps)
	if ops.KBSearchEnabled == nil || !*ops.KBSearchEnabled {
		t.Error("ops intent should have kb_search_enabled=true")
	}
	if ops.KBSearchLimit == nil || *ops.KBSearchLimit != 10 {
		t.Error("ops intent should have kb_search_limit=10")
	}
}

func TestLoadIntentConfig_MissingFile(t *testing.T) {
	_, err := LoadIntentConfig("/nonexistent/path.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadIntentConfig_BadYAML(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(tmp, []byte(":::invalid yaml:::"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadIntentConfig(tmp)
	if err == nil {
		t.Fatal("expected error for bad YAML")
	}
}

func TestLoadIntentConfig_MissingIntent(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "partial.yml")
	content := `
version: "3.1"
intents:
  code:
    destination: model
  general:
    destination: model
`
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadIntentConfig(tmp)
	if err == nil {
		t.Fatal("expected error for missing required intents")
	}
}

func TestLoadIntentConfig_InvalidTemperature(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "badtemp.yml")
	content := `
version: "3.1"
intents:
  code:
    temperature: 5.0
    destination: model
  reasoning:
    destination: model
  ops:
    destination: model
  search:
    destination: model
  agent:
    destination: agent
  general:
    destination: model
`
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadIntentConfig(tmp)
	if err == nil {
		t.Fatal("expected error for temperature out of range")
	}
}

func TestLoadIntentConfig_InvalidDestination(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "baddest.yml")
	content := `
version: "3.1"
intents:
  code:
    destination: model
  reasoning:
    destination: model
  ops:
    destination: model
  search:
    destination: model
  agent:
    destination: somewhere_else
  general:
    destination: model
`
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadIntentConfig(tmp)
	if err == nil {
		t.Fatal("expected error for invalid destination")
	}
}
