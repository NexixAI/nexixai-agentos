package federation

import "testing"

func TestAddFromSequence(t *testing.T) {
	out := addFromSequence("http://example.com/v1/runs/123/events", 5)
	if out != "http://example.com/v1/runs/123/events?from_sequence=5" {
		t.Fatalf("unexpected url: %s", out)
	}

	out2 := addFromSequence("http://example.com/v1/runs/123/events?foo=bar", 7)
	if out2 != "http://example.com/v1/runs/123/events?foo=bar&from_sequence=7" {
		t.Fatalf("unexpected url with existing query: %s", out2)
	}

	out3 := addFromSequence("http://example.com/v1/runs/123/events", 0)
	if out3 != "http://example.com/v1/runs/123/events" {
		t.Fatalf("expected unchanged url when from_sequence=0, got %s", out3)
	}
}
