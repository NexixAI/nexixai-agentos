package usage

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

func TestExportHandler_CSVFormat(t *testing.T) {
	h := NewExportHandler(&mockUsageQuerier{
		report: &postgres.UsageReport{
			TenantID:    "tnt_test",
			Period:      postgres.UsagePeriod{Start: "2026-03-01T00:00:00Z", End: "2026-03-07T00:00:00Z"},
			TotalTokens: 1500,
			TotalRuns:   5,
			ByModel:     []postgres.UsageByModel{{Model: "gpt-4o", Tokens: 1500, Runs: 5}},
			ByBucket:    []postgres.UsageByBucket{{Date: "2026-03-01", Tokens: 1500, Runs: 5}},
		},
	})

	r := newAuthRequest(http.MethodGet, "/v1/usage/export?format=csv&start=2026-03-01T00:00:00Z&end=2026-03-07T00:00:00Z", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "usage_export.csv") {
		t.Errorf("Content-Disposition = %q, want to contain usage_export.csv", cd)
	}

	reader := csv.NewReader(strings.NewReader(w.Body.String()))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}

	// Header + 1 bucket row + 1 model row = 3 rows.
	if len(records) < 2 {
		t.Fatalf("expected at least 2 CSV rows (header + data), got %d", len(records))
	}

	// Verify header columns.
	header := records[0]
	expectedCols := []string{"date", "tenant_id", "model", "input_tokens", "output_tokens", "runs"}
	for i, col := range expectedCols {
		if i >= len(header) || header[i] != col {
			t.Errorf("header[%d] = %q, want %q", i, header[i], col)
		}
	}
}

func TestExportHandler_Headers(t *testing.T) {
	h := NewExportHandler(&mockUsageQuerier{})
	r := newAuthRequest(http.MethodGet, "/v1/usage/export", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, expected attachment", cd)
	}
}

func TestExportHandler_EmptyData(t *testing.T) {
	h := NewExportHandler(&mockUsageQuerier{
		report: &postgres.UsageReport{
			TenantID:    "tnt_test",
			Period:      postgres.UsagePeriod{Start: "2026-03-01T00:00:00Z", End: "2026-03-07T00:00:00Z"},
			TotalTokens: 0,
			TotalRuns:   0,
			ByModel:     []postgres.UsageByModel{},
			ByBucket:    []postgres.UsageByBucket{},
		},
	})
	r := newAuthRequest(http.MethodGet, "/v1/usage/export", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	reader := csv.NewReader(strings.NewReader(w.Body.String()))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	// Should have just the header row.
	if len(records) != 1 {
		t.Errorf("expected 1 CSV row (header only), got %d", len(records))
	}
}

func TestExportHandler_NoAuth(t *testing.T) {
	h := NewExportHandler(&mockUsageQuerier{})
	r := httptest.NewRequest(http.MethodGet, "/v1/usage/export", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
