package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// MigrationResult summarises counts from a file-to-postgres migration.
type MigrationResult struct {
	Agents  int
	Runs    int
	Memory  int
	KV      int
	Events  int
	Skipped int
	Errors  []string
}

// StorageMigrator copies data from file-based stores to another set of stores
// (typically postgres). It is idempotent: records that already exist in the
// target are skipped.
type StorageMigrator struct {
	fileAgents AgentStore
	fileRuns   RunStore
	fileMem    MemoryStore
	fileKV     KVStore
	fileEvents EventLogStore

	pgAgents AgentStore
	pgRuns   RunStore
	pgMem    MemoryStore
	pgKV     KVStore
	pgEvents EventLogStore

	// Directory roots for file-based stores, used to enumerate tenants/agents.
	agentDir  string
	runPath   string
	memoryDir string
	kvDir     string
	eventDir  string

	dryRun bool
}

// MigratorStores groups the five store interfaces together for convenience.
type MigratorStores struct {
	Agents AgentStore
	Runs   RunStore
	Mem    MemoryStore
	KV     KVStore
	Events EventLogStore
}

// MigratorFilePaths holds the directory/file paths for the file-based stores,
// enabling the migrator to enumerate tenants and agents by walking the filesystem.
type MigratorFilePaths struct {
	AgentDir  string // e.g. data/agents
	RunPath   string // e.g. data/agent-orchestrator/runs.json
	MemoryDir string // e.g. data/memory
	KVDir     string // e.g. data/kv
	EventDir  string // e.g. data/event_log (child of the data dir passed to NewFileEventLogStore)
}

// NewStorageMigrator constructs a migrator that reads from file stores and
// writes to pg stores.
func NewStorageMigrator(file, pg MigratorStores, paths MigratorFilePaths, dryRun bool) *StorageMigrator {
	return &StorageMigrator{
		fileAgents: file.Agents,
		fileRuns:   file.Runs,
		fileMem:    file.Mem,
		fileKV:     file.KV,
		fileEvents: file.Events,

		pgAgents: pg.Agents,
		pgRuns:   pg.Runs,
		pgMem:    pg.Mem,
		pgKV:     pg.KV,
		pgEvents: pg.Events,

		agentDir:  paths.AgentDir,
		runPath:   paths.RunPath,
		memoryDir: paths.MemoryDir,
		kvDir:     paths.KVDir,
		eventDir:  paths.EventDir,

		dryRun: dryRun,
	}
}

// Migrate copies all records from file stores to postgres stores.
// Existing records in the target are skipped (idempotent).
func (m *StorageMigrator) Migrate(ctx context.Context) (*MigrationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	res := &MigrationResult{}

	// Discover tenants from the agent directory.
	tenantIDs, err := m.discoverTenants()
	if err != nil {
		return nil, fmt.Errorf("discover tenants: %w", err)
	}
	slog.Info("migration: discovered tenants", "count", len(tenantIDs))

	m.migrateAgents(ctx, tenantIDs, res)
	m.migrateRuns(ctx, tenantIDs, res)
	m.migrateMemory(ctx, res)
	m.migrateKV(ctx, res)
	m.migrateEvents(ctx, res)

	return res, nil
}

// discoverTenants walks the agent directory to find all tenant IDs.
func (m *StorageMigrator) discoverTenants() ([]string, error) {
	if m.agentDir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(m.agentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tenants []string
	for _, e := range entries {
		if e.IsDir() {
			tenants = append(tenants, e.Name())
		}
	}
	return tenants, nil
}

func (m *StorageMigrator) migrateAgents(ctx context.Context, tenantIDs []string, res *MigrationResult) {
	for _, tid := range tenantIDs {
		agents, err := m.fileAgents.List(ctx, tid)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("list agents for tenant %s: %v", tid, err))
			continue
		}
		for _, agent := range agents {
			if err := ctx.Err(); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("context cancelled: %v", err))
				return
			}
			m.migrateOneAgent(ctx, agent, res)
		}
	}
}

func (m *StorageMigrator) migrateOneAgent(ctx context.Context, agent types.Agent, res *MigrationResult) {
	_, exists, err := m.pgAgents.Get(ctx, agent.TenantID, agent.AgentID)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("check agent %s/%s: %v", agent.TenantID, agent.AgentID, err))
		return
	}
	if exists {
		slog.Debug("migration: agent already exists, skipping", "tenant", agent.TenantID, "agent", agent.AgentID)
		res.Skipped++
		return
	}

	if m.dryRun {
		slog.Info("migration: [dry-run] would create agent", "tenant", agent.TenantID, "agent", agent.AgentID)
		res.Agents++
		return
	}

	if err := m.pgAgents.Create(ctx, agent); err != nil {
		if errors.Is(err, ErrAgentExists) {
			res.Skipped++
			return
		}
		res.Errors = append(res.Errors, fmt.Sprintf("create agent %s/%s: %v", agent.TenantID, agent.AgentID, err))
		return
	}
	slog.Info("migration: created agent", "tenant", agent.TenantID, "agent", agent.AgentID)
	res.Agents++
}

func (m *StorageMigrator) migrateRuns(ctx context.Context, tenantIDs []string, res *MigrationResult) {
	for _, tid := range tenantIDs {
		runs, err := m.fileRuns.List(ctx, tid)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("list runs for tenant %s: %v", tid, err))
			continue
		}
		for _, run := range runs {
			if err := ctx.Err(); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("context cancelled: %v", err))
				return
			}
			m.migrateOneRun(ctx, run, res)
		}
	}
}

func (m *StorageMigrator) migrateOneRun(ctx context.Context, run types.Run, res *MigrationResult) {
	_, exists, err := m.pgRuns.Get(ctx, run.TenantID, run.RunID)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("check run %s/%s: %v", run.TenantID, run.RunID, err))
		return
	}
	if exists {
		slog.Debug("migration: run already exists, skipping", "tenant", run.TenantID, "run", run.RunID)
		res.Skipped++
		return
	}

	if m.dryRun {
		slog.Info("migration: [dry-run] would create run", "tenant", run.TenantID, "run", run.RunID)
		res.Runs++
		return
	}

	if err := m.pgRuns.Create(ctx, run); err != nil {
		if errors.Is(err, ErrRunExists) {
			res.Skipped++
			return
		}
		res.Errors = append(res.Errors, fmt.Sprintf("create run %s/%s: %v", run.TenantID, run.RunID, err))
		return
	}
	slog.Info("migration: created run", "tenant", run.TenantID, "run", run.RunID)
	res.Runs++
}

func (m *StorageMigrator) migrateMemory(ctx context.Context, res *MigrationResult) {
	if m.memoryDir == "" {
		return
	}

	pairs, err := discoverTenantAgentPairs(m.memoryDir)
	if err != nil {
		if !os.IsNotExist(err) {
			res.Errors = append(res.Errors, fmt.Sprintf("discover memory pairs: %v", err))
		}
		return
	}

	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("context cancelled: %v", err))
			return
		}
		m.migrateOneMemory(ctx, pair.tenantID, pair.agentID, res)
	}
}

func (m *StorageMigrator) migrateOneMemory(ctx context.Context, tenantID, agentID string, res *MigrationResult) {
	// Read all messages from file store.
	msgs, err := m.fileMem.GetRecent(ctx, tenantID, agentID, 0, 0)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("read memory %s/%s: %v", tenantID, agentID, err))
		return
	}
	if len(msgs) == 0 {
		return
	}

	// Check if target already has data.
	existing, err := m.pgMem.GetRecent(ctx, tenantID, agentID, 1, 0)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("check memory %s/%s: %v", tenantID, agentID, err))
		return
	}
	if len(existing) > 0 {
		slog.Debug("migration: memory already exists, skipping", "tenant", tenantID, "agent", agentID)
		res.Skipped++
		return
	}

	if m.dryRun {
		slog.Info("migration: [dry-run] would migrate memory", "tenant", tenantID, "agent", agentID, "messages", len(msgs))
		res.Memory++
		return
	}

	if err := m.pgMem.Append(ctx, tenantID, agentID, msgs); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("write memory %s/%s: %v", tenantID, agentID, err))
		return
	}
	slog.Info("migration: migrated memory", "tenant", tenantID, "agent", agentID, "messages", len(msgs))
	res.Memory++
}

func (m *StorageMigrator) migrateKV(ctx context.Context, res *MigrationResult) {
	if m.kvDir == "" {
		return
	}

	pairs, err := discoverTenantAgentPairs(m.kvDir)
	if err != nil {
		if !os.IsNotExist(err) {
			res.Errors = append(res.Errors, fmt.Sprintf("discover kv pairs: %v", err))
		}
		return
	}

	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("context cancelled: %v", err))
			return
		}
		m.migrateOneKV(ctx, pair.tenantID, pair.agentID, res)
	}
}

func (m *StorageMigrator) migrateOneKV(ctx context.Context, tenantID, agentID string, res *MigrationResult) {
	keys, err := m.fileKV.ListKeys(ctx, tenantID, agentID)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("list kv keys %s/%s: %v", tenantID, agentID, err))
		return
	}
	if len(keys) == 0 {
		return
	}

	migrated := 0
	for _, key := range keys {
		val, ok, err := m.fileKV.Get(ctx, tenantID, agentID, key)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("read kv %s/%s/%s: %v", tenantID, agentID, key, err))
			continue
		}
		if !ok {
			continue
		}

		// Check if key already exists in target.
		_, exists, err := m.pgKV.Get(ctx, tenantID, agentID, key)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("check kv %s/%s/%s: %v", tenantID, agentID, key, err))
			continue
		}
		if exists {
			res.Skipped++
			continue
		}

		if m.dryRun {
			migrated++
			continue
		}

		if err := m.pgKV.Set(ctx, tenantID, agentID, key, val); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("set kv %s/%s/%s: %v", tenantID, agentID, key, err))
			continue
		}
		migrated++
	}

	if migrated > 0 {
		if m.dryRun {
			slog.Info("migration: [dry-run] would migrate kv pairs", "tenant", tenantID, "agent", agentID, "keys", migrated)
		} else {
			slog.Info("migration: migrated kv pairs", "tenant", tenantID, "agent", agentID, "keys", migrated)
		}
		res.KV += migrated
	}
}

func (m *StorageMigrator) migrateEvents(ctx context.Context, res *MigrationResult) {
	if m.eventDir == "" {
		return
	}

	pairs, err := discoverTenantRunPairs(m.eventDir)
	if err != nil {
		if !os.IsNotExist(err) {
			res.Errors = append(res.Errors, fmt.Sprintf("discover event pairs: %v", err))
		}
		return
	}

	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("context cancelled: %v", err))
			return
		}
		m.migrateOneEventRun(ctx, pair.tenantID, pair.runID, res)
	}
}

func (m *StorageMigrator) migrateOneEventRun(ctx context.Context, tenantID, runID string, res *MigrationResult) {
	events, err := m.fileEvents.QueryFromSequence(ctx, tenantID, runID, 0)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("read events %s/%s: %v", tenantID, runID, err))
		return
	}
	if len(events) == 0 {
		return
	}

	// Check if target already has events for this run.
	existing, err := m.pgEvents.QueryFromSequence(ctx, tenantID, runID, 0)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("check events %s/%s: %v", tenantID, runID, err))
		return
	}
	if len(existing) > 0 {
		slog.Debug("migration: events already exist, skipping", "tenant", tenantID, "run", runID)
		res.Skipped++
		return
	}

	if m.dryRun {
		slog.Info("migration: [dry-run] would migrate events", "tenant", tenantID, "run", runID, "count", len(events))
		res.Events += len(events)
		return
	}

	for _, event := range events {
		if err := m.pgEvents.Append(ctx, event); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("write event %s/%s seq=%d: %v", tenantID, runID, event.Event.Sequence, err))
			continue
		}
		res.Events++
	}
	slog.Info("migration: migrated events", "tenant", tenantID, "run", runID, "count", len(events))
}

// tenantAgentPair represents a tenant/agent combination discovered on disk.
type tenantAgentPair struct {
	tenantID string
	agentID  string
}

// tenantRunPair represents a tenant/run combination discovered on disk.
type tenantRunPair struct {
	tenantID string
	runID    string
}

// discoverTenantAgentPairs walks a directory of structure {tenantID}/{agentID}.json
// and returns all pairs found.
func discoverTenantAgentPairs(dir string) ([]tenantAgentPair, error) {
	tenantEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var pairs []tenantAgentPair
	for _, te := range tenantEntries {
		if !te.IsDir() {
			continue
		}
		tenantID := te.Name()
		agentEntries, err := os.ReadDir(filepath.Join(dir, tenantID))
		if err != nil {
			continue
		}
		for _, ae := range agentEntries {
			if ae.IsDir() || !strings.HasSuffix(ae.Name(), ".json") {
				continue
			}
			agentID := strings.TrimSuffix(ae.Name(), ".json")
			pairs = append(pairs, tenantAgentPair{tenantID: tenantID, agentID: agentID})
		}
	}
	return pairs, nil
}

// discoverTenantRunPairs walks a directory of structure {tenantID}/{runID}.json
// and returns all pairs found.
func discoverTenantRunPairs(dir string) ([]tenantRunPair, error) {
	tenantEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var pairs []tenantRunPair
	for _, te := range tenantEntries {
		if !te.IsDir() {
			continue
		}
		tenantID := te.Name()
		runEntries, err := os.ReadDir(filepath.Join(dir, tenantID))
		if err != nil {
			continue
		}
		for _, re := range runEntries {
			if re.IsDir() || !strings.HasSuffix(re.Name(), ".json") {
				continue
			}
			runID := strings.TrimSuffix(re.Name(), ".json")
			pairs = append(pairs, tenantRunPair{tenantID: tenantID, runID: runID})
		}
	}
	return pairs, nil
}

// loadRunsFromFile reads the run store JSON file directly to get all runs
// without needing to know tenant IDs. This is used during migration to
// discover tenants from the run store.
func loadRunsFromFile(path string) (map[string]types.Run, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var runs map[string]types.Run
	if err := json.Unmarshal(b, &runs); err != nil {
		return nil, err
	}
	return runs, nil
}
