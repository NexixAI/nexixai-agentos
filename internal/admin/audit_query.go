package admin

import (
	"sort"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
)

// AuditQueryParams holds the query parameters for paginated audit log queries.
type AuditQueryParams struct {
	Start  string // RFC3339 start time filter
	End    string // RFC3339 end time filter
	Action string // action filter (e.g. "runs.create")
	Limit  int    // max entries to return (default 50, max 200)
	After  string // cursor: RFC3339 timestamp to paginate after
}

// AuditQueryResult holds the paginated result of an audit log query.
type AuditQueryResult struct {
	Entries []audit.Entry `json:"entries"`
	HasMore bool          `json:"has_more"`
}

// AuditReader is an optional interface that audit loggers can implement
// to support reading entries back for queries.
type AuditReader interface {
	ReadAll() []audit.Entry
}

// QueryAuditLog queries the audit log with filtering and pagination.
// If the audit logger does not implement AuditReader, an empty result is returned.
func QueryAuditLog(logger audit.Logger, params AuditQueryParams) AuditQueryResult {
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 200 {
		params.Limit = 200
	}

	reader, ok := logger.(AuditReader)
	if !ok {
		return AuditQueryResult{Entries: []audit.Entry{}}
	}

	entries := reader.ReadAll()

	// Sort entries by time ascending for deterministic pagination.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Time < entries[j].Time
	})

	// Apply time filters.
	if params.Start != "" {
		startTime, err := time.Parse(time.RFC3339, params.Start)
		if err == nil {
			filtered := make([]audit.Entry, 0, len(entries))
			for _, e := range entries {
				t, err := time.Parse(time.RFC3339, e.Time)
				if err != nil {
					continue
				}
				if !t.Before(startTime) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}
	}

	if params.End != "" {
		endTime, err := time.Parse(time.RFC3339, params.End)
		if err == nil {
			filtered := make([]audit.Entry, 0, len(entries))
			for _, e := range entries {
				t, err := time.Parse(time.RFC3339, e.Time)
				if err != nil {
					continue
				}
				if !t.After(endTime) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}
	}

	// Apply action filter.
	if params.Action != "" {
		filtered := make([]audit.Entry, 0, len(entries))
		for _, e := range entries {
			if e.Action == params.Action {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	// Apply cursor: skip entries with Time <= after.
	if params.After != "" {
		idx := 0
		for idx < len(entries) && entries[idx].Time <= params.After {
			idx++
		}
		entries = entries[idx:]
	}

	hasMore := len(entries) > params.Limit
	if hasMore {
		entries = entries[:params.Limit]
	}

	if entries == nil {
		entries = []audit.Entry{}
	}

	return AuditQueryResult{
		Entries: entries,
		HasMore: hasMore,
	}
}
