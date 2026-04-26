package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// mockUsageQuerier implements UsageQuerier for testing.
type mockUsageQuerier struct {
	report *postgres.UsageReport
	err    error
}

func (m *mockUsageQuerier) QueryUsage(_ context.Context, tenantID string, start, end time.Time, granularity string) (*postgres.UsageReport, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.report != nil {
		return m.report, nil
	}
	return &postgres.UsageReport{
		TenantID:    tenantID,
		Period:      postgres.UsagePeriod{Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339)},
		TotalTokens: 1234,
		TotalRuns:   10,
		ByModel:     []postgres.UsageByModel{{Model: "gpt-4o", Tokens: 1000, Runs: 8}},
		ByBucket:    []postgres.UsageByBucket{{Date: "2026-03-01", Tokens: 1234, Runs: 10}},
	}, nil
}

func newAuthRequest(method, url, tenantID string) *http.Request {
	r := httptest.NewRequest(method, url, nil)
	ac := auth.AuthContext{TenantID: tenantID}
	ctx := auth.WithContext(r.Context(), ac)
	return r.WithContext(ctx)
}

func TestUsageHandler_DefaultDates(t *testing.T) {
	h := NewUsageHandler(&mockUsageQuerier{})
	r := newAuthRequest(http.MethodGet, "/v1/usage", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var report postgres.UsageReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.TenantID != "tnt_test" {
		t.Errorf("tenant_id = %q, want tnt_test", report.TenantID)
	}
	if report.TotalTokens != 1234 {
		t.Errorf("total_tokens = %d, want 1234", report.TotalTokens)
	}
}

func TestUsageHandler_CustomRange(t *testing.T) {
	h := NewUsageHandler(&mockUsageQuerier{})
	r := newAuthRequest(http.MethodGet, "/v1/usage?start=2026-03-01T00:00:00Z&end=2026-03-07T00:00:00Z&granularity=hour", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestUsageHandler_EmptyResults(t *testing.T) {
	h := NewUsageHandler(&mockUsageQuerier{
		report: &postgres.UsageReport{
			TenantID:    "tnt_test",
			Period:      postgres.UsagePeriod{Start: "2026-03-01T00:00:00Z", End: "2026-03-07T00:00:00Z"},
			TotalTokens: 0,
			TotalRuns:   0,
			ByModel:     []postgres.UsageByModel{},
			ByBucket:    []postgres.UsageByBucket{},
		},
	})
	r := newAuthRequest(http.MethodGet, "/v1/usage?start=2026-03-01T00:00:00Z&end=2026-03-07T00:00:00Z", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var report postgres.UsageReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.TotalTokens != 0 {
		t.Errorf("total_tokens = %d, want 0", report.TotalTokens)
	}
}

func TestUsageHandler_InvalidDates(t *testing.T) {
	h := NewUsageHandler(&mockUsageQuerier{})

	tests := []struct {
		name string
		url  string
	}{
		{"bad start", "/v1/usage?start=not-a-date"},
		{"bad end", "/v1/usage?end=not-a-date"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newAuthRequest(http.MethodGet, tc.url, "tnt_test")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
		})
	}
}

func TestUsageHandler_InvalidGranularity(t *testing.T) {
	h := NewUsageHandler(&mockUsageQuerier{})
	r := newAuthRequest(http.MethodGet, "/v1/usage?granularity=minute", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestUsageHandler_NoAuth(t *testing.T) {
	h := NewUsageHandler(&mockUsageQuerier{})
	r := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestUsageHandler_MethodNotAllowed(t *testing.T) {
	h := NewUsageHandler(&mockUsageQuerier{})
	r := newAuthRequest(http.MethodPost, "/v1/usage", "tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}
