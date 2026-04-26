package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// PostgresConfig holds connection parameters for a PostgreSQL database.
type PostgresConfig struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	SSLMode  string
	PoolSize int
}

// ModelConfig holds model provider configuration loaded from environment variables.
type ModelConfig struct {
	Provider     string        // "stub" or "openai"
	BaseURL      string        // required when Provider is "openai"
	APIKey       string        // API key for the provider
	DefaultModel string        // optional default model name
	Timeout      time.Duration // HTTP request timeout (default 60s)
}

// LoadModelConfig reads AGENTOS_MODEL_* environment variables into a ModelConfig.
func LoadModelConfig() ModelConfig {
	cfg := ModelConfig{
		Provider:     os.Getenv("AGENTOS_MODEL_PROVIDER"),
		BaseURL:      os.Getenv("AGENTOS_MODEL_BASE_URL"),
		APIKey:       os.Getenv("AGENTOS_MODEL_API_KEY"),
		DefaultModel: os.Getenv("AGENTOS_MODEL_DEFAULT"),
		Timeout:      60 * time.Second,
	}

	// AGENTOS_MODEL_API_KEY_FILE takes precedence over AGENTOS_MODEL_API_KEY.
	if keyFile := os.Getenv("AGENTOS_MODEL_API_KEY_FILE"); keyFile != "" {
		if data, err := os.ReadFile(keyFile); err == nil {
			cfg.APIKey = strings.TrimRight(string(data), "\n")
		}
	}

	if v := os.Getenv("AGENTOS_MODEL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}

	return cfg
}

// ValidateModelConfig checks the ModelConfig for logical correctness.
// When Provider is "openai", BaseURL and APIKey are required.
func (c *ModelConfig) Validate() error {
	var problems []string

	switch c.Provider {
	case "":
		problems = append(problems, "AGENTOS_MODEL_PROVIDER is required (valid: openai, anthropic, stub)")
	case "stub":
		// explicit stub is allowed (dev/testing only)
	case "openai":
		if c.BaseURL == "" {
			problems = append(problems, "AGENTOS_MODEL_BASE_URL is required when provider is openai")
		}
		if c.APIKey == "" {
			problems = append(problems, "AGENTOS_MODEL_API_KEY (or AGENTOS_MODEL_API_KEY_FILE) is required when provider is openai")
		}
	case "anthropic":
		if c.APIKey == "" {
			problems = append(problems, "AGENTOS_MODEL_API_KEY (or AGENTOS_MODEL_API_KEY_FILE) is required when provider is anthropic")
		}
	default:
		problems = append(problems, fmt.Sprintf("AGENTOS_MODEL_PROVIDER=%s is unknown; must be stub, openai, or anthropic", c.Provider))
	}

	if c.Timeout <= 0 {
		problems = append(problems, "AGENTOS_MODEL_TIMEOUT must be positive")
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("model configuration invalid: %s", strings.Join(problems, "; "))
}

// ExecConfig holds agent execution engine configuration.
type ExecConfig struct {
	Workers           int           // number of executor worker goroutines
	DefaultMaxSteps   int           // default max steps per run
	DefaultTimeout    time.Duration // default wall-clock timeout per run
	ToolMaxOutputSize int           // max bytes per tool result (default 1MB)
}

// LoadExecConfig reads AGENTOS_EXEC_* environment variables into an ExecConfig.
func LoadExecConfig() ExecConfig {
	cfg := ExecConfig{
		Workers:         5,
		DefaultMaxSteps: 10,
		DefaultTimeout:  300 * time.Second,
	}

	if v := os.Getenv("AGENTOS_EXEC_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Workers = n
		}
	}

	if v := os.Getenv("AGENTOS_EXEC_DEFAULT_MAX_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DefaultMaxSteps = n
		}
	}

	if v := os.Getenv("AGENTOS_EXEC_DEFAULT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.DefaultTimeout = d
		}
	}

	cfg.ToolMaxOutputSize = 1024 * 1024 // 1MB default
	if v := os.Getenv("AGENTOS_EXEC_TOOL_MAX_OUTPUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ToolMaxOutputSize = n
		}
	}

	return cfg
}

// PIIConfig holds PII detection configuration.
type PIIConfig struct {
	Enabled     bool
	DefaultMode string
}

// StorageConfig holds all storage-related configuration loaded from environment variables.
type StorageConfig struct {
	Backend         string
	Postgres        PostgresConfig
	ShutdownTimeout time.Duration
	LogFormat       string
	MaxBodySize     int64

	// Memory store settings.
	MemoryMaxMessages int
	MemoryMaxTokens   int

	// KV store settings.
	KVMaxValueSize    int
	KVMaxKeysPerAgent int

	// PII detection settings.
	PII PIIConfig
}

// LoadFromEnv reads AGENTOS_* environment variables into a StorageConfig,
// applying defaults where values are absent.
func LoadFromEnv() StorageConfig {
	cfg := StorageConfig{
		Backend:           os.Getenv("AGENTOS_STORAGE_BACKEND"),
		ShutdownTimeout:   30 * time.Second,
		LogFormat:         envOrDefault("AGENTOS_LOG_FORMAT", ""),
		MaxBodySize:       1048576,
		MemoryMaxMessages: 50,
		MemoryMaxTokens:   8192,
		KVMaxValueSize:    65536,
		KVMaxKeysPerAgent: 1000,
		Postgres: PostgresConfig{
			Host:     os.Getenv("AGENTOS_DB_HOST"),
			Port:     5432,
			Name:     os.Getenv("AGENTOS_DB_NAME"),
			User:     os.Getenv("AGENTOS_DB_USER"),
			Password: os.Getenv("AGENTOS_DB_PASSWORD"),
			SSLMode:  envOrDefault("AGENTOS_DB_SSLMODE", "require"),
			PoolSize: 10,
		},
	}

	// AGENTOS_DB_PASSWORD_FILE takes precedence over AGENTOS_DB_PASSWORD.
	if passFile := os.Getenv("AGENTOS_DB_PASSWORD_FILE"); passFile != "" {
		if data, err := os.ReadFile(passFile); err == nil {
			cfg.Postgres.Password = strings.TrimRight(string(data), "\n")
		}
	}

	if v := os.Getenv("AGENTOS_DB_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Postgres.Port = n
		}
	}

	if v := os.Getenv("AGENTOS_DB_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Postgres.PoolSize = n
		}
	}

	if v := os.Getenv("AGENTOS_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ShutdownTimeout = d
		}
	}

	if v := os.Getenv("AGENTOS_MAX_BODY_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MaxBodySize = n
		}
	}

	if v := os.Getenv("AGENTOS_MEMORY_MAX_MESSAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MemoryMaxMessages = n
		}
	}

	if v := os.Getenv("AGENTOS_MEMORY_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MemoryMaxTokens = n
		}
	}

	if v := os.Getenv("AGENTOS_KV_MAX_VALUE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.KVMaxValueSize = n
		}
	}

	if v := os.Getenv("AGENTOS_KV_MAX_KEYS_PER_AGENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.KVMaxKeysPerAgent = n
		}
	}

	// PII configuration.
	cfg.PII = PIIConfig{
		Enabled:     strings.ToLower(envOrDefault("AGENTOS_PII_ENABLED", "true")) == "true",
		DefaultMode: envOrDefault("AGENTOS_PII_DEFAULT_MODE", "warn"),
	}

	return cfg
}

// Validate checks the StorageConfig for logical correctness.
// When Backend is "postgres", database connection fields are required.
// All detected problems are collected and returned as a single error.
func (c *StorageConfig) Validate() error {
	var problems []string

	if strings.ToLower(c.Backend) == "postgres" {
		if c.Postgres.Host == "" {
			problems = append(problems, "AGENTOS_DB_HOST is required when backend is postgres")
		}
		if c.Postgres.Name == "" {
			problems = append(problems, "AGENTOS_DB_NAME is required when backend is postgres")
		}
		if c.Postgres.User == "" {
			problems = append(problems, "AGENTOS_DB_USER is required when backend is postgres")
		}
		if c.Postgres.Password == "" {
			problems = append(problems, "AGENTOS_DB_PASSWORD (or AGENTOS_DB_PASSWORD_FILE) is required when backend is postgres")
		}
	}

	if c.Postgres.Port < 1 || c.Postgres.Port > 65535 {
		problems = append(problems, fmt.Sprintf("AGENTOS_DB_PORT=%d is invalid; must be 1-65535", c.Postgres.Port))
	}

	if c.Postgres.PoolSize < 1 {
		problems = append(problems, fmt.Sprintf("AGENTOS_DB_POOL_SIZE=%d is invalid; must be > 0", c.Postgres.PoolSize))
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("configuration invalid: %s", strings.Join(problems, "; "))
}

// MCPConfig holds MCP (Model Context Protocol) server configuration.
type MCPConfig struct {
	Enabled bool
	Port    int
}

// LoadMCPConfig reads AGENTOS_MCP_* environment variables into an MCPConfig.
func LoadMCPConfig() MCPConfig {
	cfg := MCPConfig{
		Enabled: false,
		Port:    9092,
	}

	if v := os.Getenv("AGENTOS_MCP_ENABLED"); v != "" {
		cfg.Enabled = strings.ToLower(v) == "true"
	}

	if v := os.Getenv("AGENTOS_MCP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			cfg.Port = n
		}
	}

	return cfg
}

// KBConfig holds configuration for the nexixai-kb knowledge base service.
type KBConfig struct {
	URL    string // Base URL for the KB service HTTP API.
	APIKey string // Optional Bearer token for authenticated KB access.
}

// LoadKBConfig reads the KB service URL plus optional auth token.
// Default: http://nexixai-kb:9093
func LoadKBConfig() KBConfig {
	return KBConfig{
		URL:    envOrDefault("AGENTOS_KB_URL", "http://nexixai-kb:9093"),
		APIKey: envOrDefault("AGENTOS_KB_API_KEY", os.Getenv("KB_API_KEY")),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
