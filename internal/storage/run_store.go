package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

var (
	// ErrRunExists signals attempts to create a run that already exists for the tenant.
	ErrRunExists = errors.New("run already exists")
	// ErrInvalidRun signals missing required run identity fields.
	ErrInvalidRun = errors.New("invalid run")
)

// RunStore is a tenant-scoped persistence port for core run CRUD operations.
type RunStore interface {
	Create(ctx context.Context, run types.Run) error
	Get(ctx context.Context, tenantID, runID string) (types.Run, bool, error)
	Save(ctx context.Context, run types.Run) error
	List(ctx context.Context, tenantID string) ([]types.Run, error)
	Close() error
}

// RunQueryStore provides specialized query methods for run lookup and listing.
type RunQueryStore interface {
	GetByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (types.Run, bool, error)
	ListByAgent(ctx context.Context, tenantID, agentID string, limit int, afterRunID string) ([]types.Run, error)
	// ListChildRuns returns runs that are children of the given run, i.e.
	// runs where retry_of == parentRunID or parent_run_id == parentRunID.
	ListChildRuns(ctx context.Context, tenantID, parentRunID string) ([]types.Run, error)
}

// FullRunStore combines RunStore and RunQueryStore for consumers that need
// both CRUD and specialized query access.
type FullRunStore interface {
	RunStore
	RunQueryStore
}

// NewRunStoreFromEnv constructs the default run store adapter, using AGENTOS_RUN_STORE_FILE
// or falling back to ./data/agent-orchestrator/runs.json.
func NewRunStoreFromEnv() (FullRunStore, error) {
	path := strings.TrimSpace(os.Getenv("AGENTOS_RUN_STORE_FILE"))
	if path == "" {
		path = filepath.Join("data", "agent-orchestrator", "runs.json")
	}
	return NewFileRunStore(path)
}
