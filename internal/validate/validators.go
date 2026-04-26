// Package validate provides input validation helpers for the AgentOS platform.
package validate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
)

// Precompiled patterns for ID validation.
var (
	tenantIDPattern = regexp.MustCompile(`^tnt_[a-zA-Z0-9_]{1,60}$`)
	agentIDPattern  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	runIDPattern    = regexp.MustCompile(`^run_[a-zA-Z0-9_-]{1,128}$`)
)

// ValidationError represents a structured validation failure.
type ValidationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// MarshalJSON produces the JSON-friendly envelope: {"error":{"code":"...","message":"..."}}.
func (e *ValidationError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{
			Code:    e.Code,
			Message: e.Message,
		},
	})
}

// TenantID validates that id matches the pattern tnt_[a-zA-Z0-9_]{1,60}.
func TenantID(id string) error {
	if !tenantIDPattern.MatchString(id) {
		return &ValidationError{
			Code:    "invalid_tenant_id",
			Message: fmt.Sprintf("tenant id %q must match pattern tnt_[a-zA-Z0-9_]{1,60}", id),
		}
	}
	return nil
}

// AgentID validates that id matches the pattern [a-zA-Z0-9_-]{1,128}.
func AgentID(id string) error {
	if !agentIDPattern.MatchString(id) {
		return &ValidationError{
			Code:    "invalid_agent_id",
			Message: fmt.Sprintf("agent id %q must match pattern [a-zA-Z0-9_-]{1,128}", id),
		}
	}
	return nil
}

// RunID validates that id is either a valid UUID (8-4-4-4-12 hex) or matches run_[a-zA-Z0-9_-]{1,128}.
func RunID(id string) error {
	if uuidPattern.MatchString(id) || runIDPattern.MatchString(id) {
		return nil
	}
	return &ValidationError{
		Code:    "invalid_run_id",
		Message: fmt.Sprintf("run id %q must be a valid UUID or match pattern run_[a-zA-Z0-9_-]{1,128}", id),
	}
}

// RequestBody wraps r.Body with http.MaxBytesReader and attempts a single read
// to detect whether the body exceeds maxBytes. If the body is too large a
// ValidationError with code "request_too_large" is returned.
func RequestBody(r *http.Request, maxBytes int64) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)

	// Attempt to read one byte beyond the limit to trigger the error.
	buf := make([]byte, maxBytes+1)
	n, err := r.Body.Read(buf)
	if err != nil {
		// If the error is a MaxBytesError (or its string representation),
		// the body exceeded the limit.
		if err.Error() == "http: request body too large" {
			return &ValidationError{
				Code:    "request_too_large",
				Message: fmt.Sprintf("request body exceeds %d bytes", maxBytes),
			}
		}
		// io.EOF or other read completion — body fit within the limit.
		_ = n
		return nil
	}
	// Read succeeded without error and we got more data than maxBytes — shouldn't
	// happen with MaxBytesReader, but handle defensively.
	if int64(n) > maxBytes {
		return &ValidationError{
			Code:    "request_too_large",
			Message: fmt.Sprintf("request body exceeds %d bytes", maxBytes),
		}
	}
	return nil
}
