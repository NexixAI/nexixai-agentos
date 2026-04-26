package validate

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTenantID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
		code    string
	}{
		{name: "valid tnt_demo", id: "tnt_demo", wantErr: false},
		{name: "valid with underscores and digits", id: "tnt_org_42_prod", wantErr: false},
		{name: "empty string", id: "", wantErr: true, code: "invalid_tenant_id"},
		{name: "no tnt_ prefix", id: "bad", wantErr: true, code: "invalid_tenant_id"},
		{name: "too long suffix (61 chars)", id: "tnt_" + strings.Repeat("a", 61), wantErr: true, code: "invalid_tenant_id"},
		{name: "max length suffix (60 chars)", id: "tnt_" + strings.Repeat("a", 60), wantErr: false},
		{name: "has dash (invalid char)", id: "tnt_my-org", wantErr: true, code: "invalid_tenant_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TenantID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				ve, ok := err.(*ValidationError)
				if !ok {
					t.Fatalf("expected *ValidationError, got %T", err)
				}
				if ve.Code != tt.code {
					t.Fatalf("expected code %q, got %q", tt.code, ve.Code)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestAgentID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
		code    string
	}{
		{name: "valid my-agent-1", id: "my-agent-1", wantErr: false},
		{name: "valid underscores", id: "agent_v2", wantErr: false},
		{name: "empty string", id: "", wantErr: true, code: "invalid_agent_id"},
		{name: "129 chars", id: strings.Repeat("a", 129), wantErr: true, code: "invalid_agent_id"},
		{name: "128 chars", id: strings.Repeat("a", 128), wantErr: false},
		{name: "invalid char space", id: "my agent", wantErr: true, code: "invalid_agent_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AgentID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				ve, ok := err.(*ValidationError)
				if !ok {
					t.Fatalf("expected *ValidationError, got %T", err)
				}
				if ve.Code != tt.code {
					t.Fatalf("expected code %q, got %q", tt.code, ve.Code)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestRunID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
		code    string
	}{
		{name: "valid UUID", id: "550e8400-e29b-41d4-a716-446655440000", wantErr: false},
		{name: "valid UUID uppercase", id: "550E8400-E29B-41D4-A716-446655440000", wantErr: false},
		{name: "valid run_ prefix", id: "run_abc123", wantErr: false},
		{name: "valid run_ with dashes", id: "run_my-run-1", wantErr: false},
		{name: "garbage", id: "garbage", wantErr: true, code: "invalid_run_id"},
		{name: "empty string", id: "", wantErr: true, code: "invalid_run_id"},
		{name: "bad UUID missing segment", id: "550e8400-e29b-41d4-a716", wantErr: true, code: "invalid_run_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				ve, ok := err.(*ValidationError)
				if !ok {
					t.Fatalf("expected *ValidationError, got %T", err)
				}
				if ve.Code != tt.code {
					t.Fatalf("expected code %q, got %q", tt.code, ve.Code)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestRequestBody(t *testing.T) {
	t.Run("small body within limit", func(t *testing.T) {
		body := strings.NewReader("hello")
		req := &http.Request{Body: io.NopCloser(body)}
		err := RequestBody(req, 1024)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("oversized body exceeds limit", func(t *testing.T) {
		body := strings.NewReader(strings.Repeat("x", 2048))
		req := &http.Request{Body: io.NopCloser(body)}
		err := RequestBody(req, 100)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		ve, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T", err)
		}
		if ve.Code != "request_too_large" {
			t.Fatalf("expected code %q, got %q", "request_too_large", ve.Code)
		}
	})

	t.Run("empty body within limit", func(t *testing.T) {
		body := strings.NewReader("")
		req := &http.Request{Body: io.NopCloser(body)}
		err := RequestBody(req, 1024)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidationErrorJSON(t *testing.T) {
	ve := &ValidationError{Code: "invalid_tenant_id", Message: "bad id"}
	data, err := json.Marshal(ve)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if envelope.Error.Code != "invalid_tenant_id" {
		t.Fatalf("expected code %q, got %q", "invalid_tenant_id", envelope.Error.Code)
	}
	if envelope.Error.Message != "bad id" {
		t.Fatalf("expected message %q, got %q", "bad id", envelope.Error.Message)
	}
}

func TestValidationErrorString(t *testing.T) {
	ve := &ValidationError{Code: "invalid_agent_id", Message: "too long"}
	got := ve.Error()
	want := "invalid_agent_id: too long"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
