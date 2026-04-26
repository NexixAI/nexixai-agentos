package usage

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// UsageQuerier abstracts the storage layer for testing.
type UsageQuerier interface {
	QueryUsage(ctx context.Context, tenantID string, start, end time.Time, granularity string) (*postgres.UsageReport, error)
}

// UsageHandler handles GET /v1/usage requests.
type UsageHandler struct {
	store UsageQuerier
}

// NewUsageHandler creates a new UsageHandler.
func NewUsageHandler(store UsageQuerier) *UsageHandler {
	return &UsageHandler{store: store}
}

// ServeHTTP handles the usage reporting endpoint.
func (h *UsageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	// Parse granularity.
	granularity := "day"
	if g := r.URL.Query().Get("granularity"); g != "" {
		if g != "day" && g != "hour" {
			httpx.Error(w, http.StatusBadRequest, "invalid_granularity", "granularity must be day or hour", httpx.CorrelationID(r), false)
			return
		}
		granularity = g
	}

	report, err := h.store.QueryUsage(r.Context(), tenantID, start, end, granularity)
	if err != nil {
		slog.Error("usage query failed", "error", err, "tenant_id", tenantID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to query usage", httpx.CorrelationID(r), true)
		return
	}

	httpx.JSON(w, http.StatusOK, report)
}
