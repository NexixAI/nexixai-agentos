package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestBackupCheckHandler_NilDB(t *testing.T) {
	handler := BackupCheckHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/backup-check", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp BackupCheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.DatabaseSizeBytes != -1 {
		t.Errorf("expected database_size_bytes=-1 for nil DB, got %d", resp.DatabaseSizeBytes)
	}
	for _, table := range monitoredTables {
		count, ok := resp.TableRowCounts[table]
		if !ok {
			t.Errorf("missing table %s in row counts", table)
		} else if count != -1 {
			t.Errorf("expected count=-1 for table %s with nil DB, got %d", table, count)
		}
	}
}

func TestBackupCheckHandler_WarningWhenEnvMissing(t *testing.T) {
	os.Unsetenv("AGENTOS_LAST_BACKUP_TS")

	handler := BackupCheckHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/backup-check", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp BackupCheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.Warning != "No backup detected in >24h" {
		t.Errorf("expected warning for missing env, got %q", resp.Warning)
	}
}

func TestBackupCheckHandler_WarningWhenOld(t *testing.T) {
	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	os.Setenv("AGENTOS_LAST_BACKUP_TS", old)
	defer os.Unsetenv("AGENTOS_LAST_BACKUP_TS")

	handler := BackupCheckHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/backup-check", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp BackupCheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.Warning != "No backup detected in >24h" {
		t.Errorf("expected warning for old backup, got %q", resp.Warning)
	}
}

func TestBackupCheckHandler_NoWarningWhenRecent(t *testing.T) {
	recent := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	os.Setenv("AGENTOS_LAST_BACKUP_TS", recent)
	defer os.Unsetenv("AGENTOS_LAST_BACKUP_TS")

	handler := BackupCheckHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/backup-check", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp BackupCheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.Warning != "" {
		t.Errorf("expected no warning for recent backup, got %q", resp.Warning)
	}
}

func TestBackupCheckHandler_ResponseShape(t *testing.T) {
	os.Unsetenv("AGENTOS_LAST_BACKUP_TS")

	handler := BackupCheckHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/backup-check", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var raw map[string]any
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	requiredFields := []string{"last_backup_ts", "warning", "database_size_bytes", "table_row_counts"}
	for _, f := range requiredFields {
		if _, ok := raw[f]; !ok {
			t.Errorf("missing required field %q in response", f)
		}
	}

	counts, ok := raw["table_row_counts"].(map[string]any)
	if !ok {
		t.Fatal("table_row_counts is not an object")
	}
	for _, table := range monitoredTables {
		if _, ok := counts[table]; !ok {
			t.Errorf("missing table %q in table_row_counts", table)
		}
	}
}

func TestBackupCheckHandler_MethodNotAllowed(t *testing.T) {
	handler := BackupCheckHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/backup-check", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
