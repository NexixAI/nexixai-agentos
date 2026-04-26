//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_UsageQueryStore_QueryUsage(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewUsageQueryStoreFromDB(db)
	ctx := context.Background()
	tenantID := "test_" + t.Name()
	now := time.Now().UTC()

	// Cleanup.
	defer func() {
		db.ExecContext(ctx, "DELETE FROM usage_records WHERE tenant_id = $1", tenantID)
	}()

	// Insert test records.
	const insertQ = `INSERT INTO usage_records (tenant_id, tokens, model, recorded_at) VALUES ($1, $2, $3, $4)`
	records := []struct {
		tokens int
		model  string
		ts     time.Time
	}{
		{1000, "gpt-4o", now.Add(-2 * time.Hour)},
		{500, "gpt-4o", now.Add(-1 * time.Hour)},
		{300, "gpt-3.5-turbo", now.Add(-30 * time.Minute)},
	}
	for _, r := range records {
		if _, err := db.ExecContext(ctx, insertQ, tenantID, r.tokens, r.model, r.ts); err != nil {
			t.Fatalf("insert record: %v", err)
		}
	}

	t.Run("DayGranularity", func(t *testing.T) {
		start := now.Add(-24 * time.Hour)
		report, err := store.QueryUsage(ctx, tenantID, start, now.Add(time.Hour), "day")
		if err != nil {
			t.Fatalf("QueryUsage: %v", err)
		}
		if report.TotalTokens != 1800 {
			t.Errorf("total_tokens = %d, want 1800", report.TotalTokens)
		}
		if report.TotalRuns != 3 {
			t.Errorf("total_runs = %d, want 3", report.TotalRuns)
		}
		if len(report.ByModel) < 2 {
			t.Errorf("expected at least 2 model entries, got %d", len(report.ByModel))
		}
		if len(report.ByBucket) == 0 {
			t.Error("expected at least 1 day bucket")
		}
	})

	t.Run("HourGranularity", func(t *testing.T) {
		start := now.Add(-24 * time.Hour)
		report, err := store.QueryUsage(ctx, tenantID, start, now.Add(time.Hour), "hour")
		if err != nil {
			t.Fatalf("QueryUsage: %v", err)
		}
		if report.TotalTokens != 1800 {
			t.Errorf("total_tokens = %d, want 1800", report.TotalTokens)
		}
		// With hour granularity, expect 2-3 buckets.
		if len(report.ByBucket) < 2 {
			t.Errorf("expected at least 2 hour buckets, got %d", len(report.ByBucket))
		}
	})

	t.Run("EmptyRange", func(t *testing.T) {
		start := now.Add(-48 * time.Hour)
		end := now.Add(-47 * time.Hour)
		report, err := store.QueryUsage(ctx, tenantID, start, end, "day")
		if err != nil {
			t.Fatalf("QueryUsage: %v", err)
		}
		if report.TotalTokens != 0 {
			t.Errorf("expected 0 total_tokens, got %d", report.TotalTokens)
		}
		if report.TotalRuns != 0 {
			t.Errorf("expected 0 total_runs, got %d", report.TotalRuns)
		}
	})
}
