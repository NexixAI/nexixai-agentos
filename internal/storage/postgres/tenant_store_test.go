//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_TenantStore_CRUD(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewTenantStoreFromDB(db)
	ctx := context.Background()
	tenantID := "tnt_integ_" + t.Name()
	slug := "integ-test-slug"

	// Cleanup after test.
	defer func() {
		db.ExecContext(ctx, "DELETE FROM tenant_members WHERE tenant_id = $1", tenantID)
		db.ExecContext(ctx, "DELETE FROM tenants WHERE tenant_id = $1", tenantID)
	}()

	now := time.Now().UTC()
	tenant := Tenant{
		TenantID:  tenantID,
		Name:      "Integration Test Tenant",
		Slug:      slug,
		Plan:      "starter",
		CreatedAt: now,
		UpdatedAt: now,
	}

	t.Run("Create", func(t *testing.T) {
		if err := store.Create(ctx, tenant); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})

	t.Run("Create_SlugConflict", func(t *testing.T) {
		dup := tenant
		dup.TenantID = "tnt_dup_slug"
		err := store.Create(ctx, dup)
		if err != ErrSlugConflict {
			t.Fatalf("expected ErrSlugConflict, got %v", err)
		}
		// Clean up partial
		db.ExecContext(ctx, "DELETE FROM tenants WHERE tenant_id = $1", "tnt_dup_slug")
	})

	t.Run("Get", func(t *testing.T) {
		got, err := store.Get(ctx, tenantID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Name != tenant.Name {
			t.Errorf("Get name = %q, want %q", got.Name, tenant.Name)
		}
		if got.Slug != slug {
			t.Errorf("Get slug = %q, want %q", got.Slug, slug)
		}
		if got.Plan != "starter" {
			t.Errorf("Get plan = %q, want %q", got.Plan, "starter")
		}
	})

	t.Run("Get_NotFound", func(t *testing.T) {
		_, err := store.Get(ctx, "nonexistent")
		if err != ErrTenantNotFound {
			t.Errorf("expected ErrTenantNotFound, got %v", err)
		}
	})

	t.Run("Update_Name", func(t *testing.T) {
		newName := "Updated Name"
		if err := store.Update(ctx, tenantID, TenantUpdate{Name: &newName}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err := store.Get(ctx, tenantID)
		if err != nil {
			t.Fatalf("Get after Update: %v", err)
		}
		if got.Name != newName {
			t.Errorf("name after update = %q, want %q", got.Name, newName)
		}
		if got.Plan != "starter" {
			t.Errorf("plan changed unexpectedly to %q", got.Plan)
		}
	})

	t.Run("Update_Plan", func(t *testing.T) {
		newPlan := "pro"
		if err := store.Update(ctx, tenantID, TenantUpdate{Plan: &newPlan}); err != nil {
			t.Fatalf("Update plan: %v", err)
		}
		got, err := store.Get(ctx, tenantID)
		if err != nil {
			t.Fatalf("Get after Update plan: %v", err)
		}
		if got.Plan != "pro" {
			t.Errorf("plan after update = %q, want %q", got.Plan, "pro")
		}
	})

	t.Run("Update_NotFound", func(t *testing.T) {
		name := "x"
		err := store.Update(ctx, "nonexistent", TenantUpdate{Name: &name})
		if err != ErrTenantNotFound {
			t.Errorf("expected ErrTenantNotFound, got %v", err)
		}
	})

	t.Run("AddMember", func(t *testing.T) {
		err := store.AddMember(ctx, TenantMember{
			TenantID:    tenantID,
			PrincipalID: "user_test_owner",
			Role:        "owner",
		})
		if err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	})

	t.Run("List", func(t *testing.T) {
		tenants, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		found := false
		for _, tt := range tenants {
			if tt.TenantID == tenantID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tenant %q not found in List results", tenantID)
		}
	})

	t.Run("SoftDelete", func(t *testing.T) {
		if err := store.Delete(ctx, tenantID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		// Should not be found after soft delete.
		_, err := store.Get(ctx, tenantID)
		if err != ErrTenantNotFound {
			t.Errorf("expected ErrTenantNotFound after delete, got %v", err)
		}
	})

	t.Run("Delete_NotFound", func(t *testing.T) {
		err := store.Delete(ctx, "nonexistent")
		if err != ErrTenantNotFound {
			t.Errorf("expected ErrTenantNotFound, got %v", err)
		}
	})
}
