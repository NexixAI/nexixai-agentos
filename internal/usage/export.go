package usage

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
)

// ExportHandler handles GET /v1/usage/export requests.
type ExportHandler struct {
	store UsageQuerier
}

// NewExportHandler creates a new ExportHandler.
func NewExportHandler(store UsageQuerier) *ExportHandler {
	return &ExportHandler{store: store}
}

// ServeHTTP handles the CSV export endpoint.
func (h *ExportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported", httpx.CorrelationID(r), false)
		return
	}

	ac, ok := auth.Get(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing auth context", httpx.CorrelationID(r), false)
		return
	}
	tenantID, err := auth.RequireTenant(ac)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_tenant", err.Error(), httpx.CorrelationID(r), false)
		return
	}

	now := time.Now().UTC()

	// Parse start.
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if s := r.URL.Query().Get("start"); s != "" {
		parsed, parseErr := time.Parse(time.RFC3339, s)
		if parseErr != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_start", "start must be RFC3339", httpx.CorrelationID(r), false)
			return
		}
		start = parsed
	}

	// Parse end.
	end := now
	if e := r.URL.Query().Get("end"); e != "" {
		parsed, parseErr := time.Parse(time.RFC3339, e)
		if parseErr != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_end", "end must be RFC3339", httpx.CorrelationID(r), false)
			return
		}
		end = parsed
	}

	report, err := h.store.QueryUsage(r.Context(), tenantID, start, end, "day")
	if err != nil {
		slog.Error("usage export query failed", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to query usage", httpx.CorrelationID(r), true)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=usage_export.csv")
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	defer cw.Flush()

	// Write header row.
	if err := cw.Write([]string{"date", "tenant_id", "model", "input_tokens", "output_tokens", "runs"}); err != nil {
		slog.Error("csv write header failed", "error", err)
		return
	}

	// If we have by-model data, write one row per model per bucket.
	// Since the query store only returns by_model (total) and by_bucket (total),
	// we write the by_bucket rows with the tenant-level totals and model info.
	if len(report.ByBucket) == 0 && len(report.ByModel) == 0 {
		// Empty data — just the header.
		return
	}

	// Write by_bucket rows.
	for _, b := range report.ByBucket {
		row := []string{
			b.Date,
			tenantID,
			"", // model is aggregated at bucket level
			fmt.Sprintf("%d", b.Tokens),
			"0", // output_tokens not separately tracked at bucket level
			fmt.Sprintf("%d", b.Runs),
		}
		if err := cw.Write(row); err != nil {
			slog.Error("csv write row failed", "error", err)
			return
		}
	}

	// Write by_model summary rows (date = "total").
	for _, m := range report.ByModel {
		row := []string{
			"total",
			tenantID,
			m.Model,
			fmt.Sprintf("%d", m.Tokens),
			"0",
			fmt.Sprintf("%d", m.Runs),
		}
		if err := cw.Write(row); err != nil {
			slog.Error("csv write model row failed", "error", err)
			return
		}
	}
}
