package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/auth"
	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/storage/postgres"
)

// bootstrapResult holds the output of a successful bootstrap operation.
type bootstrapResult struct {
	TenantID string
	KeyID    string
	APIKey   string
}

func bootstrap(args []string) {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	tenantID := fs.String("tenant-id", "", "tenant ID to create (required)")
	tenantName := fs.String("name", "", "tenant display name")
	keyName := fs.String("key-name", "bootstrap", "name for the generated API key")
	if err := fs.Parse(args); err != nil {
		slog.Error("failed to parse flags", "error", err)
		os.Exit(2)
	}

	if strings.TrimSpace(*tenantID) == "" {
		slog.Error("--tenant-id is required")
		fmt.Fprintln(os.Stderr, "Usage: agentos bootstrap --tenant-id TENANT_ID [--name NAME] [--key-name KEY_NAME]")
		os.Exit(2)
	}

	cfg := config.LoadFromEnv()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid storage config — set AGENTOS_DB_* env vars", "error", err)
		os.Exit(1)
	}
	if strings.ToLower(cfg.Backend) != "postgres" {
		slog.Error("bootstrap requires AGENTOS_STORAGE_BACKEND=postgres")
		os.Exit(1)
	}

	db, err := postgres.OpenDB(cfg.Postgres)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	result, err := bootstrapRun(db, *tenantID, *tenantName, *keyName)
	if err != nil {
		slog.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}

	slog.Info("tenant created", "tenant_id", result.TenantID)
	slog.Info("API key created", "key_id", result.KeyID, "role", "owner")

	fmt.Println()
	fmt.Println("=== Bootstrap Complete ===")
	fmt.Printf("Tenant ID: %s\n", result.TenantID)
	fmt.Printf("API Key:   %s\n", result.APIKey)
	fmt.Println()
	fmt.Println("Save this key — it cannot be retrieved later.")
	fmt.Println("Set it as OPENAI_API_KEY in your Open WebUI stack.")
}

// bootstrapRun is the testable core of the bootstrap command.
// It creates the first tenant and owner API key, returning the plaintext key.
// Returns an error if tenants already exist or any DB operation fails.
func bootstrapRun(db *sql.DB, tenantID, tenantName, keyName string) (*bootstrapResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run migrations (idempotent).
	if err := postgres.Migrate(db); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// Check if any tenants already exist.
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants").Scan(&count); err != nil {
		return nil, fmt.Errorf("check existing tenants: %w", err)
	}
	if count > 0 {
		return nil, fmt.Errorf("tenants already exist (%d) — bootstrap is for first-time setup only", count)
	}

	// Create tenant.
	displayName := tenantName
	if displayName == "" {
		displayName = tenantID
	}
	now := time.Now().UTC()
	slug := strings.ToLower(strings.ReplaceAll(tenantID, "_", "-"))
	_, err := db.ExecContext(ctx,
		`INSERT INTO tenants (tenant_id, slug, name, plan, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		tenantID, slug, displayName, "default", now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}

	// Generate API key.
	keyID, fullKey, prefix, keyHash, err := auth.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate API key: %w", err)
	}

	apiKeyStore := postgres.NewAPIKeyStore(db)
	rec := postgres.APIKeyRecord{
		KeyID:     keyID,
		TenantID:  tenantID,
		KeyHash:   keyHash,
		KeyPrefix: prefix,
		Name:      keyName,
		Role:      "owner",
		CreatedBy: "bootstrap",
		CreatedAt: now,
	}
	if err := apiKeyStore.Create(ctx, rec); err != nil {
		return nil, fmt.Errorf("create API key: %w", err)
	}

	return &bootstrapResult{
		TenantID: tenantID,
		KeyID:    keyID,
		APIKey:   fullKey,
	}, nil
}
