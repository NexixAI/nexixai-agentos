package governance

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadConfig reads and parses a governance.yml file from the given path.
// Returns an error if the file is missing, unreadable, or unparseable.
// This is fail-closed: any error prevents engine initialization.
func LoadConfig(path string) (*GovernanceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("governance: failed to read %s: %w", path, err)
	}

	var cfg GovernanceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("governance: failed to parse %s: %w", path, err)
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("governance: invalid config in %s: %w", path, err)
	}

	return &cfg, nil
}

// validateConfig performs basic validation on the loaded governance config.
func validateConfig(cfg *GovernanceConfig) error {
	if cfg.Version == "" {
		return fmt.Errorf("missing version field")
	}
	if len(cfg.Agents) == 0 {
		return fmt.Errorf("no agents defined")
	}
	for name, agent := range cfg.Agents {
		if agent.Role == "" {
			return fmt.Errorf("agent %q has no role", name)
		}
	}
	// Validate that each segmentation key refers to a known agent.
	for name := range cfg.Segments {
		if _, ok := cfg.Agents[name]; !ok {
			return fmt.Errorf("segmentation references unknown agent %q", name)
		}
	}
	return nil
}
