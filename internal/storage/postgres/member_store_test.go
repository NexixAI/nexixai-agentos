//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_MemberStore_SetAndGetRole(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewMemberStore(db)
	ctx := context.Background()

	if err := store.SetRole(ctx, "tnt_ms1", "usr_1", "developer"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}

	role, err := store.GetRole(ctx, "tnt_ms1", "usr_1")
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if role != "developer" {
		t.Fatalf("expected developer, got %s", role)
	}

	// Upsert to admin.
	if err := store.SetRole(ctx, "tnt_ms1", "usr_1", "admin"); err != nil {
		t.Fatalf("SetRole upsert: %v", err)
	}
	role, err = store.GetRole(ctx, "tnt_ms1", "usr_1")
	if err != nil {
		t.Fatalf("GetRole after upsert: %v", err)
	}
	if role != "admin" {
		t.Fatalf("expected admin, got %s", role)
	}
}

func TestIntegration_MemberStore_GetRole_NotFound(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewMemberStore(db)
	ctx := context.Background()

	_, err := store.GetRole(ctx, "tnt_nope", "usr_nope")
	if !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("expected ErrMemberNotFound, got %v", err)
	}
}

func TestIntegration_MemberStore_CountMembers(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewMemberStore(db)
	ctx := context.Background()

	count, err := store.CountMembers(ctx, "tnt_count_empty")
	if err != nil {
		t.Fatalf("CountMembers: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 members, got %d", count)
	}

	if err := store.SetRole(ctx, "tnt_count_empty", "usr_a", "viewer"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if err := store.SetRole(ctx, "tnt_count_empty", "usr_b", "developer"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}

	count, err = store.CountMembers(ctx, "tnt_count_empty")
	if err != nil {
		t.Fatalf("CountMembers: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 members, got %d", count)
	}
}

func TestIntegration_MemberStore_DeleteMember(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewMemberStore(db)
	ctx := context.Background()

	if err := store.SetRole(ctx, "tnt_del", "usr_del", "viewer"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}

	if err := store.DeleteMember(ctx, "tnt_del", "usr_del"); err != nil {
		t.Fatalf("DeleteMember: %v", err)
	}

	_, err := store.GetRole(ctx, "tnt_del", "usr_del")
	if !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("expected ErrMemberNotFound after delete, got %v", err)
	}

	// Delete non-existent should return ErrMemberNotFound.
	err = store.DeleteMember(ctx, "tnt_del", "usr_nope")
	if !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("expected ErrMemberNotFound for missing member, got %v", err)
	}
}

func TestIntegration_MemberStore_ListMembers(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewMemberStore(db)
	ctx := context.Background()

	if err := store.SetRole(ctx, "tnt_list", "usr_x", "admin"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if err := store.SetRole(ctx, "tnt_list", "usr_y", "viewer"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}

	members, err := store.ListMembers(ctx, "tnt_list")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) < 2 {
		t.Fatalf("expected at least 2 members, got %d", len(members))
	}
}
