package audit

import (
	"net/http"
	"strings"

	"github.com/NexixAI/nexixai-agentos/internal/pii"
)

const redacted = "[REDACTED]"

// defaultPIIDetector is created once for use in audit scrubbing.
var defaultPIIDetector = pii.NewDetector(nil)

// sensitiveKeys lists field names that must be redacted (compared case-insensitively).
var sensitiveKeys = map[string]struct{}{
	"password":      {},
	"secret":        {},
	"token":         {},
	"api_key":       {},
	"apikey":        {},
	"authorization": {},
}

// isSensitive returns true if the key matches a sensitive field name
// (case-insensitive).
func isSensitive(key string) bool {
	_, ok := sensitiveKeys[strings.ToLower(key)]
	return ok
}

// Scrub returns a deep copy of detail with sensitive fields replaced by
// "[REDACTED]". The original map is never mutated.
func Scrub(detail map[string]any) map[string]any {
	if detail == nil {
		return nil
	}
	out := make(map[string]any, len(detail))
	for k, v := range detail {
		if isSensitive(k) {
			out[k] = redacted
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = Scrub(nested)
			continue
		}
		// Scan string values for PII and redact.
		if s, ok := v.(string); ok {
			dets := defaultPIIDetector.Scan(s)
			if len(dets) > 0 {
				out[k] = pii.Redact(s, dets)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// ScrubHeaders returns a copy of headers with the Authorization header value
// replaced by "[REDACTED]". The original headers are never mutated.
func ScrubHeaders(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}
	out := make(http.Header, len(headers))
	for k, vs := range headers {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	if out.Get("Authorization") != "" {
		out.Set("Authorization", redacted)
	}
	return out
}
