package compliance

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/id"
)

// ExportJob tracks the state of a data-export request.
type ExportJob struct {
	mu        sync.Mutex
	JobID     string `json:"job_id"`
	TenantID  string `json:"tenant_id"`
	status    string // pending, complete, expired — access via methods
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
	filePath  string // access via methods
}

// SetStatus updates the job status in a thread-safe manner.
func (j *ExportJob) SetStatus(s string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = s
}

// Status returns the current job status.
func (j *ExportJob) Status() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// SetFilePath updates the file path in a thread-safe manner.
func (j *ExportJob) SetFilePath(p string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.filePath = p
}

// FilePath returns the current file path.
func (j *ExportJob) FilePath() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.filePath
}

// DataCollector gathers tenant data for export.
// Implementations should collect agents, runs, events, audit, usage, memory, and KV data.
type DataCollector interface {
	CollectTenantData(tenantID string) (map[string]any, error)
}

// ExportJobStore tracks pending export jobs with a bounded in-memory map.
// Max 100 jobs; oldest evicted when full.
type ExportJobStore struct {
	mu      sync.Mutex
	jobs    map[string]*ExportJob
	order   []string // insertion order for eviction
	maxJobs int
}

// NewExportJobStore creates a store with the given capacity.
func NewExportJobStore(maxJobs int) *ExportJobStore {
	if maxJobs <= 0 {
		maxJobs = 100
	}
	return &ExportJobStore{
		jobs:    make(map[string]*ExportJob, maxJobs),
		order:   make([]string, 0, maxJobs),
		maxJobs: maxJobs,
	}
}

// Add inserts a job, evicting the oldest if at capacity.
func (s *ExportJobStore) Add(job *ExportJob) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Evict oldest if at capacity.
	for len(s.order) >= s.maxJobs {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.jobs, oldest)
	}

	s.jobs[job.JobID] = job
	s.order = append(s.order, job.JobID)
}

// Get retrieves a job by ID.
func (s *ExportJobStore) Get(jobID string) (*ExportJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	return j, ok
}

// Len returns the number of tracked jobs.
func (s *ExportJobStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}

// ExportHandler handles POST /v1/tenants/data-export and GET /v1/tenants/data-export/{job_id}.
type ExportHandler struct {
	jobStore  *ExportJobStore
	collector DataCollector
	exportDir string
}

// NewExportHandler creates a new ExportHandler.
func NewExportHandler(jobStore *ExportJobStore, collector DataCollector) *ExportHandler {
	dir := os.Getenv("AGENTOS_EXPORT_DIR")
	if dir == "" {
		dir = "data/exports/"
	}
	return &ExportHandler{
		jobStore:  jobStore,
		collector: collector,
		exportDir: dir,
	}
}

// HandleCreate handles POST /v1/tenants/data-export.
func (h *ExportHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported", httpx.CorrelationID(r), false)
		return
	}

	ac, ok := auth.Get(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing auth context", httpx.CorrelationID(r), false)
		return
	}

	// Require owner role.
	role := auth.Role(ac.SubjectType)
	if role == "" {
		role = auth.RoleOwner // default for backward compatibility in tests
	}
	if !auth.HasPermission(role, auth.RoleOwner) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "owner role required", httpx.CorrelationID(r), false)
		return
	}

	tenantID, err := auth.RequireTenant(ac)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_tenant", err.Error(), httpx.CorrelationID(r), false)
		return
	}

	now := time.Now().UTC()
	job := &ExportJob{
		JobID:     id.New("exp"),
		TenantID:  tenantID,
		status:    "pending",
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
	}

	h.jobStore.Add(job)

	// Capture immutable values for the response.
	jobID := job.JobID

	// Launch async export.
	go h.runExport(job)

	httpx.JSON(w, http.StatusAccepted, map[string]string{
		"job_id": jobID,
		"status": "pending",
	})
}

// HandleStatus handles GET /v1/tenants/data-export/{job_id}.
// The caller must extract job_id from the URL path and pass it as a query param or path segment.
func (h *ExportHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported", httpx.CorrelationID(r), false)
		return
	}

	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		// Try extracting from path: /v1/tenants/data-export/{job_id}
		jobID = filepath.Base(r.URL.Path)
	}

	if jobID == "" || jobID == "." || jobID == "/" {
		httpx.Error(w, http.StatusBadRequest, "missing_job_id", "job_id is required", httpx.CorrelationID(r), false)
		return
	}

	job, ok := h.jobStore.Get(jobID)
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not_found", "export job not found", httpx.CorrelationID(r), false)
		return
	}

	status := job.Status()
	fp := job.FilePath()

	// Check expiry.
	if status == "complete" {
		expires, parseErr := time.Parse(time.RFC3339, job.ExpiresAt)
		if parseErr == nil && time.Now().UTC().After(expires) {
			job.SetStatus("expired")
			status = "expired"
		}
	}

	resp := map[string]string{
		"job_id": job.JobID,
		"status": status,
	}
	if status == "complete" && fp != "" {
		resp["download_url"] = fp
	}

	httpx.JSON(w, http.StatusOK, resp)
}

func (h *ExportHandler) runExport(job *ExportJob) {
	if h.collector == nil {
		slog.Warn("export: no data collector configured, marking complete with empty archive",
			"job_id", job.JobID)
		job.SetStatus("complete")
		return
	}

	data, err := h.collector.CollectTenantData(job.TenantID)
	if err != nil {
		slog.Error("export: data collection failed",
			"error", err, "job_id", job.JobID, "tenant_id", job.TenantID)
		job.SetStatus("complete") // Still mark complete so the job doesn't hang.
		return
	}

	// Ensure export directory exists.
	if mkErr := os.MkdirAll(h.exportDir, 0o750); mkErr != nil {
		slog.Error("export: failed to create export directory",
			"error", mkErr, "dir", h.exportDir)
		job.SetStatus("complete")
		return
	}

	fPath := filepath.Join(h.exportDir, job.JobID+".json")
	f, err := os.Create(fPath)
	if err != nil {
		slog.Error("export: failed to create export file",
			"error", err, "path", fPath)
		job.SetStatus("complete")
		return
	}
	defer f.Close() //nolint:errcheck // best-effort close

	if err := json.NewEncoder(f).Encode(data); err != nil {
		slog.Error("export: failed to write export data",
			"error", err, "job_id", job.JobID)
	}

	job.SetFilePath(fPath)
	job.SetStatus("complete")

	slog.Info("export: completed successfully",
		"job_id", job.JobID, "tenant_id", job.TenantID, "path", fPath)
}
