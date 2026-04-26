package tenants

import (
	"errors"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

var (
	ErrInvalidTenant = errors.New("invalid tenant")
	ErrTenantExists  = errors.New("tenant already exists")
	ErrNotFound      = errors.New("tenant not found")
)

type Store struct {
	mu       sync.RWMutex
	tenants  map[string]types.Tenant
	defaults map[string]struct{}
}

func NewStore() *Store {
	return &Store{
		tenants:  make(map[string]types.Tenant),
		defaults: make(map[string]struct{}),
	}
}

// EnsureDefault seeds a tenant if it does not exist.
func (s *Store) EnsureDefault(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		s.defaults[id] = struct{}{}
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	s.tenants[id] = types.Tenant{
		TenantID:  id,
		Name:      "default tenant",
		Status:    "active",
		PlanTier:  "default",
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.defaults[id] = struct{}{}
}

func (s *Store) Create(t types.Tenant) error {
	if t.TenantID == "" {
		return ErrInvalidTenant
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if t.Status == "" {
		t.Status = "active"
	}
	if t.CreatedAt == "" {
		t.CreatedAt = now
	}
	t.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tenants[t.TenantID]; exists {
		return ErrTenantExists
	}
	s.tenants[t.TenantID] = t
	return nil
}

func (s *Store) Update(id string, update types.Tenant) (types.Tenant, error) {
	if id == "" {
		return types.Tenant{}, ErrInvalidTenant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.tenants[id]
	if !ok {
		return types.Tenant{}, ErrNotFound
	}

	if update.Name != "" {
		cur.Name = update.Name
	}
	if update.PlanTier != "" {
		cur.PlanTier = update.PlanTier
	}
	if update.Status != "" {
		cur.Status = update.Status
	}
	if update.Entitlements != nil {
		cur.Entitlements = update.Entitlements
	}
	if update.Quotas != nil {
		cur.Quotas = update.Quotas
	}
	if update.Metadata != nil {
		cur.Metadata = update.Metadata
	}
	if update.Policy != nil {
		cur.Policy = update.Policy
	}
	cur.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.tenants[id] = cur
	return cur, nil
}

func (s *Store) Delete(id string) (types.Tenant, error) {
	if id == "" {
		return types.Tenant{}, ErrInvalidTenant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.tenants[id]
	if !ok {
		return types.Tenant{}, ErrNotFound
	}
	// prevent deleting seeded defaults
	if _, isDefault := s.defaults[id]; isDefault {
		return types.Tenant{}, errors.New("cannot delete default tenant")
	}
	delete(s.tenants, id)
	return cur, nil
}

func (s *Store) Get(id string) (types.Tenant, bool) {
	if id == "" {
		return types.Tenant{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[id]
	return t, ok
}

func (s *Store) List() []types.Tenant {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]types.Tenant, 0, len(s.tenants))
	for _, t := range s.tenants {
		out = append(out, t)
	}
	return out
}
