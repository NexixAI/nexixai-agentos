package types

import (
	"errors"
	"fmt"
	"regexp"
)

// ChatMessage represents a single message in conversation memory.
type ChatMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  []byte `json:"tool_calls,omitempty"` // raw JSON
	Name       string `json:"name,omitempty"`
}

var (
	// ErrKeyTooLong signals that a KV key exceeds the maximum allowed length.
	ErrKeyTooLong = errors.New("key exceeds maximum length of 256 characters")
	// ErrInvalidKey signals that a KV key contains disallowed characters.
	ErrInvalidKey = errors.New("key must contain only alphanumeric characters, hyphens, underscores, and dots")
	// ErrValueTooLarge signals that a KV value exceeds the maximum allowed size.
	ErrValueTooLarge = errors.New("value exceeds maximum size")
	// ErrMaxKeysExceeded signals that the agent has reached the maximum number of KV keys.
	ErrMaxKeysExceeded = errors.New("maximum number of keys per agent exceeded")

	validKeyRe = regexp.MustCompile(`^[a-zA-Z0-9\-_.]+$`)
)

// ValidateKey checks that a key conforms to format and length rules.
func ValidateKey(key string) error {
	if len(key) == 0 {
		return ErrInvalidKey
	}
	if len(key) > 256 {
		return ErrKeyTooLong
	}
	if !validKeyRe.MatchString(key) {
		return ErrInvalidKey
	}
	return nil
}

// ValidateValueSize checks that a value does not exceed maxSize bytes.
func ValidateValueSize(value string, maxSize int) error {
	if len(value) > maxSize {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrValueTooLarge, len(value), maxSize)
	}
	return nil
}
