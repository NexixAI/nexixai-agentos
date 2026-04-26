package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// clearEnv unsets all AGENTOS_* env vars used by LoadFromEnv to guarantee test isolation.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AGENTOS_STORAGE_BACKEND",
		"AGENTOS_DB_HOST",
		"AGENTOS_DB_PORT",
		"AGENTOS_DB_NAME",
		"AGENTOS_DB_USER",
		"AGENTOS_DB_PASSWORD",
		"AGENTOS_DB_PASSWORD_FILE",
		"AGENTOS_DB_SSLMODE",
		"AGENTOS_DB_POOL_SIZE",
		"AGENTOS_SHUTDOWN_TIMEOUT",
		"AGENTOS_LOG_FORMAT",
		"AGENTOS_MAX_BODY_SIZE",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

func TestLoadFromEnv_Defaults(t *testing.T) {
	clearEnv(t)

	cfg := LoadFromEnv()

	if cfg.Backend != "" {
		t.Errorf("Backend: got %q, want empty", cfg.Backend)
	}
	if cfg.Postgres.Port != 5432 {
		t.Errorf("Port: got %d, want 5432", cfg.Postgres.Port)
	}
	if cfg.Postgres.PoolSize != 10 {
		t.Errorf("PoolSize: got %d, want 10", cfg.Postgres.PoolSize)
	}
	if cfg.Postgres.SSLMode != "require" {
		t.Errorf("SSLMode: got %q, want %q", cfg.Postgres.SSLMode, "require")
	}
	if cfg.ShutdownTimeout.Seconds() != 30 {
		t.Errorf("ShutdownTimeout: got %v, want 30s", cfg.ShutdownTimeout)
	}
	if cfg.MaxBodySize != 1048576 {
		t.Errorf("MaxBodySize: got %d, want 1048576", cfg.MaxBodySize)
	}
}

func TestLoadFromEnv_AllVars(t *testing.T) {
	clearEnv(t)

	t.Setenv("AGENTOS_STORAGE_BACKEND", "postgres")
	t.Setenv("AGENTOS_DB_HOST", "db.example.com")
	t.Setenv("AGENTOS_DB_PORT", "5433")
	t.Setenv("AGENTOS_DB_NAME", "mydb")
	t.Setenv("AGENTOS_DB_USER", "admin")
	t.Setenv("AGENTOS_DB_PASSWORD", "secret")
	t.Setenv("AGENTOS_DB_SSLMODE", "disable")
	t.Setenv("AGENTOS_DB_POOL_SIZE", "20")
	t.Setenv("AGENTOS_SHUTDOWN_TIMEOUT", "10s")
	t.Setenv("AGENTOS_LOG_FORMAT", "json")
	t.Setenv("AGENTOS_MAX_BODY_SIZE", "2097152")

	cfg := LoadFromEnv()

	if cfg.Backend != "postgres" {
		t.Errorf("Backend: got %q, want %q", cfg.Backend, "postgres")
	}
	if cfg.Postgres.Host != "db.example.com" {
		t.Errorf("Host: got %q", cfg.Postgres.Host)
	}
	if cfg.Postgres.Port != 5433 {
		t.Errorf("Port: got %d, want 5433", cfg.Postgres.Port)
	}
	if cfg.Postgres.Name != "mydb" {
		t.Errorf("Name: got %q", cfg.Postgres.Name)
	}
	if cfg.Postgres.User != "admin" {
		t.Errorf("User: got %q", cfg.Postgres.User)
	}
	if cfg.Postgres.Password != "secret" {
		t.Errorf("Password: got %q", cfg.Postgres.Password)
	}
	if cfg.Postgres.SSLMode != "disable" {
		t.Errorf("SSLMode: got %q", cfg.Postgres.SSLMode)
	}
	if cfg.Postgres.PoolSize != 20 {
		t.Errorf("PoolSize: got %d, want 20", cfg.Postgres.PoolSize)
	}
	if cfg.ShutdownTimeout.Seconds() != 10 {
		t.Errorf("ShutdownTimeout: got %v", cfg.ShutdownTimeout)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat: got %q", cfg.LogFormat)
	}
	if cfg.MaxBodySize != 2097152 {
		t.Errorf("MaxBodySize: got %d", cfg.MaxBodySize)
	}
}

func TestLoadFromEnv_PasswordFile(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	passFile := filepath.Join(dir, "db_password")
	if err := os.WriteFile(passFile, []byte("file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Set both; file should win.
	t.Setenv("AGENTOS_DB_PASSWORD", "env-secret")
	t.Setenv("AGENTOS_DB_PASSWORD_FILE", passFile)

	cfg := LoadFromEnv()

	if cfg.Postgres.Password != "file-secret" {
		t.Errorf("Password: got %q, want %q (file should take precedence)", cfg.Postgres.Password, "file-secret")
	}
}

func TestLoadFromEnv_PasswordFile_NotFound(t *testing.T) {
	clearEnv(t)

	t.Setenv("AGENTOS_DB_PASSWORD", "env-secret")
	t.Setenv("AGENTOS_DB_PASSWORD_FILE", "/nonexistent/path/password")

	cfg := LoadFromEnv()

	// When the file cannot be read, fall back to the env var value.
	if cfg.Postgres.Password != "env-secret" {
		t.Errorf("Password: got %q, want %q (should fall back to env var)", cfg.Postgres.Password, "env-secret")
	}
}

func TestValidate_ValidPostgres(t *testing.T) {
	cfg := StorageConfig{
		Backend: "postgres",
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			Name:     "testdb",
			User:     "user",
			Password: "pass",
			SSLMode:  "require",
			PoolSize: 10,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_NonPostgresSkipsDBChecks(t *testing.T) {
	cfg := StorageConfig{
		Backend: "",
		Postgres: PostgresConfig{
			Port:     5432,
			PoolSize: 10,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error for file backend: %v", err)
	}
}

func TestValidate_PostgresMissingFields(t *testing.T) {
	cfg := StorageConfig{
		Backend: "postgres",
		Postgres: PostgresConfig{
			Port:     5432,
			PoolSize: 10,
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	msg := err.Error()
	for _, want := range []string{
		"AGENTOS_DB_HOST",
		"AGENTOS_DB_NAME",
		"AGENTOS_DB_USER",
		"AGENTOS_DB_PASSWORD",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %s; got: %s", want, msg)
		}
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too_large", 70000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := StorageConfig{
				Postgres: PostgresConfig{
					Port:     tc.port,
					PoolSize: 10,
				},
			}
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "AGENTOS_DB_PORT") {
				t.Errorf("error should mention AGENTOS_DB_PORT; got: %s", err.Error())
			}
			if !strings.Contains(err.Error(), strconv.Itoa(tc.port)) {
				t.Errorf("error should include invalid value %d; got: %s", tc.port, err.Error())
			}
		})
	}
}

func TestValidate_InvalidPoolSize(t *testing.T) {
	cfg := StorageConfig{
		Postgres: PostgresConfig{
			Port:     5432,
			PoolSize: 0,
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "AGENTOS_DB_POOL_SIZE") {
		t.Errorf("error should mention AGENTOS_DB_POOL_SIZE; got: %s", err.Error())
	}
}

func TestValidate_CollectsAllErrors(t *testing.T) {
	cfg := StorageConfig{
		Backend: "postgres",
		Postgres: PostgresConfig{
			Port:     0,
			PoolSize: -1,
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	msg := err.Error()
	// Should contain all postgres-required fields AND port AND pool size errors.
	for _, substr := range []string{
		"AGENTOS_DB_HOST",
		"AGENTOS_DB_NAME",
		"AGENTOS_DB_USER",
		"AGENTOS_DB_PASSWORD",
		"AGENTOS_DB_PORT",
		"AGENTOS_DB_POOL_SIZE",
	} {
		if !strings.Contains(msg, substr) {
			t.Errorf("error should mention %s; got: %s", substr, msg)
		}
	}
}

func TestValidate_PortBoundaries(t *testing.T) {
	// Port 1 and 65535 should be valid.
	for _, port := range []int{1, 65535} {
		cfg := StorageConfig{
			Postgres: PostgresConfig{
				Port:     port,
				PoolSize: 1,
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("port %d should be valid; got: %v", port, err)
		}
	}
}

func TestLoadFromEnv_InvalidNumericDefaults(t *testing.T) {
	clearEnv(t)

	// Non-numeric values should leave defaults in place.
	t.Setenv("AGENTOS_DB_PORT", "abc")
	t.Setenv("AGENTOS_DB_POOL_SIZE", "xyz")
	t.Setenv("AGENTOS_MAX_BODY_SIZE", "nope")

	cfg := LoadFromEnv()

	if cfg.Postgres.Port != 5432 {
		t.Errorf("Port: got %d, want 5432 (default on parse error)", cfg.Postgres.Port)
	}
	if cfg.Postgres.PoolSize != 10 {
		t.Errorf("PoolSize: got %d, want 10 (default on parse error)", cfg.Postgres.PoolSize)
	}
	if cfg.MaxBodySize != 1048576 {
		t.Errorf("MaxBodySize: got %d, want 1048576 (default on parse error)", cfg.MaxBodySize)
	}
}
