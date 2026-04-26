package storage

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

var fileBackendWarningOnce sync.Once

// OpenSharedDB opens a single *sql.DB connection pool for the given postgres
// config. Callers can pass the returned pool to the various New*FromDB store
// constructors to avoid creating independent pools per store (v9.0 M-9).
func OpenSharedDB(cfg config.PostgresConfig) (*sql.DB, error) {
	return postgres.OpenDB(cfg)
}

// warnFileBackend logs a one-time warning when file-based storage is used.
func warnFileBackend(store string) {
	fileBackendWarningOnce.Do(func() {
		slog.Warn("file-based storage backend is not recommended for production",
			"store", store,
			"recommendation", "set AGENTOS_STORAGE_BACKEND=postgres for production deployments",
			"migration", "use 'agentos migrate file-to-postgres' to migrate existing data",
		)
	})
}

// NewRunStore returns a FullRunStore implementation based on the storage backend
// specified in cfg. Supported backends: "file" (default), "postgres".
func NewRunStore(cfg config.StorageConfig) (FullRunStore, error) {
	switch strings.ToLower(cfg.Backend) {
	case "postgres":
		return postgres.NewRunStore(cfg.Postgres)
	case "file", "":
		warnFileBackend("RunStore")
		path := strings.TrimSpace(os.Getenv("AGENTOS_RUN_STORE_FILE"))
		if path == "" {
			path = filepath.Join("data", "agent-orchestrator", "runs.json")
		}
		return NewFileRunStore(path)
	default:
		return nil, fmt.Errorf("unknown storage backend: %q", cfg.Backend)
	}
}

// NewAgentStore returns an AgentStore implementation based on the storage backend
// specified in cfg. Supported backends: "file" (default), "postgres".
func NewAgentStore(cfg config.StorageConfig) (AgentStore, error) {
	switch strings.ToLower(cfg.Backend) {
	case "postgres":
		return postgres.NewAgentStore(cfg.Postgres)
	case "file", "":
		dir := strings.TrimSpace(os.Getenv("AGENTOS_AGENT_STORE_DIR"))
		if dir == "" {
			dir = filepath.Join("data", "agents")
		}
		return NewFileAgentStore(dir)
	default:
		return nil, fmt.Errorf("unknown storage backend: %q", cfg.Backend)
	}
}

// NewMemoryStore returns a MemoryStore implementation based on the storage backend
// specified in cfg. Supported backends: "file" (default), "postgres".
func NewMemoryStore(cfg config.StorageConfig) (MemoryStore, error) {
	switch strings.ToLower(cfg.Backend) {
	case "postgres":
		return postgres.NewMemoryStore(cfg.Postgres)
	case "file", "":
		dir := strings.TrimSpace(os.Getenv("AGENTOS_MEMORY_STORE_DIR"))
		if dir == "" {
			dir = filepath.Join("data", "memory")
		}
		return NewFileMemoryStore(dir)
	default:
		return nil, fmt.Errorf("unknown storage backend: %q", cfg.Backend)
	}
}

// NewUsageStore returns a UsageStore when Postgres backend is configured.
// Returns nil (no persistence) for file backend — quota stays in-memory.
func NewUsageStore(cfg config.StorageConfig) (*postgres.UsageStore, error) {
	if strings.ToLower(cfg.Backend) == "postgres" {
		return postgres.NewUsageStore(cfg.Postgres)
	}
	return nil, nil
}

// NewEventLogStore returns an EventLogStore implementation based on the storage backend.
// Supported backends: "file" (default), "postgres".
func NewEventLogStore(cfg config.StorageConfig, dataDir string) (EventLogStore, error) {
	switch strings.ToLower(cfg.Backend) {
	case "postgres":
		return postgres.NewEventLogStore(cfg.Postgres)
	case "file", "":
		return NewFileEventLogStore(dataDir)
	default:
		return nil, fmt.Errorf("unknown storage backend: %q", cfg.Backend)
	}
}

// NewKVStore returns a KVStore implementation based on the storage backend
// specified in cfg. Supported backends: "file" (default), "postgres".
func NewKVStore(cfg config.StorageConfig) (KVStore, error) {
	switch strings.ToLower(cfg.Backend) {
	case "postgres":
		return postgres.NewKVStore(cfg.Postgres, cfg.KVMaxValueSize, cfg.KVMaxKeysPerAgent)
	case "file", "":
		dir := strings.TrimSpace(os.Getenv("AGENTOS_KV_STORE_DIR"))
		if dir == "" {
			dir = filepath.Join("data", "kv")
		}
		return NewFileKVStore(dir, cfg.KVMaxValueSize, cfg.KVMaxKeysPerAgent)
	default:
		return nil, fmt.Errorf("unknown storage backend: %q", cfg.Backend)
	}
}
