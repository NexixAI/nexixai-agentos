package federation

import (
	"path/filepath"
	"testing"
)

func TestForwardIndexPersistsAndIsTenantScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forward-index.json")

	idx := newForwardIndexPersistent(path)
	idx.Set("tnt_alpha", "run_1", "stk_a", "http://node-a/runs/run_1/events")
	idx.Set("tnt_beta", "run_1", "stk_b", "http://node-b/runs/run_1/events")

	if tgt, ok := idx.Get("tnt_alpha", "run_1"); !ok || tgt.RemoteStackID != "stk_a" {
		t.Fatalf("unexpected lookup for tenant alpha: %+v ok=%v", tgt, ok)
	}
	if _, ok := idx.Get("tnt_alpha", "missing"); ok {
		t.Fatalf("expected miss for unknown run")
	}
	if _, ok := idx.Get("tnt_gamma", "run_1"); ok {
		t.Fatalf("expected tenant isolation on Get")
	}

	// Simulate restart by creating a new index from the same path.
	reloaded := newForwardIndexPersistent(path)
	if tgt, ok := reloaded.Get("tnt_beta", "run_1"); !ok || tgt.RemoteStackID != "stk_b" {
		t.Fatalf("expected persisted record for tenant beta, got %+v ok=%v", tgt, ok)
	}
	if tgt, ok := reloaded.Get("tnt_alpha", "run_1"); !ok || tgt.RemoteEventsURL == "" {
		t.Fatalf("expected persisted tenant alpha record, got %+v ok=%v", tgt, ok)
	}
}
