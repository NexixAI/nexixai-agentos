package compliance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexixAI/nexixai-agentos/internal/types"
)

// mockEventLoader implements EventLoader for testing.
type mockEventLoader struct {
	events []types.EventEnvelope
	err    error
}

func (m *mockEventLoader) QueryFromSequence(_ context.Context, _, _ string, _ int) ([]types.EventEnvelope, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.events, nil
}

func TestPIIScanHandler_Detection(t *testing.T) {
	loader := &mockEventLoader{
		events: []types.EventEnvelope{
			{Event: types.Event{Payload: map[string]any{"content": "No PII here"}}},
			{Event: types.Event{Payload: map[string]any{"content": "Hello world"}}},
			{Event: types.Event{Payload: map[string]any{
				"content": "Contact user@example.com for details",
			}}},
			{Event: types.Event{Payload: map[string]any{
				"content": "Call 555-123-4567",
			}}},
		},
	}

	h := NewPIIScanHandler(loader)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/pii-scan?run_id=run_123", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var locations []PIILocation
	if err := json.NewDecoder(w.Body).Decode(&locations); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(locations) < 2 {
		t.Fatalf("expected at least 2 PII locations, got %d", len(locations))
	}

	// Verify the detections point to the correct events.
	foundEmail := false
	foundPhone := false
	for _, loc := range locations {
		if loc.PIIType == "email" && loc.EventIndex == 2 {
			foundEmail = true
		}
		if loc.PIIType == "phone" && loc.EventIndex == 3 {
			foundPhone = true
		}
	}
	if !foundEmail {
		t.Error("expected email detection at event_index 2")
	}
	if !foundPhone {
		t.Error("expected phone detection at event_index 3")
	}

	// Verify that actual PII values are NOT in the response.
	body := w.Body.String()
	if containsSubstring(body, "user@example.com") {
		t.Error("response should not contain actual PII value (email)")
	}
	if containsSubstring(body, "555-123-4567") {
		t.Error("response should not contain actual PII value (phone)")
	}
}

func TestPIIScanHandler_NoPIIFound(t *testing.T) {
	loader := &mockEventLoader{
		events: []types.EventEnvelope{
			{Event: types.Event{Payload: map[string]any{"content": "Hello world"}}},
			{Event: types.Event{Payload: map[string]any{"content": "No sensitive data"}}},
		},
	}

	h := NewPIIScanHandler(loader)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/pii-scan?run_id=run_clean", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var locations []PIILocation
	if err := json.NewDecoder(w.Body).Decode(&locations); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(locations) != 0 {
		t.Errorf("expected 0 PII locations, got %d", len(locations))
	}
}

func TestPIIScanHandler_MissingRunID(t *testing.T) {
	loader := &mockEventLoader{}
	h := NewPIIScanHandler(loader)

	r := httptest.NewRequest(http.MethodGet, "/v1/admin/pii-scan", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPIIScanHandler_MethodNotAllowed(t *testing.T) {
	loader := &mockEventLoader{}
	h := NewPIIScanHandler(loader)

	r := httptest.NewRequest(http.MethodPost, "/v1/admin/pii-scan?run_id=run_123", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
