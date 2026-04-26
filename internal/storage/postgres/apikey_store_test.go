//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIntegration_APIKeyStore_CreateAndGetByPrefix(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewAPIKeyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	rec := APIKeyRecord{
		KeyID:     "key_int_test1",
		TenantID:  "tnt_ak1",
		KeyHash:   "$2a$10$fakehashfakehashfakehashfakehashfakehashfakehashfake",
		KeyPrefix: "abcd1234",
		Name:      "test key",
		Role:      "developer",
		CreatedBy: "usr_creator",
		CreatedAt: now,
	}

	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByPrefix(ctx, "tnt_ak1", "abcd1234")
	if err != nil {
		t.Fatalf("GetByPrefix: %v", err)
	}
	if got.KeyID != "key_int_test1" {
		t.Errorf("expected key_int_test1, got %s", got.KeyID)
	}
	if got.Role != "developer" {
		t.Errorf("expected developer, got %s", got.Role)
	}
	if got.KeyHash == "" {
		t.Error("expected non-empty hash from GetByPrefix")
	}
}

func TestIntegration_APIKeyStore_GetByPrefix_NotFound(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewAPIKeyStore(db)
	ctx := context.Background()

	_, err := store.GetByPrefix(ctx, "tnt_nope", "nope1234")
	if !errors.Is(err, ErrAPIKeyNotFound) {
		t.Fatalf("expected ErrAPIKeyNotFound, got %v", err)
	}
}

func TestIntegration_APIKeyStore_List(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewAPIKeyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	for i, name := range []string{"key_list_a", "key_list_b"} {
		rec := APIKeyRecord{
			KeyID:     name,
			TenantID:  "tnt_ak_list",
			KeyHash:   "$2a$10$fakehash",
			KeyPrefix: "list" + string(rune('a'+i)) + "123",
			Name:      name,
			Role:      "viewer",
			CreatedBy: "usr_lister",
			CreatedAt: now,
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	keys, err := store.List(ctx, "tnt_ak_list")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) < 2 {
		t.Fatalf("expected at least 2 keys, got %d", len(keys))
	}
	// Hashes must be omitted in list.
	for _, k := range keys {
		if k.KeyHash != "" {
			t.Errorf("List should not include key hash, but got %q for %s", k.KeyHash, k.KeyID)
		}
	}
}

func TestIntegration_APIKeyStore_Revoke(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	store := NewAPIKeyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	rec := APIKeyRecord{
		KeyID:     "key_revoke_test",
		TenantID:  "tnt_ak_revoke",
		KeyHash:   "$2a$10$fakehash",
		KeyPrefix: "revo1234",
		Name:      "revoke me",
		Role:      "developer",
		CreatedBy: "usr_rev",
		CreatedAt: now,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Revoke(ctx, "tnt_ak_revoke", "key_revoke_test"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	got, err := store.GetByPrefix(ctx, "tnt_ak_revoke", "revo1234")
	if err != nil {
		t.Fatalf("GetByPrefix after revoke: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("expected non-nil RevokedAt after revoke")
	}

	// Revoking again should return ErrAPIKeyNotFound (already revoked).
	err = store.Revoke(ctx, "tnt_ak_revoke", "key_revoke_test")
	if !errors.Is(err, ErrAPIKeyNotFound) {
		t.Fatalf("expected ErrAPIKeyNotFound on double revoke, got %v", err)
	}
}
