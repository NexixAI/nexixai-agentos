package storage

import (
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/config"
)

func TestNewRunStore_FileBackend(t *testing.T) {
	t.Setenv("AGENTOS_RUN_STORE_FILE", t.TempDir()+"/runs.json")
	cfg := config.StorageConfig{Backend: "file"}
	store, err := NewRunStore(cfg)
	if err != nil {
		t.Fatalf("NewRunStore(file): %v", err)
	}
	if _, ok := store.(*fileRunStore); !ok {
		t.Fatalf("expected *fileRunStore, got %T", store)
	}
}

func TestNewRunStore_DefaultBackendIsFile(t *testing.T) {
	t.Setenv("AGENTOS_RUN_STORE_FILE", t.TempDir()+"/runs.json")
	cfg := config.StorageConfig{Backend: ""}
	store, err := NewRunStore(cfg)
	if err != nil {
		t.Fatalf("NewRunStore(default): %v", err)
	}
	if _, ok := store.(*fileRunStore); !ok {
		t.Fatalf("expected *fileRunStore for empty backend, got %T", store)
	}
}

func TestNewRunStore_InvalidBackend(t *testing.T) {
	cfg := config.StorageConfig{Backend: "redis"}
	_, err := NewRunStore(cfg)
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestNewRunStore_CaseInsensitive(t *testing.T) {
	t.Setenv("AGENTOS_RUN_STORE_FILE", t.TempDir()+"/runs.json")
	cfg := config.StorageConfig{Backend: "FILE"}
	store, err := NewRunStore(cfg)
	if err != nil {
		t.Fatalf("NewRunStore(FILE): %v", err)
	}
	if _, ok := store.(*fileRunStore); !ok {
		t.Fatalf("expected *fileRunStore for uppercase FILE, got %T", store)
	}
}

func TestNewAgentStore_FileBackend(t *testing.T) {
	t.Setenv("AGENTOS_AGENT_STORE_DIR", t.TempDir())
	cfg := config.StorageConfig{Backend: "file"}
	store, err := NewAgentStore(cfg)
	if err != nil {
		t.Fatalf("NewAgentStore(file): %v", err)
	}
	if _, ok := store.(*fileAgentStore); !ok {
		t.Fatalf("expected *fileAgentStore, got %T", store)
	}
}

func TestNewAgentStore_DefaultBackendIsFile(t *testing.T) {
	t.Setenv("AGENTOS_AGENT_STORE_DIR", t.TempDir())
	cfg := config.StorageConfig{Backend: ""}
	store, err := NewAgentStore(cfg)
	if err != nil {
		t.Fatalf("NewAgentStore(default): %v", err)
	}
	if _, ok := store.(*fileAgentStore); !ok {
		t.Fatalf("expected *fileAgentStore for empty backend, got %T", store)
	}
}

func TestNewAgentStore_InvalidBackend(t *testing.T) {
	cfg := config.StorageConfig{Backend: "dynamodb"}
	_, err := NewAgentStore(cfg)
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestNewMemoryStore_FileBackend(t *testing.T) {
	t.Setenv("AGENTOS_MEMORY_STORE_DIR", t.TempDir())
	cfg := config.StorageConfig{Backend: "file"}
	store, err := NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore(file): %v", err)
	}
	if _, ok := store.(*fileMemoryStore); !ok {
		t.Fatalf("expected *fileMemoryStore, got %T", store)
	}
}

func TestNewMemoryStore_DefaultBackendIsFile(t *testing.T) {
	t.Setenv("AGENTOS_MEMORY_STORE_DIR", t.TempDir())
	cfg := config.StorageConfig{Backend: ""}
	store, err := NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore(default): %v", err)
	}
	if _, ok := store.(*fileMemoryStore); !ok {
		t.Fatalf("expected *fileMemoryStore for empty backend, got %T", store)
	}
}

func TestNewMemoryStore_InvalidBackend(t *testing.T) {
	cfg := config.StorageConfig{Backend: "mongo"}
	_, err := NewMemoryStore(cfg)
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestNewKVStore_FileBackend(t *testing.T) {
	t.Setenv("AGENTOS_KV_STORE_DIR", t.TempDir())
	cfg := config.StorageConfig{Backend: "file"}
	store, err := NewKVStore(cfg)
	if err != nil {
		t.Fatalf("NewKVStore(file): %v", err)
	}
	if _, ok := store.(*fileKVStore); !ok {
		t.Fatalf("expected *fileKVStore, got %T", store)
	}
}

func TestNewKVStore_DefaultBackendIsFile(t *testing.T) {
	t.Setenv("AGENTOS_KV_STORE_DIR", t.TempDir())
	cfg := config.StorageConfig{Backend: ""}
	store, err := NewKVStore(cfg)
	if err != nil {
		t.Fatalf("NewKVStore(default): %v", err)
	}
	if _, ok := store.(*fileKVStore); !ok {
		t.Fatalf("expected *fileKVStore for empty backend, got %T", store)
	}
}

func TestNewKVStore_InvalidBackend(t *testing.T) {
	cfg := config.StorageConfig{Backend: "sqlite"}
	_, err := NewKVStore(cfg)
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestNewEventLogStore_FileBackend(t *testing.T) {
	cfg := config.StorageConfig{Backend: "file"}
	store, err := NewEventLogStore(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewEventLogStore(file): %v", err)
	}
	if _, ok := store.(*fileEventLogStore); !ok {
		t.Fatalf("expected *fileEventLogStore, got %T", store)
	}
}

func TestNewEventLogStore_DefaultBackendIsFile(t *testing.T) {
	cfg := config.StorageConfig{Backend: ""}
	store, err := NewEventLogStore(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewEventLogStore(default): %v", err)
	}
	if _, ok := store.(*fileEventLogStore); !ok {
		t.Fatalf("expected *fileEventLogStore for empty backend, got %T", store)
	}
}

func TestNewEventLogStore_InvalidBackend(t *testing.T) {
	cfg := config.StorageConfig{Backend: "cassandra"}
	_, err := NewEventLogStore(cfg, t.TempDir())
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestNewUsageStore_FileBackendReturnsNil(t *testing.T) {
	cfg := config.StorageConfig{Backend: "file"}
	store, err := NewUsageStore(cfg)
	if err != nil {
		t.Fatalf("NewUsageStore(file): %v", err)
	}
	if store != nil {
		t.Fatalf("expected nil UsageStore for file backend, got %T", store)
	}
}

func TestNewUsageStore_EmptyBackendReturnsNil(t *testing.T) {
	cfg := config.StorageConfig{Backend: ""}
	store, err := NewUsageStore(cfg)
	if err != nil {
		t.Fatalf("NewUsageStore(default): %v", err)
	}
	if store != nil {
		t.Fatalf("expected nil UsageStore for empty backend, got %T", store)
	}
}
