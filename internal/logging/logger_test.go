package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// uuidRe matches a UUID v4 string (8-4-4-4-12 hex digits).
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestInitLogger_JSON(t *testing.T) {
	var buf bytes.Buffer
	l := initLogger("json", &buf)
	l.Info("hello")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}
	for _, key := range []string{"time", "level", "msg"} {
		if _, ok := m[key]; !ok {
			t.Errorf("expected key %q in JSON output", key)
		}
	}
}

func TestInitLogger_Text(t *testing.T) {
	var buf bytes.Buffer
	l := initLogger("text", &buf)
	l.Info("hello")

	out := buf.String()
	if !strings.Contains(out, "msg=hello") {
		t.Errorf("text output missing msg field: %s", out)
	}
}

func TestNewRequestID_Format(t *testing.T) {
	id := NewRequestID()
	if !uuidRe.MatchString(id) {
		t.Errorf("NewRequestID() = %q, want UUID v4 format", id)
	}
}

func TestNewRequestID_Unique(t *testing.T) {
	a := NewRequestID()
	b := NewRequestID()
	if a == b {
		t.Errorf("two calls to NewRequestID returned the same value: %s", a)
	}
}

func TestMiddleware_InjectsRequestID(t *testing.T) {
	var buf bytes.Buffer
	initLogger("json", &buf)

	var captured string
	handler := RequestContextMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !uuidRe.MatchString(captured) {
		t.Errorf("request_id from context = %q, want UUID v4", captured)
	}
}

func TestMiddleware_ExtractsTenantID(t *testing.T) {
	var buf bytes.Buffer
	initLogger("json", &buf)

	const wantTenant = "tenant-42"
	var captured string
	handler := RequestContextMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = TenantID(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Tenant-ID", wantTenant)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured != wantTenant {
		t.Errorf("tenant_id = %q, want %q", captured, wantTenant)
	}
}

func TestMiddleware_LogsRequestStartAndCompletion(t *testing.T) {
	var buf bytes.Buffer
	initLogger("json", &buf)

	handler := RequestContextMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/items", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 log lines, got %d: %s", len(lines), buf.String())
	}

	// Verify "request started" line.
	var start map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &start); err != nil {
		t.Fatalf("line 0 invalid JSON: %v", err)
	}
	if start["msg"] != "request started" {
		t.Errorf("first log msg = %v, want %q", start["msg"], "request started")
	}

	// Verify "request completed" line.
	var end map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &end); err != nil {
		t.Fatalf("line 1 invalid JSON: %v", err)
	}
	if end["msg"] != "request completed" {
		t.Errorf("second log msg = %v, want %q", end["msg"], "request completed")
	}
	if end["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want %v", end["status"], http.StatusCreated)
	}
}

func TestFromContext_IncludesRequestAndTenantID(t *testing.T) {
	var buf bytes.Buffer
	initLogger("json", &buf)

	ctx := context.Background()
	ctx = WithRequestID(ctx, "rid-123")
	ctx = WithTenantID(ctx, "tid-456")

	l := FromContext(ctx)
	l.Info("test")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, buf.String())
	}
	if m["request_id"] != "rid-123" {
		t.Errorf("request_id = %v, want %q", m["request_id"], "rid-123")
	}
	if m["tenant_id"] != "tid-456" {
		t.Errorf("tenant_id = %v, want %q", m["tenant_id"], "tid-456")
	}
}
