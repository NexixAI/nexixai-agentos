// Package multimodal holds shared multipart-content primitives used by both
// the MCP chat_completion tool (v11.0) and the HTTP OpenAI-compat endpoint
// (v11.1). Kept narrowly scoped: types + validation + audit formatting.
// No HTTP, no MCP, no transport specifics.
package multimodal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxImageParts caps how many image_url parts may appear in a single message.
// Keeps upstream request envelopes bounded; default 8 matches typical vision
// model budgets.
const MaxImageParts = 8

// ImageURL carries the URL and optional detail hint for an image content part.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ContentPart is a single content part in an OpenAI-compatible multipart
// message. Supported types: "text", "image_url". Unknown types parse but
// are rejected by ValidateContent.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// Content is a union type for chat message content. It accepts either a
// plain JSON string (backward-compatible text-only form) or a JSON array
// of content parts (OpenAI-compatible multipart form).
//
// Callers read text via AsText(); multipart presence via IsMultipart();
// image parts via HasImage(). MarshalJSON emits the exact shape it was
// constructed with so round-tripping preserves the on-the-wire form.
type Content struct {
	text      string
	parts     []ContentPart
	multipart bool
}

// UnmarshalJSON implements the string-or-array union. Tries string first
// (most common, preserves backward compat), falls back to array form for
// multipart.
func (c *Content) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.text = s
		c.parts = nil
		c.multipart = false
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return fmt.Errorf("chat message content must be a string or an array of content parts: %w", err)
	}
	c.parts = parts
	c.multipart = true
	c.text = ""
	return nil
}

// MarshalJSON emits either a string or an array depending on how the
// content was constructed. Preserves round-trip fidelity for both forms.
func (c Content) MarshalJSON() ([]byte, error) {
	if c.multipart {
		return json.Marshal(c.parts)
	}
	return json.Marshal(c.text)
}

// AsText returns a flat text representation of the content. String form
// returns itself. Array form concatenates all "text" parts with single
// spaces; image_url and other non-text parts are elided. Used by the
// classifier (text-only) and any legacy text-only downstream.
func (c Content) AsText() string {
	if !c.multipart {
		return c.text
	}
	var b strings.Builder
	for _, p := range c.parts {
		if p.Type != "text" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

// IsMultipart reports whether the content was provided in array form.
func (c Content) IsMultipart() bool { return c.multipart }

// HasImage reports whether the content contains any image_url parts.
func (c Content) HasImage() bool {
	if !c.multipart {
		return false
	}
	for _, p := range c.parts {
		if p.Type == "image_url" {
			return true
		}
	}
	return false
}

// Parts returns a copy of the multipart content parts. Returns nil for
// string-form content. Callers must not mutate the returned slice.
func (c Content) Parts() []ContentPart {
	if !c.multipart {
		return nil
	}
	out := make([]ContentPart, len(c.parts))
	copy(out, c.parts)
	return out
}

// NewText constructs a Content holding a plain string value. Intended for
// tests and internal callers that build Content literals.
func NewText(s string) Content { return Content{text: s} }

// NewMultipart constructs a Content holding an array of parts.
func NewMultipart(parts ...ContentPart) Content {
	return Content{parts: parts, multipart: true}
}

// ValidateContent checks message content for multipart safety: unknown
// part types, missing required fields, unsupported URL schemes, and the
// per-message image part cap. Returns nil for string-form content
// (backward compatible — legacy string callers are never rejected).
// Image fetches themselves are performed by the upstream model backend,
// not by AgentOS; network-level policy (private-range blocking, etc.)
// remains the backend's responsibility.
func ValidateContent(c Content) error {
	if !c.IsMultipart() {
		return nil
	}
	imageCount := 0
	for i, p := range c.Parts() {
		switch p.Type {
		case "text":
			// text parts have no further validation at this layer
		case "image_url":
			if p.ImageURL == nil || p.ImageURL.URL == "" {
				return fmt.Errorf("content part %d: image_url requires a non-empty url", i)
			}
			u := p.ImageURL.URL
			lower := strings.ToLower(u)
			switch {
			case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
				// accepted
			case strings.HasPrefix(lower, "data:"):
				return fmt.Errorf("content part %d: base64 data URLs are not supported; use a hosted http(s) URL", i)
			case strings.HasPrefix(lower, "file:"):
				return fmt.Errorf("content part %d: file:// URLs are rejected for safety; use a hosted http(s) URL", i)
			default:
				return fmt.Errorf("content part %d: image_url.url scheme not supported; use http:// or https://", i)
			}
			imageCount++
			if imageCount > MaxImageParts {
				return fmt.Errorf("message exceeds per-message image part cap (%d): reduce number of image_url parts", MaxImageParts)
			}
		case "":
			return fmt.Errorf("content part %d: type field is required", i)
		default:
			return fmt.Errorf("content part %d: unsupported type %q (accepted: text, image_url)", i, p.Type)
		}
	}
	return nil
}

// ImageRefForAudit returns the value to record in an audit trail for an
// image URL. When hashMode is false (default), the URL is returned
// verbatim. When true, the SHA-256 hex of the URL is returned — useful
// in deploys where image URLs may contain bearer tokens or
// personally-identifying slugs.
func ImageRefForAudit(url string, hashMode bool) string {
	if !hashMode {
		return url
	}
	sum := sha256.Sum256([]byte(url))
	return "sha256:" + hex.EncodeToString(sum[:])
}
