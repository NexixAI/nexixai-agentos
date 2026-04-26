//go:build integration

package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("AGENTOS_DB_HOST")
	if host == "" {
		t.Skip("AGENTOS_DB_HOST not set — skipping integration test")
	}
	cfg := config.PostgresConfig{
		Host:     host,
		Port:     5432,
		Name:     os.Getenv("AGENTOS_DB_NAME"),
		User:     os.Getenv("AGENTOS_DB_USER"),
		Password: os.Getenv("AGENTOS_DB_PASSWORD"),
		SSLMode:  "disable",
		PoolSize: 2,
	}
	db, err := postgres.OpenDB(cfg)
	if err != nil {
		t.Fatalf("failed to connect to postgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Clean tables for test isolation.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := postgres.Migrate(db); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	_, _ = db.ExecContext(ctx, "DELETE FROM api_keys")
	_, _ = db.ExecContext(ctx, "DELETE FROM tenant_members")
	_, _ = db.ExecContext(ctx, "DELETE FROM tenants")

	return db
}

func TestBootstrapRun_Success(t *testing.T) {
	db := testDB(t)

	result, err := bootstrapRun(db, "tnt_bootstrap_test", "Test Tenant", "test-key")
	if err != nil {
		t.Fatalf("bootstrapRun failed: %v", err)
	}

	if result.TenantID != "tnt_bootstrap_test" {
		t.Errorf("expected tenant_id=tnt_bootstrap_test, got %s", result.TenantID)
	}
	if !strings.HasPrefix(result.APIKey, "aos_live_") {
		t.Errorf("expected API key to start with aos_live_, got %s", result.APIKey)
	}
	if result.KeyID == "" {
		t.Error("expected non-empty key_id")
	}

	// Verify tenant exists in DB.
	var name string
	err = db.QueryRow("SELECT name FROM tenants WHERE tenant_id = $1", "tnt_bootstrap_test").Scan(&name)
	if err != nil {
		t.Fatalf("tenant not found in DB: %v", err)
	}
	if name != "Test Tenant" {
		t.Errorf("expected tenant name 'Test Tenant', got '%s'", name)
	}

	// Verify API key exists and validates.
	store := postgres.NewAPIKeyStore(db)
	prefix := auth.ExtractPrefix(result.APIKey)
	rec, err := store.GetByPrefix(context.Background(), "tnt_bootstrap_test", prefix)
	if err != nil {
		t.Fatalf("API key not found in DB: %v", err)
	}
	if rec.Role != "owner" {
		t.Errorf("expected role=owner, got %s", rec.Role)
	}
	if rec.Name != "test-key" {
		t.Errorf("expected name=test-key, got %s", rec.Name)
	}
	if !auth.ValidateKey(result.APIKey, rec.KeyHash) {
		t.Error("API key hash validation failed")
	}
}

func TestBootstrapRun_RefusesIfTenantsExist(t *testing.T) {
	db := testDB(t)

	// First bootstrap succeeds.
	_, err := bootstrapRun(db, "tnt_first", "", "key1")
	if err != nil {
		t.Fatalf("first bootstrap failed: %v", err)
	}

	// Second bootstrap must fail.
	_, err = bootstrapRun(db, "tnt_second", "", "key2")
	if err == nil {
		t.Fatal("expected error on second bootstrap, got nil")
	}
	if !strings.Contains(err.Error(), "tenants already exist") {
		t.Errorf("expected 'tenants already exist' error, got: %v", err)
	}
}

func TestBootstrapRun_DefaultsNameToTenantID(t *testing.T) {
	db := testDB(t)

	result, err := bootstrapRun(db, "tnt_noname", "", "key")
	if err != nil {
		t.Fatalf("bootstrapRun failed: %v", err)
	}

	var name string
	err = db.QueryRow("SELECT name FROM tenants WHERE tenant_id = $1", result.TenantID).Scan(&name)
	if err != nil {
		t.Fatalf("tenant not found: %v", err)
	}
	if name != "tnt_noname" {
		t.Errorf("expected name to default to tenant_id 'tnt_noname', got '%s'", name)
	}
}
