package agentorchestrator

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// IntentName is one of the recognized intent labels.
type IntentName string

const (
	IntentCode      IntentName = "code"
	IntentReasoning IntentName = "reasoning"
	IntentOps       IntentName = "ops"
	IntentSearch    IntentName = "search"
	IntentAgent     IntentName = "agent"
	IntentGeneral   IntentName = "general"
)

var knownIntents = []IntentName{
	IntentCode, IntentReasoning, IntentOps,
	IntentSearch, IntentAgent, IntentGeneral,
}

// IntentConfig is the top-level structure of intent-router.yml.
type IntentConfig struct {
	Version  string                   `yaml:"version"`
	Defaults IntentDefaults           `yaml:"defaults"`
	Intents  map[IntentName]IntentDef `yaml:"intents"`
}

// IntentDefaults are fallback values when an intent omits a field.
type IntentDefaults struct {
	Temperature     *float64 `yaml:"temperature"`
	MaxTokensMult   float64  `yaml:"max_tokens_multiplier"`
	EnableThinking  bool     `yaml:"enable_thinking"`
	KBSearchEnabled bool     `yaml:"kb_search_enabled"`
	KBSearchLimit   int      `yaml:"kb_search_limit"`
}

// IntentDef is the per-intent composition configuration.
type IntentDef struct {
	SystemPrompt    string   `yaml:"system_prompt,omitempty"`
	Temperature     *float64 `yaml:"temperature,omitempty"`
	MaxTokensMult   *float64 `yaml:"max_tokens_multiplier,omitempty"`
	EnableThinking  *bool    `yaml:"enable_thinking,omitempty"`
	KBSearchEnabled *bool    `yaml:"kb_search_enabled,omitempty"`
	KBSearchLimit   *int     `yaml:"kb_search_limit,omitempty"`
	KBScope         string   `yaml:"kb_scope,omitempty"`
	Destination     string   `yaml:"destination,omitempty"` // "model" (default) or "agent"
}

// ResolvedIntent returns the effective values for an intent, merging with defaults.
func (cfg *IntentConfig) ResolvedIntent(name IntentName) IntentDef {
	def, ok := cfg.Intents[name]
	if !ok {
		return IntentDef{Destination: "model"}
	}
	if def.Temperature == nil {
		def.Temperature = cfg.Defaults.Temperature
	}
	if def.MaxTokensMult == nil {
		m := cfg.Defaults.MaxTokensMult
		def.MaxTokensMult = &m
	}
	if def.EnableThinking == nil {
		def.EnableThinking = &cfg.Defaults.EnableThinking
	}
	if def.KBSearchEnabled == nil {
		def.KBSearchEnabled = &cfg.Defaults.KBSearchEnabled
	}
	if def.KBSearchLimit == nil {
		def.KBSearchLimit = &cfg.Defaults.KBSearchLimit
	}
	if def.Destination == "" {
		def.Destination = "model"
	}
	return def
}

// LoadIntentConfig reads and validates an intent-router.yml file.
// Fail-loud: any error prevents startup.
func LoadIntentConfig(path string) (*IntentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("intent config: failed to read %s: %w", path, err)
	}

	var cfg IntentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("intent config: failed to parse %s: %w", path, err)
	}

	if err := validateIntentConfig(&cfg); err != nil {
		return nil, fmt.Errorf("intent config: invalid %s: %w", path, err)
	}

	return &cfg, nil
}

func validateIntentConfig(cfg *IntentConfig) error {
	if cfg.Version == "" {
		return fmt.Errorf("missing version field")
	}
	if len(cfg.Intents) == 0 {
		return fmt.Errorf("no intents defined")
	}
	for _, name := range knownIntents {
		if _, ok := cfg.Intents[name]; !ok {
			return fmt.Errorf("missing required intent %q", name)
		}
	}
	for name, def := range cfg.Intents {
		if def.Temperature != nil && (*def.Temperature < 0 || *def.Temperature > 2) {
			return fmt.Errorf("intent %q: temperature %.1f out of range [0, 2]", name, *def.Temperature)
		}
		if def.MaxTokensMult != nil && *def.MaxTokensMult <= 0 {
			return fmt.Errorf("intent %q: max_tokens_multiplier must be positive", name)
		}
		if def.Destination != "" && def.Destination != "model" && def.Destination != "agent" {
			return fmt.Errorf("intent %q: destination must be 'model' or 'agent', got %q", name, def.Destination)
		}
	}
	return nil
}
