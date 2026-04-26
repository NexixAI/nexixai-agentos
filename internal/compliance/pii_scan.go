package compliance

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/NexixAI/nexixai-agentos/internal/httpx"
	"github.com/NexixAI/nexixai-agentos/internal/pii"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// EventLoader loads events for a given run from the event log.
type EventLoader interface {
	QueryFromSequence(ctx context.Context, tenantID, runID string, afterSequence int) ([]types.EventEnvelope, error)
}

// PIILocation describes a PII detection within an event, without revealing the actual value.
type PIILocation struct {
	EventIndex int    `json:"event_index"`
	Field      string `json:"field"`
	PIIType    string `json:"pii_type"`
}

// PIIScanHandler handles GET /v1/admin/pii-scan requests.
type PIIScanHandler struct {
	eventLoader EventLoader
	detector    *pii.Detector
}

// NewPIIScanHandler creates a new PIIScanHandler.
func NewPIIScanHandler(loader EventLoader) *PIIScanHandler {
	return &PIIScanHandler{
		eventLoader: loader,
		detector:    pii.NewDetector(nil), // all patterns enabled
	}
}

// ServeHTTP handles PII scan requests.
func (h *PIIScanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported", httpx.CorrelationID(r), false)
		return
	}

	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_run_id", "run_id query parameter is required", httpx.CorrelationID(r), false)
		return
	}

	// For PII scan, we use an empty tenant to scan all events for the run,
	// or we can extract tenant from auth context if available.
	tenantID := ""
	if r.Header.Get("X-Tenant-Id") != "" {
		tenantID = r.Header.Get("X-Tenant-Id")
	}

	events, err := h.eventLoader.QueryFromSequence(r.Context(), tenantID, runID, 0)
	if err != nil {
		slog.Error("pii-scan: failed to load events",
			"error", err, "run_id", runID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load events", httpx.CorrelationID(r), true)
		return
	}

	var locations []PIILocation
	for i, envelope := range events {
		// Scan the payload fields for PII.
		for field, val := range envelope.Event.Payload {
			text := extractText(val)
			if text == "" {
				continue
			}
			detections := h.detector.Scan(text)
			for _, det := range detections {
				locations = append(locations, PIILocation{
					EventIndex: i,
					Field:      field,
					PIIType:    det.Pattern,
				})
			}
		}
	}

	if locations == nil {
		locations = []PIILocation{}
	}

	httpx.JSON(w, http.StatusOK, locations)
}

// extractText attempts to get a string from a payload value.
// Handles string, json.RawMessage, and nested maps.
func extractText(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return ""
	case float64:
		return ""
	case bool:
		return ""
	case nil:
		return ""
	default:
		// Try to marshal and use the raw JSON as text for scanning.
		b, err := json.Marshal(val)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
