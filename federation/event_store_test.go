package federation

import "testing"

func TestEventStoreListFromSequence(t *testing.T) {
	store := newEventStore()

	env1 := map[string]any{"event": map[string]any{"event_id": "e1", "run_id": "r1", "sequence": 1}}
	env2 := map[string]any{"event": map[string]any{"event_id": "e2", "run_id": "r1", "sequence": 2}}
	env3 := map[string]any{"event": map[string]any{"event_id": "e3", "run_id": "r1"}}

	accepted, rejected := store.Ingest("t1", "r1", []map[string]any{env1, env2, env3})
	if accepted != 3 || rejected != 0 {
		t.Fatalf("unexpected ingest counts accepted=%d rejected=%d", accepted, rejected)
	}

	envs, ok := store.ListFromSequence("t1", "r1", 1)
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if len(envs) != 2 {
		t.Fatalf("expected 2 events after cursor, got %d", len(envs))
	}
	gotIDs := []string{
		envs[0]["event"].(map[string]any)["event_id"].(string),
		envs[1]["event"].(map[string]any)["event_id"].(string),
	}
	if gotIDs[0] != "e2" || gotIDs[1] != "e3" {
		t.Fatalf("unexpected event order: %v", gotIDs)
	}
}

func TestEventStoreRejectsNonMonotonicSequence(t *testing.T) {
	store := newEventStore()

	env1 := map[string]any{"event": map[string]any{"event_id": "e1", "run_id": "r1", "sequence": 2}}
	env2 := map[string]any{"event": map[string]any{"event_id": "e2", "run_id": "r1", "sequence": 2}}

	accepted, rejected := store.Ingest("t1", "r1", []map[string]any{env1, env2})
	if accepted != 1 || rejected != 1 {
		t.Fatalf("expected monotonic guard to reject duplicate sequence, got accepted=%d rejected=%d", accepted, rejected)
	}
}
