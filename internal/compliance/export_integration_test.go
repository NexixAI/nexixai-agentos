//go:build integration

package compliance

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockCollector implements DataCollector for integration testing.
type mockCollector struct {
	data map[string]any
	err  error
}

func (m *mockCollector) CollectTenantData(tenantID string) (map[string]any, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.data != nil {
		return m.data, nil
	}
	return map[string]any{
		"tenant_id": tenantID,
		"agents":    []string{"agent-1", "agent-2"},
		"runs":      []string{"run-1"},
		"events":    []any{},
	}, nil
}

func TestIntegration_ExportCreatesOutputFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENTOS_EXPORT_DIR", tmpDir)

	collector := &mockCollector{}
	jobStore := NewExportJobStore(10)
	handler := NewExportHandler(jobStore, collector)

	// Override export dir to match temp dir.
	handler.exportDir = tmpDir

	now := time.Now().UTC()
	job := &ExportJob{
		JobID:     "exp-test-001",
		TenantID:  "tenant-export-test",
		status:    "pending",
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
	}
	jobStore.Add(job)

	// Run export synchronously.
	handler.runExport(job)

	// Verify status changed.
	if job.Status() != "complete" {
		t.Errorf("expected status=complete, got %s", job.Status())
	}

	// Verify file was created.
	fp := job.FilePath()
	if fp == "" {
		t.Fatal("expected non-empty file path after export")
	}

	expectedPath := filepath.Join(tmpDir, "exp-test-001.json")
	if fp != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, fp)
	}

	info, err := os.Stat(fp)
	if err != nil {
		t.Fatalf("exported file does not exist: %v", err)
	}
	if info.Size() == 0 {
		t.Error("exported file is empty")
	}
}

func TestIntegration_ExportWithNilCollector(t *testing.T) {
	tmpDir := t.TempDir()

	jobStore := NewExportJobStore(10)
	handler := NewExportHandler(jobStore, nil)
	handler.exportDir = tmpDir

	job := &ExportJob{
		JobID:    "exp-nil-001",
		TenantID: "tenant-nil",
		status:   "pending",
	}
	jobStore.Add(job)

	handler.runExport(job)

	if job.Status() != "complete" {
		t.Errorf("expected complete status with nil collector, got %s", job.Status())
	}
	// No file should be created with nil collector.
	if job.FilePath() != "" {
		t.Errorf("expected empty file path with nil collector, got %s", job.FilePath())
	}
}

func TestIntegration_ExportJobStoreEviction(t *testing.T) {
	store := NewExportJobStore(3)

	for i := 0; i < 5; i++ {
		store.Add(&ExportJob{
			JobID:    fmt.Sprintf("exp-%d", i),
			TenantID: "t1",
			status:   "complete",
		})
	}

	if store.Len() != 3 {
		t.Errorf("expected 3 jobs after eviction, got %d", store.Len())
	}

	// Oldest two should be evicted.
	if _, ok := store.Get("exp-0"); ok {
		t.Error("exp-0 should have been evicted")
	}
	if _, ok := store.Get("exp-1"); ok {
		t.Error("exp-1 should have been evicted")
	}
	// Newest three should remain.
	for i := 2; i < 5; i++ {
		if _, ok := store.Get(fmt.Sprintf("exp-%d", i)); !ok {
			t.Errorf("exp-%d should still exist", i)
		}
	}
}

func TestIntegration_ExportJobStatusTransitions(t *testing.T) {
	job := &ExportJob{
		JobID:    "exp-status-001",
		TenantID: "t1",
		status:   "pending",
	}

	if job.Status() != "pending" {
		t.Errorf("expected pending, got %s", job.Status())
	}

	job.SetStatus("complete")
	if job.Status() != "complete" {
		t.Errorf("expected complete, got %s", job.Status())
	}

	job.SetFilePath("/data/exports/test.json")
	if job.FilePath() != "/data/exports/test.json" {
		t.Errorf("expected file path, got %s", job.FilePath())
	}
}

func TestIntegration_DeleteRemovesTenantExportData(t *testing.T) {
	// Test the file-level cleanup path: create export, then remove file.
	tmpDir := t.TempDir()
	collector := &mockCollector{}
	jobStore := NewExportJobStore(10)
	handler := NewExportHandler(jobStore, collector)
	handler.exportDir = tmpDir

	job := &ExportJob{
		JobID:     "exp-delete-001",
		TenantID:  "tenant-delete",
		status:    "pending",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	jobStore.Add(job)
	handler.runExport(job)

	fp := job.FilePath()
	if fp == "" {
		t.Fatal("expected file path after export")
	}

	// Verify file exists before deletion.
	if _, err := os.Stat(fp); err != nil {
		t.Fatalf("exported file should exist: %v", err)
	}

	// Delete the exported file (simulating tenant data removal).
	if err := os.Remove(fp); err != nil {
		t.Fatalf("failed to remove export file: %v", err)
	}

	// Verify file is gone.
	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}
