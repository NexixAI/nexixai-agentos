package agentorchestrator

import (
	"sync"
	"testing"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

func TestEventSink_EmitAndEvents(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	sink.Emit("step.start", map[string]any{"n": 1})
	sink.Emit("step.end", map[string]any{"n": 2})

	evts := sink.Events()
	if len(evts) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evts))
	}
	if evts[0].Event.Type != "step.start" {
		t.Fatalf("expected type step.start, got %s", evts[0].Event.Type)
	}
	if evts[1].Event.Sequence != 2 {
		t.Fatalf("expected sequence 2, got %d", evts[1].Event.Sequence)
	}
}

func TestEventSink_EventsFromSequence(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	sink.Emit("a", nil)
	sink.Emit("b", nil)
	sink.Emit("c", nil)

	evts := sink.EventsFromSequence(1)
	if len(evts) != 2 {
		t.Fatalf("expected 2 events after seq 1, got %d", len(evts))
	}
	if evts[0].Event.Sequence != 2 {
		t.Fatalf("expected first event seq 2, got %d", evts[0].Event.Sequence)
	}
}

func TestEventSink_Subscribe_ReceivesEvents(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	ch := sink.Subscribe()

	sink.Emit("test.event", map[string]any{"hello": "world"})

	select {
	case env := <-ch:
		if env.Event.Type != "test.event" {
			t.Fatalf("expected type test.event, got %s", env.Event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event on subscriber channel")
	}

	sink.Unsubscribe(ch)
}

func TestEventSink_Unsubscribe_ClosesChannel(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	ch := sink.Subscribe()
	sink.Unsubscribe(ch)

	// Channel should be closed.
	_, open := <-ch
	if open {
		t.Fatal("expected channel to be closed after Unsubscribe")
	}
}

func TestEventSink_Done_ClosesAllSubscribers(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	ch1 := sink.Subscribe()
	ch2 := sink.Subscribe()

	sink.Done()

	// Both channels should be closed.
	if _, open := <-ch1; open {
		t.Fatal("expected ch1 closed after Done")
	}
	if _, open := <-ch2; open {
		t.Fatal("expected ch2 closed after Done")
	}

	// Done is idempotent.
	sink.Done()
}

func TestEventSink_Done_IdempotentNoDoublePanic(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	_ = sink.Subscribe()
	sink.Done()
	// Calling Done again should not panic.
	sink.Done()
}

func TestEventSink_Subscribe_BoundedBuffer_DropsEvents(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	ch := sink.Subscribe()

	// Fill the buffer (subscriberBufSize = 64).
	for i := 0; i < subscriberBufSize+10; i++ {
		sink.Emit("flood", nil)
	}

	// Should have exactly subscriberBufSize events buffered.
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
	if count != subscriberBufSize {
		t.Fatalf("expected %d buffered events, got %d", subscriberBufSize, count)
	}

	sink.Unsubscribe(ch)
}

func TestEventSink_ConcurrentEmitSubscribe(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	ch := sink.Subscribe()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			sink.Emit("concurrent", nil)
		}
		sink.Done()
	}()

	received := 0
	for range ch {
		received++
	}
	wg.Wait()

	if received == 0 {
		t.Fatal("expected to receive at least some events")
	}
	if received > 100 {
		t.Fatalf("received more events than emitted: %d", received)
	}
}

func TestEventSink_UnsubscribeUnknownChannel_NoOp(t *testing.T) {
	sink := NewEventSink("t1", "a1", "r1")
	unknownCh := make(chan types.EventEnvelope)
	// Should not panic.
	sink.Unsubscribe(unknownCh)
}
