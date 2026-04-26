package quota

import (
	"testing"
)

// These tests verify the PostgresBackend at the type/interface level
// and test construction. Full SQL integration tests require a real
// Postgres instance and belong in //go:build integration tests.

func TestNewPostgresBackend(t *testing.T) {
	t.Parallel()
	pb := NewPostgresBackend(nil)
	if pb == nil {
		t.Fatal("expected non-nil PostgresBackend")
	}
	if pb.db != nil {
		t.Fatal("expected nil db when constructed with nil")
	}
}

func TestPostgresBackend_ImplementsQuotaBackend(t *testing.T) {
	t.Parallel()
	// Compile-time interface check.
	var _ QuotaBackend = (*PostgresBackend)(nil)
}

// TestPostgresBackend_AllowQPS_NilDB verifies that calling AllowQPS with a nil db
// fails closed (returns false) rather than panicking.
func TestPostgresBackend_AllowQPS_NilDB(t *testing.T) {
	t.Parallel()
	pb := NewPostgresBackend(nil)
	// Should not panic, should fail closed.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AllowQPS panicked with nil db: %v", r)
		}
	}()
	if pb.AllowQPS("tenant", 10) {
		t.Fatal("expected AllowQPS to fail closed with nil db")
	}
}

// TestPostgresBackend_TryIncConcurrent_NilDB verifies fail-closed behavior.
func TestPostgresBackend_TryIncConcurrent_NilDB(t *testing.T) {
	t.Parallel()
	pb := NewPostgresBackend(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TryIncConcurrent panicked with nil db: %v", r)
		}
	}()
	if pb.TryIncConcurrent("tenant", 5) {
		t.Fatal("expected TryIncConcurrent to fail closed with nil db")
	}
}

// TestPostgresBackend_DecConcurrent_NilDB verifies no panic on nil db.
func TestPostgresBackend_DecConcurrent_NilDB(t *testing.T) {
	t.Parallel()
	pb := NewPostgresBackend(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecConcurrent panicked with nil db: %v", r)
		}
	}()
	// Should not panic.
	pb.DecConcurrent("tenant")
}

// TestPostgresBackend_WithInitialConcurrent_NilDB verifies no panic on nil db.
func TestPostgresBackend_WithInitialConcurrent_NilDB(t *testing.T) {
	t.Parallel()
	pb := NewPostgresBackend(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WithInitialConcurrent panicked with nil db: %v", r)
		}
	}()
	// Should not panic.
	pb.WithInitialConcurrent(map[string]int{"t1": 3})
}
