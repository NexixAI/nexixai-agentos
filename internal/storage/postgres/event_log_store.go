package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/lib/pq"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// maxSubscriptions is the upper bound on concurrent LISTEN/NOTIFY subscribers
// to prevent unbounded memory growth.
const maxSubscriptions = 1000

// ErrMaxSubscriptions is returned when Subscribe is called and the subscriber
// map has reached its capacity.
var ErrMaxSubscriptions = errors.New("maximum number of event subscriptions reached")

// EventLogStore is a PostgreSQL-backed implementation of storage.EventLogStore.
type EventLogStore struct {
	db *sql.DB

	mu          sync.Mutex
	subscribers map[string][]chan types.EventEnvelope // key: "tenantID:runID"
	subCount    int                                  // total channels across all keys
}

// NewEventLogStore opens a connection pool and returns a ready-to-use EventLogStore.
func NewEventLogStore(cfg config.PostgresConfig) (*EventLogStore, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	return &EventLogStore{
		db:          db,
		subscribers: make(map[string][]chan types.EventEnvelope),
	}, nil
}

// NewEventLogStoreFromDB wraps an existing *sql.DB as an EventLogStore.
func NewEventLogStoreFromDB(db *sql.DB) *EventLogStore {
	return &EventLogStore{
		db:          db,
		subscribers: make(map[string][]chan types.EventEnvelope),
	}
}

func (s *EventLogStore) Append(ctx context.Context, event types.EventEnvelope) error {
	const q = `
INSERT INTO event_log (tenant_id, agent_id, run_id, event_id, sequence, event_type, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

	payload, err := json.Marshal(event.Event.Payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, q,
		event.Event.TenantID, event.Event.AgentID, event.Event.RunID,
		event.Event.EventID, event.Event.Sequence, event.Event.Type, payload,
	)
	if err != nil {
		return err
	}

	// Notify listeners about the new event. The payload is "tenantID:runID"
	// so that the listener can query only the relevant events.
	notifyPayload := event.Event.TenantID + ":" + event.Event.RunID
	_, err = s.db.ExecContext(ctx, "SELECT pg_notify('agentos_events', $1)", notifyPayload)
	if err != nil {
		slog.Warn("pg_notify failed", "error", err, "run_id", event.Event.RunID)
		// Do not fail the append; the event is already persisted.
	}

	return nil
}

func (s *EventLogStore) QueryFromSequence(ctx context.Context, tenantID, runID string, afterSequence int) ([]types.EventEnvelope, error) {
	const q = `
SELECT event_id, sequence, event_type, tenant_id, run_id, agent_id, payload, created_at
FROM event_log
WHERE tenant_id = $1 AND run_id = $2 AND sequence > $3
ORDER BY sequence ASC`

	rows, err := s.db.QueryContext(ctx, q, tenantID, runID, afterSequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []types.EventEnvelope
	for rows.Next() {
		var e types.Event
		var payloadJSON []byte
		var createdAt string
		if err := rows.Scan(
			&e.EventID, &e.Sequence, &e.Type,
			&e.TenantID, &e.RunID, &e.AgentID,
			&payloadJSON, &createdAt,
		); err != nil {
			return nil, err
		}
		e.Time = createdAt
		if len(payloadJSON) > 0 {
			_ = json.Unmarshal(payloadJSON, &e.Payload) // best-effort payload parse
		}
		events = append(events, types.EventEnvelope{Event: e})
	}
	return events, rows.Err()
}

// Subscribe registers a channel that receives events for the given tenant/run.
// It returns the channel and an unsubscribe function. The caller MUST call the
// unsubscribe function when done to free resources.
// Returns ErrMaxSubscriptions if the subscriber cap is reached.
func (s *EventLogStore) Subscribe(tenantID, runID string) (<-chan types.EventEnvelope, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subCount >= maxSubscriptions {
		return nil, nil, ErrMaxSubscriptions
	}

	ch := make(chan types.EventEnvelope, 64)
	key := tenantID + ":" + runID
	s.subscribers[key] = append(s.subscribers[key], ch)
	s.subCount++

	unsub := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.subscribers[key]
		for i, c := range subs {
			if c == ch {
				s.subscribers[key] = append(subs[:i], subs[i+1:]...)
				s.subCount--
				close(ch)
				break
			}
		}
		if len(s.subscribers[key]) == 0 {
			delete(s.subscribers, key)
		}
	}

	return ch, unsub, nil
}

// fanOut sends events to all subscribers for the given key.
// Non-blocking: if a subscriber's channel is full the event is dropped for that subscriber.
func (s *EventLogStore) fanOut(key string, events []types.EventEnvelope) {
	s.mu.Lock()
	subs := make([]chan types.EventEnvelope, len(s.subscribers[key]))
	copy(subs, s.subscribers[key])
	s.mu.Unlock()

	for _, ch := range subs {
		for _, ev := range events {
			select {
			case ch <- ev:
			default:
				slog.Warn("dropping event for slow subscriber", "key", key)
			}
		}
	}
}

// StartListener runs a LISTEN loop on the agentos_events channel using
// lib/pq's notification support. When a notification is received, it queries
// new events for the run and fans them out to subscribers.
// It blocks until ctx is canceled.
func (s *EventLogStore) StartListener(ctx context.Context) error {
	// Build DSN from the existing connection pool. lib/pq's Listener needs
	// a connection string. We obtain one by querying the pool's DSN via
	// a dedicated connection.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn for listener: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			slog.Warn("listener conn close error", "error", closeErr)
		}
	}()



	connStr, err := extractConnStr(s.db)
	if err != nil {
		return fmt.Errorf("extract connection string for listener: %w", err)
	}

	listener := pq.NewListener(connStr, 0, 0, func(ev pq.ListenerEventType, err error) {
		if err != nil {
			slog.Error("pq listener event", "event", ev, "error", err)
		}
	})
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			slog.Warn("pq listener close error", "error", closeErr)
		}
	}()

	if err := listener.Listen("agentos_events"); err != nil {
		return fmt.Errorf("LISTEN agentos_events: %w", err)
	}

	slog.Info("event log LISTEN started", "channel", "agentos_events")

	// Track last-seen sequence per subscriber key to query only new events.
	// Bounded to maxSubscriptions to prevent unbounded memory growth.
	lastSeq := make(map[string]int)
	lastSeqOrder := make([]string, 0, maxSubscriptions) // insertion order for eviction

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n := <-listener.Notify:
			if n == nil {
				// Reconnection notification.
				continue
			}
			key := n.Extra // "tenantID:runID"
			tenantID, runID := splitSubKey(key)
			if tenantID == "" || runID == "" {
				slog.Warn("malformed notification payload", "payload", key)
				continue
			}

			after := lastSeq[key]
			events, err := s.QueryFromSequence(ctx, tenantID, runID, after)
			if err != nil {
				slog.Error("query events after notify", "error", err, "key", key)
				continue
			}
			if len(events) > 0 {
				if _, exists := lastSeq[key]; !exists {
					// New key — evict oldest if at capacity.
					for len(lastSeqOrder) >= maxSubscriptions {
						oldest := lastSeqOrder[0]
						lastSeqOrder = lastSeqOrder[1:]
						delete(lastSeq, oldest)
					}
					lastSeqOrder = append(lastSeqOrder, key)
				}
				lastSeq[key] = events[len(events)-1].Event.Sequence
				s.fanOut(key, events)
			}
		}
	}
}

// splitSubKey splits "tenantID:runID" into its parts.
func splitSubKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i], key[i+1:]
		}
	}
	return "", ""
}

// extractConnStr attempts to get the connection string from the *sql.DB's
// underlying driver. This works with lib/pq's connector.
func extractConnStr(db *sql.DB) (string, error) {
	// lib/pq registers as "postgres". We can get the DSN by opening a raw
	// connection and inspecting settings. A simpler approach: query for the
	// parameters needed to build a connection string.
	ctx := context.Background()
	var host, port, dbname, user string
	row := db.QueryRowContext(ctx, `
		SELECT inet_server_addr()::text,
		       inet_server_port()::text,
		       current_database(),
		       current_user`)
	if err := row.Scan(&host, &port, &dbname, &user); err != nil {
		return "", fmt.Errorf("query connection params: %w", err)
	}
	// sslmode=disable is a safe default for same-host connections.
	// In production, the caller should pass the full DSN.
	return fmt.Sprintf("host=%s port=%s dbname=%s user=%s sslmode=disable", host, port, dbname, user), nil
}

func (s *EventLogStore) Close() error {
	return s.db.Close()
}
