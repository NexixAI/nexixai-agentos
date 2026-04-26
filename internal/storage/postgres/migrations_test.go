package postgres

import (
	"testing"
)

func TestRegisteredMigrationsNonEmpty(t *testing.T) {
	if len(registeredMigrations) == 0 {
		t.Fatal("registeredMigrations must not be empty")
	}
}

func TestRegisteredMigrationsSequential(t *testing.T) {
	for i, m := range registeredMigrations {
		expectedVersion := i + 1
		if m.Version != expectedVersion {
			t.Errorf("migration at index %d: version = %d, want %d", i, m.Version, expectedVersion)
		}
		if m.Description == "" {
			t.Errorf("migration %d has empty description", m.Version)
		}
		if m.SQL == "" {
			t.Errorf("migration %d has empty SQL", m.Version)
		}
	}
}

func TestMigrationChecksumDeterministic(t *testing.T) {
	for _, m := range registeredMigrations {
		cs1 := migrationChecksum(m.SQL)
		cs2 := migrationChecksum(m.SQL)
		if cs1 != cs2 {
			t.Errorf("migration %d: checksum not deterministic: %s != %s", m.Version, cs1, cs2)
		}
		if len(cs1) != 64 {
			t.Errorf("migration %d: checksum length = %d, want 64 (SHA-256 hex)", m.Version, len(cs1))
		}
	}
}

func TestMigrationChecksumDistinct(t *testing.T) {
	if len(registeredMigrations) < 2 {
		t.Skip("need at least 2 migrations to test distinct checksums")
	}
	cs1 := migrationChecksum(registeredMigrations[0].SQL)
	cs2 := migrationChecksum(registeredMigrations[1].SQL)
	if cs1 == cs2 {
		t.Error("migrations 1 and 2 have identical checksums but different SQL")
	}
}

func TestDryRunMode(t *testing.T) {
	// Set the env var, call Migrate with a nil db.
	// In dry-run mode, Migrate should return nil without touching the DB.
	t.Setenv("AGENTOS_MIGRATION_DRY_RUN", "true")

	err := Migrate(nil)
	if err != nil {
		t.Fatalf("Migrate in dry-run mode returned error: %v", err)
	}
}

func TestDryRunNotTriggeredByDefault(t *testing.T) {
	// Ensure dry-run is NOT triggered when the env var is unset or "false".
	// We can't call Migrate with a nil db outside dry-run (nil deref on sql.DB),
	// so we verify the env var gating logic indirectly: dry-run with "false"
	// should NOT return nil with a nil db — it should panic/crash, proving the
	// code path diverges.
	t.Setenv("AGENTOS_MIGRATION_DRY_RUN", "false")

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when calling Migrate with nil db outside dry-run mode")
		}
	}()

	_ = Migrate(nil) //nolint:errcheck // intentional: testing panic path
}

func TestBootstrapDDLNotEmpty(t *testing.T) {
	if bootstrapDDL == "" {
		t.Fatal("bootstrapDDL must not be empty")
	}
}
