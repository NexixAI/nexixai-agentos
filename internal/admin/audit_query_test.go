package admin

import (
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
)

// mockAuditLogger is an in-memory audit logger that supports AuditReader.
type mockAuditLogger struct {
	entries []audit.Entry
}

func (m *mockAuditLogger) Log(e audit.Entry) {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	m.entries = append(m.entries, e)
}

func (m *mockAuditLogger) Close() error { return nil }

func (m *mockAuditLogger) ReadAll() []audit.Entry {
	out := make([]audit.Entry, len(m.entries))
	copy(out, m.entries)
	return out
}

func newMockLogger(n int) *mockAuditLogger {
	m := &mockAuditLogger{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		action := "runs.create"
		if i%3 == 0 {
			action = "tenants.create"
		}
		m.Log(audit.Entry{
			Time:     base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			TenantID: "tnt_test",
			Action:   action,
			Resource: "test",
			Outcome:  "allowed",
		})
	}
	return m
}

func TestQueryAuditLog_DefaultLimit(t *testing.T) {
	logger := newMockLogger(100)
	result := QueryAuditLog(logger, AuditQueryParams{})

	if len(result.Entries) != 50 {
		t.Fatalf("expected 50 entries with default limit, got %d", len(result.Entries))
	}
	if !result.HasMore {
		t.Fatal("expected has_more=true with 100 entries and limit=50")
	}
}

func TestQueryAuditLog_CustomLimit(t *testing.T) {
	logger := newMockLogger(10)
	result := QueryAuditLog(logger, AuditQueryParams{Limit: 5})

	if len(result.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(result.Entries))
	}
	if !result.HasMore {
		t.Fatal("expected has_more=true")
	}
}

func TestQueryAuditLog_MaxLimit(t *testing.T) {
	logger := newMockLogger(300)
	result := QueryAuditLog(logger, AuditQueryParams{Limit: 500})

	if len(result.Entries) != 200 {
		t.Fatalf("expected max 200 entries, got %d", len(result.Entries))
	}
	if !result.HasMore {
		t.Fatal("expected has_more=true with 300 entries and max limit 200")
	}
}

func TestQueryAuditLog_EmptyResult(t *testing.T) {
	logger := newMockLogger(0)
	result := QueryAuditLog(logger, AuditQueryParams{})

	if len(result.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(result.Entries))
	}
	if result.HasMore {
		t.Fatal("expected has_more=false for empty result")
	}
}

func TestQueryAuditLog_CursorPagination(t *testing.T) {
	logger := newMockLogger(10)

	// Get first page.
	page1 := QueryAuditLog(logger, AuditQueryParams{Limit: 3})
	if len(page1.Entries) != 3 {
		t.Fatalf("page 1: expected 3 entries, got %d", len(page1.Entries))
	}
	if !page1.HasMore {
		t.Fatal("page 1: expected has_more=true")
	}

	// Get second page using cursor from last entry of page 1.
	cursor := page1.Entries[len(page1.Entries)-1].Time
	page2 := QueryAuditLog(logger, AuditQueryParams{Limit: 3, After: cursor})
	if len(page2.Entries) != 3 {
		t.Fatalf("page 2: expected 3 entries, got %d", len(page2.Entries))
	}

	// Ensure no overlap between pages.
	for _, e1 := range page1.Entries {
		for _, e2 := range page2.Entries {
			if e1.Time == e2.Time {
				t.Fatalf("overlap detected: entry with time %s appears in both pages", e1.Time)
			}
		}
	}
}

func TestQueryAuditLog_CursorBeyondEnd(t *testing.T) {
	logger := newMockLogger(5)
	result := QueryAuditLog(logger, AuditQueryParams{
		After: "2030-01-01T00:00:00Z",
	})

	if len(result.Entries) != 0 {
		t.Fatalf("expected 0 entries for cursor beyond end, got %d", len(result.Entries))
	}
	if result.HasMore {
		t.Fatal("expected has_more=false for cursor beyond end")
	}
}

func TestQueryAuditLog_ActionFilter(t *testing.T) {
	logger := newMockLogger(10)
	result := QueryAuditLog(logger, AuditQueryParams{
		Action: "tenants.create",
		Limit:  100,
	})

	for _, e := range result.Entries {
		if e.Action != "tenants.create" {
			t.Fatalf("expected action=tenants.create, got %s", e.Action)
		}
	}

	if len(result.Entries) == 0 {
		t.Fatal("expected some entries with action=tenants.create")
	}
}

func TestQueryAuditLog_TimeFilters(t *testing.T) {
	logger := newMockLogger(10)
	// Entries are at 2026-01-01T00:00:00Z through 2026-01-01T00:09:00Z

	result := QueryAuditLog(logger, AuditQueryParams{
		Start: "2026-01-01T00:03:00Z",
		End:   "2026-01-01T00:06:00Z",
		Limit: 100,
	})

	for _, e := range result.Entries {
		ts, err := time.Parse(time.RFC3339, e.Time)
		if err != nil {
			t.Fatalf("invalid time in entry: %s", e.Time)
		}
		start := time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC)
		end := time.Date(2026, 1, 1, 0, 6, 0, 0, time.UTC)
		if ts.Before(start) || ts.After(end) {
			t.Fatalf("entry time %s is outside range [%s, %s]", e.Time, start, end)
		}
	}

	// Should have entries at minutes 3, 4, 5, 6 = 4 entries.
	if len(result.Entries) != 4 {
		t.Fatalf("expected 4 entries in time range, got %d", len(result.Entries))
	}
}

func TestQueryAuditLog_NonReaderLogger(t *testing.T) {
	// Use an audit logger that does not implement AuditReader.
	logger := audit.NewFromEnv()
	defer func() {
		if err := logger.Close(); err != nil {
			t.Logf("logger close error: %v", err)
		}
	}()

	result := QueryAuditLog(logger, AuditQueryParams{})
	if len(result.Entries) != 0 {
		t.Fatalf("expected 0 entries for non-reader logger, got %d", len(result.Entries))
	}
	if result.HasMore {
		t.Fatal("expected has_more=false for non-reader logger")
	}
}

func TestQueryAuditLog_ExactBoundary(t *testing.T) {
	// Exactly limit entries: has_more should be false.
	logger := newMockLogger(5)
	result := QueryAuditLog(logger, AuditQueryParams{Limit: 5})

	if len(result.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(result.Entries))
	}
	if result.HasMore {
		t.Fatal("expected has_more=false when count == limit")
	}
}

func TestQueryAuditLog_LimitPlusOne(t *testing.T) {
	// Limit+1 entries: has_more should be true with limit entries returned.
	logger := newMockLogger(6)
	result := QueryAuditLog(logger, AuditQueryParams{Limit: 5})

	if len(result.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(result.Entries))
	}
	if !result.HasMore {
		t.Fatal("expected has_more=true when count > limit")
	}
}

func TestQueryAuditLog_CombinedFiltersAndPagination(t *testing.T) {
	logger := newMockLogger(30)
	// Filter by action and paginate.
	result := QueryAuditLog(logger, AuditQueryParams{
		Action: "tenants.create",
		Limit:  3,
	})

	if len(result.Entries) > 3 {
		t.Fatalf("expected at most 3 entries, got %d", len(result.Entries))
	}
	for _, e := range result.Entries {
		if e.Action != "tenants.create" {
			t.Fatalf("expected action=tenants.create, got %s", e.Action)
		}
	}
}
