package postgres

import (
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestSplitSubKey(t *testing.T) {
	tests := []struct {
		key            string
		wantTenantID   string
		wantRunID      string
	}{
		{"tenant1:run1", "tenant1", "run1"},
		{"t:r", "t", "r"},
		{"abc:def:ghi", "abc", "def:ghi"},
		{"nocolon", "", ""},
		{"", "", ""},
		{":emptyTenant", "", "emptyTenant"},
	}
	for _, tt := range tests {
		tenantID, runID := splitSubKey(tt.key)
		if tenantID != tt.wantTenantID || runID != tt.wantRunID {
			t.Errorf("splitSubKey(%q) = (%q, %q), want (%q, %q)",
				tt.key, tenantID, runID, tt.wantTenantID, tt.wantRunID)
		}
	}
}

func TestSubscribe_BasicSendReceive(t *testing.T) {
	store := &EventLogStore{
		subscribers: make(map[string][]chan types.EventEnvelope),
	}

	ch, unsub, err := store.Subscribe("t1", "r1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	// Fan out an event.
	ev := types.EventEnvelope{
		Event: types.Event{
			EventID:  "e1",
			Sequence: 1,
			TenantID: "t1",
			RunID:    "r1",
			Type:     "test",
		},
	}
	store.fanOut("t1:r1", []types.EventEnvelope{ev})

	select {
	case got := <-ch:
		if got.Event.EventID != "e1" {
			t.Errorf("got event_id=%q, want e1", got.Event.EventID)
		}
	default:
		t.Fatal("expected event on channel, got none")
	}
}

func TestSubscribe_Unsubscribe(t *testing.T) {
	store := &EventLogStore{
		subscribers: make(map[string][]chan types.EventEnvelope),
	}

	_, unsub, err := store.Subscribe("t1", "r1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if store.subCount != 1 {
		t.Fatalf("subCount = %d, want 1", store.subCount)
	}

	unsub()

	if store.subCount != 0 {
		t.Fatalf("subCount after unsub = %d, want 0", store.subCount)
	}

	store.mu.Lock()
	if len(store.subscribers) != 0 {
		t.Errorf("subscribers map not cleaned up: %d entries", len(store.subscribers))
	}
	store.mu.Unlock()
}

func TestSubscribe_MaxSubscriptions(t *testing.T) {
	store := &EventLogStore{
		subscribers: make(map[string][]chan types.EventEnvelope),
	}

	// Fill up to the max.
	var unsubs []func()
	for i := 0; i < maxSubscriptions; i++ {
		_, unsub, err := store.Subscribe("t1", "r1")
		if err != nil {
			t.Fatalf("Subscribe %d: %v", i, err)
		}
		unsubs = append(unsubs, unsub)
	}

	// Next one should fail.
	_, _, err := store.Subscribe("t1", "r2")
	if err != ErrMaxSubscriptions {
		t.Fatalf("expected ErrMaxSubscriptions, got %v", err)
	}

	// Unsubscribe one, then it should succeed again.
	unsubs[0]()
	ch, unsub, err := store.Subscribe("t1", "r2")
	if err != nil {
		t.Fatalf("Subscribe after unsub: %v", err)
	}
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}
	unsub()

	// Clean up remaining.
	for _, u := range unsubs[1:] {
		u()
	}
}

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	store := &EventLogStore{
		subscribers: make(map[string][]chan types.EventEnvelope),
	}

	ch1, unsub1, err := store.Subscribe("t1", "r1")
	if err != nil {
		t.Fatalf("Subscribe 1: %v", err)
	}
	defer unsub1()

	ch2, unsub2, err := store.Subscribe("t1", "r1")
	if err != nil {
		t.Fatalf("Subscribe 2: %v", err)
	}
	defer unsub2()

	ev := types.EventEnvelope{
		Event: types.Event{EventID: "e1", TenantID: "t1", RunID: "r1"},
	}
	store.fanOut("t1:r1", []types.EventEnvelope{ev})

	// Both should receive.
	for i, ch := range []<-chan types.EventEnvelope{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Event.EventID != "e1" {
				t.Errorf("subscriber %d: got event_id=%q, want e1", i, got.Event.EventID)
			}
		default:
			t.Errorf("subscriber %d: expected event, got none", i)
		}
	}
}

func TestFanOut_NoSubscribers(t *testing.T) {
	store := &EventLogStore{
		subscribers: make(map[string][]chan types.EventEnvelope),
	}

	// Should not panic with no subscribers.
	ev := types.EventEnvelope{
		Event: types.Event{EventID: "e1", TenantID: "t1", RunID: "r1"},
	}
	store.fanOut("t1:r1", []types.EventEnvelope{ev})
}

func TestFanOut_SlowSubscriber(t *testing.T) {
	store := &EventLogStore{
		subscribers: make(map[string][]chan types.EventEnvelope),
	}

	ch, unsub, err := store.Subscribe("t1", "r1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	// Fill the channel buffer (size 64).
	for i := 0; i < 64; i++ {
		ev := types.EventEnvelope{
			Event: types.Event{EventID: "fill", Sequence: i, TenantID: "t1", RunID: "r1"},
		}
		store.fanOut("t1:r1", []types.EventEnvelope{ev})
	}

	// Next fanOut should drop without blocking.
	ev := types.EventEnvelope{
		Event: types.Event{EventID: "dropped", Sequence: 65, TenantID: "t1", RunID: "r1"},
	}
	store.fanOut("t1:r1", []types.EventEnvelope{ev})

	// Drain and verify we got the first 64.
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != 64 {
		t.Errorf("drained %d events, want 64", count)
	}
}
