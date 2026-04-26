package compliance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
)

func newAuthRequest(method, url, tenantID, role string) *http.Request {
	r := httptest.NewRequest(method, url, nil)
	ac := auth.AuthContext{TenantID: tenantID, SubjectType: role}
	ctx := auth.WithContext(r.Context(), ac)
	return r.WithContext(ctx)
}

func TestExportHandler_CreateJob(t *testing.T) {
	store := NewExportJobStore(100)
	h := NewExportHandler(store, nil)

	r := newAuthRequest(http.MethodPost, "/v1/tenants/data-export", "tnt_test", "owner")
	w := httptest.NewRecorder()

	h.HandleCreate(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp["status"] != "pending" {
		t.Errorf("status = %q, want pending", resp["status"])
	}
	if resp["job_id"] == "" {
		t.Error("expected non-empty job_id")
	}

	// Verify job was stored.
	if store.Len() != 1 {
		t.Errorf("store.Len() = %d, want 1", store.Len())
	}
}

func TestExportHandler_StatusCheck(t *testing.T) {
	store := NewExportJobStore(100)
	job := &ExportJob{
		JobID:     "exp_test123",
		TenantID:  "tnt_test",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339),
	}
	job.SetStatus("complete")
	job.SetFilePath("/data/exports/exp_test123.json")
	store.Add(job)

	h := NewExportHandler(store, nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/tenants/data-export/exp_test123", nil)
	w := httptest.NewRecorder()

	h.HandleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "complete" {
		t.Errorf("status = %q, want complete", resp["status"])
	}
	if resp["download_url"] == "" {
		t.Error("expected download_url for complete job")
	}
}

func TestExportHandler_StatusNotFound(t *testing.T) {
	store := NewExportJobStore(100)
	h := NewExportHandler(store, nil)

	r := httptest.NewRequest(http.MethodGet, "/v1/tenants/data-export/exp_nonexistent", nil)
	w := httptest.NewRecorder()

	h.HandleStatus(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestExportJobStore_BoundedSize(t *testing.T) {
	store := NewExportJobStore(3)

	for i := 0; i < 5; i++ {
		job := &ExportJob{
			JobID:    "exp_" + string(rune('a'+i)),
			TenantID: "tnt_test",
		}
		job.SetStatus("pending")
		store.Add(job)
	}

	if store.Len() != 3 {
		t.Errorf("store.Len() = %d, want 3 (bounded)", store.Len())
	}

	// First two should have been evicted.
	if _, ok := store.Get("exp_a"); ok {
		t.Error("exp_a should have been evicted")
	}
	if _, ok := store.Get("exp_b"); ok {
		t.Error("exp_b should have been evicted")
	}
	// Last three should exist.
	for _, jid := range []string{"exp_c", "exp_d", "exp_e"} {
		if _, ok := store.Get(jid); !ok {
			t.Errorf("%s should still exist", jid)
		}
	}
}

func TestExportHandler_ForbiddenForNonOwner(t *testing.T) {
	store := NewExportJobStore(100)
	h := NewExportHandler(store, nil)

	r := newAuthRequest(http.MethodPost, "/v1/tenants/data-export", "tnt_test", "viewer")
	w := httptest.NewRecorder()

	h.HandleCreate(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}
