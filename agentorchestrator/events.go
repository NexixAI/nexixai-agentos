package agentorchestrator

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/id"
	"github.com/NexixAI/nexixai-agentos/internal/storage"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// subscriber channels — bounded buffer of 64 events per subscriber.
const subscriberBufSize = 64

// EventSink collects events for a single run. It is safe for concurrent use.
// When a durable EventLogStore is configured, events are persisted as they are emitted.
type EventSink struct {
	mu          sync.Mutex
	events      []types.EventEnvelope
	seq         int
	runID       string
	tenant      string
	agent       string
	eventLog    storage.EventLogStore // optional durable store
	subscribers []chan types.EventEnvelope
	done        bool // true after Done() is called

	droppedEvents atomic.Int64 // counter for dropped events (gap detection, M-1)
}

// NewEventSink creates a new event sink for the given run.
func NewEventSink(tenantID, agentID, runID string) *EventSink {
	return &EventSink{
		runID:  runID,
		tenant: tenantID,
		agent:  agentID,
	}
}

// NewEventSinkWithLog creates a new event sink backed by a durable event log.
func NewEventSinkWithLog(tenantID, agentID, runID string, eventLog storage.EventLogStore) *EventSink {
	return &EventSink{
		runID:    runID,
		tenant:   tenantID,
		agent:    agentID,
		eventLog: eventLog,
	}
}

// Emit records an event with the given type and payload.
func (s *EventSink) Emit(eventType string, payload map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	env := types.EventEnvelope{
		Event: types.Event{
			EventID:  id.New("evt"),
			Sequence: s.seq,
			Time:     time.Now().UTC().Format(time.RFC3339),
			Type:     eventType,
			TenantID: s.tenant,
			AgentID:  s.agent,
			RunID:    s.runID,
			Trace:    types.TraceContext{Traceparent: "00-00000000000000000000000000000000-0000000000000000-01"},
			Payload:  payload,
		},
	}
	s.events = append(s.events, env)
	s.notifySubscribers(env)

	// Persist to durable log if available. Use a bounded context to avoid
	// blocking the emitter indefinitely (v9.0 M-8).
	if s.eventLog != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.eventLog.Append(ctx, env); err != nil {
			slog.Error("failed to persist event", "event_id", env.Event.EventID, "run_id", s.runID, "error", err)
		}
		cancel()
	}
}

// Events returns a copy of all recorded events.
func (s *EventSink) Events() []types.EventEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.EventEnvelope, len(s.events))
	copy(out, s.events)
	return out
}

// EventsFromSequence returns events with sequence > afterSequence.
func (s *EventSink) EventsFromSequence(afterSequence int) []types.EventEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.EventEnvelope
	for _, e := range s.events {
		if e.Event.Sequence > afterSequence {
			out = append(out, e)
		}
	}
	return out
}

// Subscribe returns a channel that receives events as they are emitted.
// Caller must call Unsubscribe when done.
func (s *EventSink) Subscribe() <-chan types.EventEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan types.EventEnvelope, subscriberBufSize)
	s.subscribers = append(s.subscribers, ch)
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (s *EventSink) Unsubscribe(ch <-chan types.EventEnvelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subscribers {
		if sub == ch {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			close(sub)
			return
		}
	}
}

// notifySubscribers sends the event to all subscriber channels (non-blocking).
func (s *EventSink) notifySubscribers(env types.EventEnvelope) {
	for _, ch := range s.subscribers {
		select {
		case ch <- env:
		default:
			dropped := s.droppedEvents.Add(1)
			slog.Warn("subscriber channel full, dropping event",
				"event_id", env.Event.EventID,
				"run_id", s.runID,
				"total_dropped", dropped,
			)
		}
	}
}

// DroppedEvents returns the total number of events dropped due to full subscriber buffers.
func (s *EventSink) DroppedEvents() int64 {
	return s.droppedEvents.Load()
}

// Done closes all subscriber channels. Called when the run completes.
func (s *EventSink) Done() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	s.done = true
	for _, ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = nil
}
