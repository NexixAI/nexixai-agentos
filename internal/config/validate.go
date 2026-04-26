package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ValidateServiceConfig enforces prod-mode requirements for each service and fails fast on unsafe defaults.
// It returns nil when the current profile is non-prod or when all required settings are satisfied.
func ValidateServiceConfig(service string) error {
	profile := CurrentProfile()
	if profile != ProfileProd {
		return nil
	}

	service = strings.ToLower(strings.TrimSpace(service))
	var problems []string

	if strings.TrimSpace(os.Getenv("AGENTOS_METRICS_REQUIRE_AUTH")) != "1" {
		problems = append(problems, "AGENTOS_METRICS_REQUIRE_AUTH must be set to 1 in prod to protect /metrics")
	}

	if service == "federation" {
		peersPath := strings.TrimSpace(os.Getenv("AGENTOS_PEERS_FILE"))
		if peersPath == "" {
			problems = append(problems, "AGENTOS_PEERS_FILE is required in prod (path to peer registry JSON)")
		} else if _, err := os.Stat(peersPath); err != nil {
			problems = append(problems, fmt.Sprintf("AGENTOS_PEERS_FILE=%s not readable: %v", peersPath, err))
		}
	}

	if len(problems) == 0 {
		return nil
	}

	msg := fmt.Sprintf("configuration invalid for profile=prod (service=%s): %s", service, strings.Join(problems, "; "))
	return errors.New(msg)
}
