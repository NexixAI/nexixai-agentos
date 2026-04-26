package id

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	t.Run("with_prefix", func(t *testing.T) {
		got := New("run")
		if !strings.HasPrefix(got, "run_") {
			t.Errorf("New(%q) = %q, want prefix %q", "run", got, "run_")
		}
	})

	t.Run("empty_prefix", func(t *testing.T) {
		got := New("")
		if got == "" {
			t.Error("New(\"\") returned empty string")
		}
		if strings.Contains(got, "_") {
			t.Errorf("New(\"\") = %q, should not contain underscore", got)
		}
	})

	t.Run("unique_values", func(t *testing.T) {
		a := New("test")
		b := New("test")
		if a == b {
			t.Errorf("two calls to New returned same value: %q", a)
		}
	})
}
