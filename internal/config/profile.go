package config

import (
	"errors"
	"os"
	"strings"
)

const (
	ProfileDev  = "dev"
	ProfileDemo = "demo"
	ProfileProd = "prod"
)

// CurrentProfile returns the active profile (dev|demo|prod), defaulting to dev.
func CurrentProfile() string {
	p := strings.TrimSpace(os.Getenv("AGENTOS_PROFILE"))
	switch strings.ToLower(p) {
	case ProfileDemo:
		return ProfileDemo
	case ProfileProd:
		return ProfileProd
	default:
		return ProfileDev
	}
}

// EnsureSafeProfile enforces production safety guards.
// In prod: disallow default tenant shortcuts and dev header bypass.
func EnsureSafeProfile() error {
	if CurrentProfile() != ProfileProd {
		return nil
	}
	if dt := strings.TrimSpace(os.Getenv("AGENTOS_DEFAULT_TENANT")); dt != "" {
		return errors.New("unsafe prod config: AGENTOS_DEFAULT_TENANT must be empty")
	}
	if strings.TrimSpace(os.Getenv("AGENTOS_ALLOW_DEV_HEADERS")) == "1" {
		return errors.New("unsafe prod config: AGENTOS_ALLOW_DEV_HEADERS must be disabled")
	}
	return nil
}
