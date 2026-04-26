package tenants

import (
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestStoreCRUD(t *testing.T) {
	s := NewStore()
	s.EnsureDefault("tnt_default")

	if _, ok := s.Get("tnt_default"); !ok {
		t.Fatalf("expected default tenant to exist")
	}

	err := s.Create(types.Tenant{TenantID: "tnt_new", Name: "New"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Create(types.Tenant{TenantID: "tnt_new"}); err == nil {
		t.Fatalf("expected duplicate create to fail")
	}

	updated, err := s.Update("tnt_new", types.Tenant{Status: "suspended"})
	if err != nil || updated.Status != "suspended" {
		t.Fatalf("update failed: %v", err)
	}

	if _, err := s.Delete("tnt_default"); err == nil {
		t.Fatalf("expected delete default to fail")
	}
	if _, err := s.Delete("tnt_new"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestStore_CreateEmptyID_ReturnsError(t *testing.T) {
	s := NewStore()
	err := s.Create(types.Tenant{TenantID: ""})
	if err == nil {
		t.Fatal("expected error creating tenant with empty ID")
	}
	if err != ErrInvalidTenant {
		t.Fatalf("expected ErrInvalidTenant, got %v", err)
	}
}

func TestStore_UpdateNonExistent_ReturnsError(t *testing.T) {
	s := NewStore()
	_, err := s.Update("tnt_ghost", types.Tenant{Name: "Ghost"})
	if err == nil {
		t.Fatal("expected error updating non-existent tenant")
	}
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_UpdateEmptyID_ReturnsError(t *testing.T) {
	s := NewStore()
	_, err := s.Update("", types.Tenant{Name: "Bad"})
	if err == nil {
		t.Fatal("expected error updating with empty ID")
	}
	if err != ErrInvalidTenant {
		t.Fatalf("expected ErrInvalidTenant, got %v", err)
	}
}

func TestStore_DeleteNonExistent_ReturnsError(t *testing.T) {
	s := NewStore()
	_, err := s.Delete("tnt_missing")
	if err == nil {
		t.Fatal("expected error deleting non-existent tenant")
	}
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_DeleteEmptyID_ReturnsError(t *testing.T) {
	s := NewStore()
	_, err := s.Delete("")
	if err == nil {
		t.Fatal("expected error deleting with empty ID")
	}
	if err != ErrInvalidTenant {
		t.Fatalf("expected ErrInvalidTenant, got %v", err)
	}
}

func TestStore_GetEmptyID_ReturnsFalse(t *testing.T) {
	s := NewStore()
	_, ok := s.Get("")
	if ok {
		t.Fatal("expected Get with empty ID to return false")
	}
}

func TestStore_EnsureDefaultEmpty_Noop(t *testing.T) {
	s := NewStore()
	s.EnsureDefault("")
	if len(s.List()) != 0 {
		t.Fatal("expected no tenants after EnsureDefault with empty ID")
	}
}
