package storageerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrorIdentity(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrAgentExists", ErrAgentExists},
		{"ErrAgentNotFound", ErrAgentNotFound},
		{"ErrInvalidAgent", ErrInvalidAgent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("sentinel error is nil")
			}
			// Wrapping must preserve identity.
			wrapped := fmt.Errorf("outer: %w", tc.err)
			if !errors.Is(wrapped, tc.err) {
				t.Errorf("errors.Is failed for wrapped %v", tc.err)
			}
		})
	}
}
