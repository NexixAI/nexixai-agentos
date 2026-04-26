package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/mcp"
)

// ClearanceStore retrieves and sets agent clearance tiers.
// Implementations MUST return an error for unknown agents (fail closed).
type ClearanceStore interface {
	GetClearance(ctx context.Context, agentID string) (mcp.ClearanceTier, error)
	SetClearance(ctx context.Context, agentID, tenantID string, tier mcp.ClearanceTier) error
}

// PostgresClearanceStore implements ClearanceStore backed by Postgres.
type PostgresClearanceStore struct {
	db *sql.DB
}

// NewPostgresClearanceStore creates a ClearanceStore backed by the given database.
func NewPostgresClearanceStore(db *sql.DB) *PostgresClearanceStore {
	return &PostgresClearanceStore{db: db}
}

// GetClearance returns the clearance tier for the given agent.
// If the agent is not found, it returns an error (fail closed — no default tier).
func (s *PostgresClearanceStore) GetClearance(ctx context.Context, agentID string) (mcp.ClearanceTier, error) {
	if agentID == "" {
		return 0, fmt.Errorf("clearance: agent_id must not be empty")
	}

	var tier int
	err := s.db.QueryRowContext(ctx,
		"SELECT tier FROM agent_clearance WHERE agent_id = $1",
		agentID,
	).Scan(&tier)

	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("clearance: agent %q not found", agentID)
	}
	if err != nil {
		return 0, fmt.Errorf("clearance: query failed: %w", err)
	}

	return mcp.ClearanceTier(tier), nil
}

// SetClearance upserts the clearance tier for the given agent.
func (s *PostgresClearanceStore) SetClearance(ctx context.Context, agentID, tenantID string, tier mcp.ClearanceTier) error {
	if agentID == "" {
		return fmt.Errorf("clearance: agent_id must not be empty")
	}
	if tenantID == "" {
		return fmt.Errorf("clearance: tenant_id must not be empty")
	}

	now := time.Now().UTC()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_clearance (agent_id, tenant_id, tier, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (agent_id) DO UPDATE
		 SET tier = EXCLUDED.tier, tenant_id = EXCLUDED.tenant_id, updated_at = EXCLUDED.updated_at`,
		agentID, tenantID, int(tier), now, now,
	)
	if err != nil {
		return fmt.Errorf("clearance: upsert failed: %w", err)
	}

	return nil
}

// --- Elevation Request types and store ---

// ElevationRequest represents a pending, approved, or denied clearance
// elevation request from an agent.
type ElevationRequest struct {
	ID            string     `json:"id"`
	AgentID       string     `json:"agent_id"`
	TenantID      string     `json:"tenant_id"`
	RequestedTier int        `json:"requested_tier"`
	Reason        string     `json:"reason"`
	Status        string     `json:"status"` // "pending", "approved", "denied"
	ApprovedAt    *time.Time `json:"approved_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ElevationStore manages elevation requests for human review.
type ElevationStore interface {
	RequestElevation(ctx context.Context, agentID, tenantID string, requestedTier int, reason string) (string, error)
	GetElevationStatus(ctx context.Context, requestID string) (*ElevationRequest, error)
	ApproveElevation(ctx context.Context, requestID string, duration time.Duration) error
	DenyElevation(ctx context.Context, requestID string) error
}

// InMemoryElevationStore implements ElevationStore using an in-memory map
// protected by a sync.RWMutex. Suitable for development and testing;
// production should use Postgres.
type InMemoryElevationStore struct {
	mu       sync.RWMutex
	requests map[string]*ElevationRequest
}

// NewInMemoryElevationStore creates an empty in-memory elevation store.
func NewInMemoryElevationStore() *InMemoryElevationStore {
	return &InMemoryElevationStore{
		requests: make(map[string]*ElevationRequest),
	}
}

// RequestElevation creates a new pending elevation request and returns its ID.
func (s *InMemoryElevationStore) RequestElevation(_ context.Context, agentID, tenantID string, requestedTier int, reason string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("elevation: agent_id must not be empty")
	}
	if tenantID == "" {
		return "", fmt.Errorf("elevation: tenant_id must not be empty")
	}
	if requestedTier < 0 || requestedTier > 4 {
		return "", fmt.Errorf("elevation: requested_tier must be 0-4, got %d", requestedTier)
	}
	if reason == "" {
		return "", fmt.Errorf("elevation: reason must not be empty")
	}

	id, err := elevationUUID()
	if err != nil {
		return "", fmt.Errorf("elevation: generate ID: %w", err)
	}

	req := &ElevationRequest{
		ID:            id,
		AgentID:       agentID,
		TenantID:      tenantID,
		RequestedTier: requestedTier,
		Reason:        reason,
		Status:        "pending",
		CreatedAt:     time.Now().UTC(),
	}

	s.mu.Lock()
	s.requests[id] = req
	s.mu.Unlock()

	return id, nil
}

// GetElevationStatus returns the elevation request for the given ID.
func (s *InMemoryElevationStore) GetElevationStatus(_ context.Context, requestID string) (*ElevationRequest, error) {
	if requestID == "" {
		return nil, fmt.Errorf("elevation: request_id must not be empty")
	}

	s.mu.RLock()
	req, ok := s.requests[requestID]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("elevation: request %q not found", requestID)
	}

	// Return a copy to avoid data races on the pointer fields.
	cp := *req
	return &cp, nil
}

// ApproveElevation sets the request status to "approved" with an expiry.
func (s *InMemoryElevationStore) ApproveElevation(_ context.Context, requestID string, duration time.Duration) error {
	if requestID == "" {
		return fmt.Errorf("elevation: request_id must not be empty")
	}
	if duration <= 0 {
		return fmt.Errorf("elevation: duration must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	req, ok := s.requests[requestID]
	if !ok {
		return fmt.Errorf("elevation: request %q not found", requestID)
	}

	now := time.Now().UTC()
	expires := now.Add(duration)
	req.Status = "approved"
	req.ApprovedAt = &now
	req.ExpiresAt = &expires

	return nil
}

// DenyElevation sets the request status to "denied".
func (s *InMemoryElevationStore) DenyElevation(_ context.Context, requestID string) error {
	if requestID == "" {
		return fmt.Errorf("elevation: request_id must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	req, ok := s.requests[requestID]
	if !ok {
		return fmt.Errorf("elevation: request %q not found", requestID)
	}

	req.Status = "denied"
	return nil
}

// elevationUUID generates a UUID v4 string using crypto/rand.
func elevationUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
