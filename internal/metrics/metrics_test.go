package metrics

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMetricsRequireAuth(t *testing.T) {
	tests := []struct {
		name string
		env  string // value to set; "\x00" means unset
		want bool
	}{
		{"empty_default_true", "\x00", true},
		{"zero_false", "0", false},
		{"one_true", "1", true},
		{"invalid_true", "invalid", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "\x00" {
				os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")
			} else {
				os.Setenv("AGENTOS_METRICS_REQUIRE_AUTH", tt.env)
			}
			defer os.Unsetenv("AGENTOS_METRICS_REQUIRE_AUTH")

			got := MetricsRequireAuth()
			if got != tt.want {
				t.Errorf("MetricsRequireAuth() with env=%q: got %v, want %v", tt.env, got, tt.want)
			}
		})
	}
}

func TestTruncateLabel(t *testing.T) {
	t.Run("short_unchanged", func(t *testing.T) {
		s := "short-label"
		got := truncateLabel(s)
		if got != s {
			t.Errorf("truncateLabel(%q) = %q, want %q", s, got, s)
		}
	})

	t.Run("long_truncated", func(t *testing.T) {
		s := strings.Repeat("a", 200)
		got := truncateLabel(s)
		if len(got) != maxLabelLen {
			t.Errorf("truncateLabel(len=%d) len = %d, want %d", len(s), len(got), maxLabelLen)
		}
	})

	t.Run("exact_boundary", func(t *testing.T) {
		s := strings.Repeat("b", maxLabelLen)
		got := truncateLabel(s)
		if got != s {
			t.Errorf("truncateLabel at exact boundary should return unchanged")
		}
	})
}

func TestInstrument(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := Instrument("test-service", inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Instrument handler status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("Instrument handler body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestAddModelTokensTracksDailyTenantUsage(t *testing.T) {
	resetDailyTokenBudgetTestState(t)

	nowUTC = func() time.Time {
		return time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)
	}

	AddModelTokens("tenant-a", "model-1", 3, 7)
	AddModelTokens("tenant-a", "model-1", 5, 2)
	AddModelTokens("tenant-b", "model-1", 1, 1)

	if got := dailyTokenGaugeValue(t, "tenant-a"); got != 17 {
		t.Fatalf("tenant-a daily usage = %v, want 17", got)
	}
	if got := dailyTokenGaugeValue(t, "tenant-b"); got != 2 {
		t.Fatalf("tenant-b daily usage = %v, want 2", got)
	}
}

func TestDailyTokenBudgetResetsAtMidnightUTCOnScrape(t *testing.T) {
	resetDailyTokenBudgetTestState(t)

	nowUTC = func() time.Time {
		return time.Date(2026, time.April, 10, 23, 59, 0, 0, time.UTC)
	}
	AddModelTokens("tenant-a", "model-1", 2, 3)

	if got := dailyTokenGaugeValue(t, "tenant-a"); got != 5 {
		t.Fatalf("tenant-a daily usage before midnight = %v, want 5", got)
	}

	nowUTC = func() time.Time {
		return time.Date(2026, time.April, 11, 0, 0, 1, 0, time.UTC)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics handler status = %d, want %d", rec.Code, http.StatusOK)
	}

	if got := dailyTokenGaugeValue(t, "tenant-a"); got != 0 {
		t.Fatalf("tenant-a daily usage after midnight scrape = %v, want 0", got)
	}
}

func resetDailyTokenBudgetTestState(t *testing.T) {
	t.Helper()

	dailyTokenBudgetState.mu.Lock()
	dailyTokenBudgetState.currentDay = ""
	clear(dailyTokenBudgetState.usageByTenant)
	dailyTokenBudgetState.mu.Unlock()
	dailyTokenBudgetUsed.Reset()

	nowUTC = func() time.Time {
		return time.Now().UTC()
	}

	t.Cleanup(func() {
		dailyTokenBudgetState.mu.Lock()
		dailyTokenBudgetState.currentDay = ""
		clear(dailyTokenBudgetState.usageByTenant)
		dailyTokenBudgetState.mu.Unlock()
		dailyTokenBudgetUsed.Reset()
		nowUTC = func() time.Time {
			return time.Now().UTC()
		}
	})
}

func dailyTokenGaugeValue(t *testing.T, tenant string) float64 {
	t.Helper()

	mfs, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	for _, mf := range mfs {
		if mf.GetName() != "agentos_daily_token_budget_used" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "tenant" && label.GetValue() == tenant {
					return metric.GetGauge().GetValue()
				}
			}
		}
		return 0
	}

	return 0
}
