package multimodal

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Content (union type)
// ---------------------------------------------------------------------------

func TestContent_UnmarshalStringForm(t *testing.T) {
	var c Content
	if err := json.Unmarshal([]byte(`"hello world"`), &c); err != nil {
		t.Fatalf("unmarshal string form: %v", err)
	}
	if c.IsMultipart() {
		t.Errorf("string form should not be multipart")
	}
	if c.AsText() != "hello world" {
		t.Errorf("AsText=%q, want %q", c.AsText(), "hello world")
	}
	if c.HasImage() {
		t.Errorf("string form should not report HasImage")
	}
}

func TestContent_UnmarshalMultipartTextAndImage(t *testing.T) {
	in := `[{"type":"text","text":"what is this?"},{"type":"image_url","image_url":{"url":"https://example.com/pic.jpg","detail":"auto"}}]`
	var c Content
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal multipart: %v", err)
	}
	if !c.IsMultipart() {
		t.Errorf("should be multipart")
	}
	if c.AsText() != "what is this?" {
		t.Errorf("AsText=%q, want %q", c.AsText(), "what is this?")
	}
	if !c.HasImage() {
		t.Errorf("should report HasImage")
	}
	parts := c.Parts()
	if len(parts) != 2 {
		t.Fatalf("len(Parts)=%d, want 2", len(parts))
	}
	if parts[1].ImageURL == nil || parts[1].ImageURL.URL != "https://example.com/pic.jpg" {
		t.Errorf("image URL not preserved: %+v", parts[1].ImageURL)
	}
}

func TestContent_UnmarshalInvalid(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"number", `42`},
		{"bool", `true`},
		{"null-object", `{"type":"text"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Content
			if err := json.Unmarshal([]byte(tc.in), &c); err == nil {
				t.Errorf("expected error for %q, got nil", tc.in)
			}
		})
	}
}

func TestContent_MarshalRoundTripString(t *testing.T) {
	orig := NewText("hello")
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"hello"` {
		t.Errorf("marshaled=%q, want %q", string(data), `"hello"`)
	}
	var round Content
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if round.IsMultipart() || round.AsText() != "hello" {
		t.Errorf("round trip altered content")
	}
}

func TestContent_MarshalRoundTripMultipart(t *testing.T) {
	orig := NewMultipart(
		ContentPart{Type: "text", Text: "describe"},
		ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "https://x/y.png"}},
	)
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round Content
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if !round.IsMultipart() || !round.HasImage() || round.AsText() != "describe" {
		t.Errorf("round trip altered multipart")
	}
}

func TestContent_AsTextElidesNonTextParts(t *testing.T) {
	c := NewMultipart(
		ContentPart{Type: "text", Text: "a"},
		ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "https://x/y"}},
		ContentPart{Type: "text", Text: "b"},
	)
	if got := c.AsText(); got != "a b" {
		t.Errorf("AsText=%q, want %q", got, "a b")
	}
}

// ---------------------------------------------------------------------------
// ValidateContent
// ---------------------------------------------------------------------------

func TestValidateContent_StringFormAccepted(t *testing.T) {
	if err := ValidateContent(NewText("hi")); err != nil {
		t.Errorf("string form should be accepted, got %v", err)
	}
}

func TestValidateContent_HTTPS_and_HTTP_Accepted(t *testing.T) {
	for _, url := range []string{"https://example.com/x.png", "http://grafana.internal/panel.png"} {
		c := NewMultipart(ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: url}})
		if err := ValidateContent(c); err != nil {
			t.Errorf("%s should be accepted, got %v", url, err)
		}
	}
}

func TestValidateContent_RejectedSchemes(t *testing.T) {
	cases := []struct {
		name, url, wantSub string
	}{
		{"file", "file:///etc/passwd", "file://"},
		{"data", "data:image/png;base64,AAA", "base64 data URLs"},
		{"ftp", "ftp://example.com/x.png", "scheme not supported"},
		{"empty", "", "non-empty url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewMultipart(ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: tc.url}})
			err := ValidateContent(c)
			if err == nil {
				t.Fatalf("expected error for %q", tc.url)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error=%q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestValidateContent_NilImageURL(t *testing.T) {
	c := NewMultipart(ContentPart{Type: "image_url", ImageURL: nil})
	if err := ValidateContent(c); err == nil {
		t.Errorf("nil ImageURL should be rejected")
	}
}

func TestValidateContent_UnknownPartType(t *testing.T) {
	c := NewMultipart(ContentPart{Type: "audio", Text: "beep"})
	err := ValidateContent(c)
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("expected unsupported type error, got %v", err)
	}
}

func TestValidateContent_MissingTypeField(t *testing.T) {
	c := NewMultipart(ContentPart{Type: "", Text: "x"})
	if err := ValidateContent(c); err == nil {
		t.Errorf("empty type should be rejected")
	}
}

func TestValidateContent_CapEnforcement(t *testing.T) {
	var tooMany []ContentPart
	for i := 0; i < MaxImageParts+1; i++ {
		tooMany = append(tooMany, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "https://x/y.png"}})
	}
	if err := ValidateContent(NewMultipart(tooMany...)); err == nil || !strings.Contains(err.Error(), "image part cap") {
		t.Errorf("expected cap error, got %v", err)
	}
	if err := ValidateContent(NewMultipart(tooMany[:MaxImageParts]...)); err != nil {
		t.Errorf("exactly %d image parts should be accepted, got %v", MaxImageParts, err)
	}
}

// ---------------------------------------------------------------------------
// ImageRefForAudit
// ---------------------------------------------------------------------------

func TestImageRefForAudit(t *testing.T) {
	url := "https://example.com/pic.jpg"
	if got := ImageRefForAudit(url, false); got != url {
		t.Errorf("verbatim mode should return URL, got %q", got)
	}
	h := ImageRefForAudit(url, true)
	if !strings.HasPrefix(h, "sha256:") {
		t.Errorf("hash mode should prefix sha256:, got %q", h)
	}
	// deterministic
	if h2 := ImageRefForAudit(url, true); h2 != h {
		t.Errorf("hash not deterministic: %q vs %q", h, h2)
	}
	// hash should not leak URL content
	if strings.Contains(h, "example.com") || strings.Contains(h, "pic.jpg") {
		t.Errorf("hash should not contain plaintext URL: %q", h)
	}
}
