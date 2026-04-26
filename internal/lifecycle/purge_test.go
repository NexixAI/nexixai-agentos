package lifecycle

import (
	"os"
	"testing"
)

func TestLoadPurgeConfig_Defaults(t *testing.T) {
	// Clear env vars to get defaults.
	os.Unsetenv("AGENTOS_RETENTION_RUNS_DAYS")
	os.Unsetenv("AGENTOS_RETENTION_EVENTS_DAYS")
	os.Unsetenv("AGENTOS_RETENTION_AUDIT_DAYS")
	os.Unsetenv("AGENTOS_RETENTION_USAGE_DAYS")

	cfg := LoadPurgeConfig()

	if cfg.RetentionRunsDays != 90 {
		t.Errorf("RetentionRunsDays = %d, want 90", cfg.RetentionRunsDays)
	}
	if cfg.RetentionEventsDays != 30 {
		t.Errorf("RetentionEventsDays = %d, want 30", cfg.RetentionEventsDays)
	}
	if cfg.RetentionAuditDays != 365 {
		t.Errorf("RetentionAuditDays = %d, want 365", cfg.RetentionAuditDays)
	}
	if cfg.RetentionUsageDays != 730 {
		t.Errorf("RetentionUsageDays = %d, want 730", cfg.RetentionUsageDays)
	}
}

func TestLoadPurgeConfig_CustomValues(t *testing.T) {
	t.Setenv("AGENTOS_RETENTION_RUNS_DAYS", "180")
	t.Setenv("AGENTOS_RETENTION_EVENTS_DAYS", "60")
	t.Setenv("AGENTOS_RETENTION_AUDIT_DAYS", "500")
	t.Setenv("AGENTOS_RETENTION_USAGE_DAYS", "1000")

	cfg := LoadPurgeConfig()

	if cfg.RetentionRunsDays != 180 {
		t.Errorf("RetentionRunsDays = %d, want 180", cfg.RetentionRunsDays)
	}
	if cfg.RetentionEventsDays != 60 {
		t.Errorf("RetentionEventsDays = %d, want 60", cfg.RetentionEventsDays)
	}
	if cfg.RetentionAuditDays != 500 {
		t.Errorf("RetentionAuditDays = %d, want 500", cfg.RetentionAuditDays)
	}
	if cfg.RetentionUsageDays != 1000 {
		t.Errorf("RetentionUsageDays = %d, want 1000", cfg.RetentionUsageDays)
	}
}

func TestLoadPurgeConfig_ClampMinimum(t *testing.T) {
	t.Setenv("AGENTOS_RETENTION_RUNS_DAYS", "1")    // min 7
	t.Setenv("AGENTOS_RETENTION_EVENTS_DAYS", "3")   // min 7
	t.Setenv("AGENTOS_RETENTION_AUDIT_DAYS", "10")   // min 90
	t.Setenv("AGENTOS_RETENTION_USAGE_DAYS", "50")   // min 90

	cfg := LoadPurgeConfig()

	if cfg.RetentionRunsDays != 7 {
		t.Errorf("RetentionRunsDays = %d, want 7 (clamped)", cfg.RetentionRunsDays)
	}
	if cfg.RetentionEventsDays != 7 {
		t.Errorf("RetentionEventsDays = %d, want 7 (clamped)", cfg.RetentionEventsDays)
	}
	if cfg.RetentionAuditDays != 90 {
		t.Errorf("RetentionAuditDays = %d, want 90 (clamped)", cfg.RetentionAuditDays)
	}
	if cfg.RetentionUsageDays != 90 {
		t.Errorf("RetentionUsageDays = %d, want 90 (clamped)", cfg.RetentionUsageDays)
	}
}

func TestLoadPurgeConfig_InvalidValue(t *testing.T) {
	t.Setenv("AGENTOS_RETENTION_RUNS_DAYS", "notanumber")

	cfg := LoadPurgeConfig()

	// Should fall back to default.
	if cfg.RetentionRunsDays != 90 {
		t.Errorf("RetentionRunsDays = %d, want 90 (default on invalid)", cfg.RetentionRunsDays)
	}
}

func TestEnvIntWithMinDefault(t *testing.T) {
	tests := []struct {
		name       string
		envVal     string
		defaultVal int
		minVal     int
		want       int
	}{
		{"empty_env", "", 90, 7, 90},
		{"valid_above_min", "30", 90, 7, 30},
		{"at_min", "7", 90, 7, 7},
		{"below_min", "3", 90, 7, 7},
		{"invalid", "xyz", 90, 7, 90},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_PURGE_" + tt.name
			if tt.envVal == "" {
				os.Unsetenv(key)
			} else {
				t.Setenv(key, tt.envVal)
			}

			got := envIntWithMinDefault(key, tt.defaultVal, tt.minVal)
			if got != tt.want {
				t.Errorf("envIntWithMinDefault(%q) = %d, want %d", tt.envVal, got, tt.want)
			}
		})
	}
}

func TestPurgeResult_Fields(t *testing.T) {
	r := PurgeResult{
		DeletedRuns:   10,
		DeletedEvents: 20,
		DeletedAudit:  5,
		DeletedUsage:  100,
	}

	if r.DeletedRuns != 10 {
		t.Errorf("DeletedRuns = %d, want 10", r.DeletedRuns)
	}
	if r.DeletedEvents != 20 {
		t.Errorf("DeletedEvents = %d, want 20", r.DeletedEvents)
	}
	if r.DeletedAudit != 5 {
		t.Errorf("DeletedAudit = %d, want 5", r.DeletedAudit)
	}
	if r.DeletedUsage != 100 {
		t.Errorf("DeletedUsage = %d, want 100", r.DeletedUsage)
	}
}
