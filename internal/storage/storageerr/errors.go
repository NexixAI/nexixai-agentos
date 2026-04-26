// Package storageerr defines sentinel errors shared by all storage backends.
// It exists as a separate package to break the import cycle between
// internal/storage (which contains the factory) and backend sub-packages
// like internal/storage/postgres.
package storageerr

import "errors"

var (
	// ErrAgentExists signals attempts to create an agent that already exists.
	ErrAgentExists = errors.New("agent already exists")
	// ErrAgentNotFound signals that the requested agent was not found.
	ErrAgentNotFound = errors.New("agent not found")
	// ErrInvalidAgent signals missing required agent identity fields.
	ErrInvalidAgent = errors.New("invalid agent")
)
