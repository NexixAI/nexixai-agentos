package audit

import (
	"net/http"
	"testing"
)

func TestScrubPasswordField(t *testing.T) {
	in := map[string]any{"password": "hunter2"}
	out := Scrub(in)
	if out["password"] != "[REDACTED]" {
		t.Fatalf("expected [REDACTED], got %v", out["password"])
	}
}

func TestScrubAuthorizationField(t *testing.T) {
	in := map[string]any{"Authorization": "Bearer xyz"}
	out := Scrub(in)
	if out["Authorization"] != "[REDACTED]" {
		t.Fatalf("expected [REDACTED], got %v", out["Authorization"])
	}
}

func TestScrubNestedToken(t *testing.T) {
	in := map[string]any{
		"credentials": map[string]any{
			"token": "abc123",
			"user":  "alice",
		},
	}
	out := Scrub(in)
	nested, ok := out["credentials"].(map[string]any)
	if !ok {
		t.Fatal("expected nested map")
	}
	if nested["token"] != "[REDACTED]" {
		t.Fatalf("expected [REDACTED], got %v", nested["token"])
	}
	if nested["user"] != "alice" {
		t.Fatalf("expected alice, got %v", nested["user"])
	}
}

func TestScrubDoesNotMutateOriginal(t *testing.T) {
	in := map[string]any{
		"password": "hunter2",
		"name":     "test",
	}
	_ = Scrub(in)
	if in["password"] != "hunter2" {
		t.Fatal("original map was mutated")
	}
}

func TestScrubHeadersRedactsAuthorization(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token")
	h.Set("Content-Type", "application/json")

	out := ScrubHeaders(h)
	if out.Get("Authorization") != "[REDACTED]" {
		t.Fatalf("expected [REDACTED], got %v", out.Get("Authorization"))
	}
	if out.Get("Content-Type") != "application/json" {
		t.Fatalf("expected application/json, got %v", out.Get("Content-Type"))
	}
}

func TestScrubHeadersDoesNotMutateOriginal(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token")

	_ = ScrubHeaders(h)
	if h.Get("Authorization") != "Bearer secret-token" {
		t.Fatal("original headers were mutated")
	}
}

func TestScrubNonSensitiveFieldsPassThrough(t *testing.T) {
	in := map[string]any{
		"name":   "test",
		"count":  42,
		"active": true,
	}
	out := Scrub(in)
	if out["name"] != "test" {
		t.Fatalf("expected test, got %v", out["name"])
	}
	if out["count"] != 42 {
		t.Fatalf("expected 42, got %v", out["count"])
	}
	if out["active"] != true {
		t.Fatalf("expected true, got %v", out["active"])
	}
}

func TestScrubCaseInsensitive(t *testing.T) {
	cases := []string{"PASSWORD", "Password", "API_KEY", "Api_Key", "SECRET", "Token", "ApiKey"}
	for _, key := range cases {
		in := map[string]any{key: "value"}
		out := Scrub(in)
		if out[key] != "[REDACTED]" {
			t.Errorf("key %q: expected [REDACTED], got %v", key, out[key])
		}
	}
}

func TestScrubNilInputs(t *testing.T) {
	if Scrub(nil) != nil {
		t.Fatal("expected nil for nil map")
	}
	if ScrubHeaders(nil) != nil {
		t.Fatal("expected nil for nil headers")
	}
}

func TestScrubRedactsPIIInStringValues(t *testing.T) {
	in := map[string]any{
		"message": "Contact user@example.com for help",
		"note":    "SSN is 123-45-6789",
		"count":   42,
	}
	out := Scrub(in)

	msg, ok := out["message"].(string)
	if !ok {
		t.Fatal("expected string for message")
	}
	if msg == in["message"] {
		t.Error("expected PII to be redacted in message field")
	}
	if msg != "Contact [EMAIL] for help" {
		t.Errorf("unexpected redacted message: %q", msg)
	}

	note, ok := out["note"].(string)
	if !ok {
		t.Fatal("expected string for note")
	}
	if note != "SSN is [SSN]" {
		t.Errorf("unexpected redacted note: %q", note)
	}

	// Non-string values pass through unchanged.
	if out["count"] != 42 {
		t.Errorf("expected 42, got %v", out["count"])
	}
}
