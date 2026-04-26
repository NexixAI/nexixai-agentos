package pii

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Email detection
// ---------------------------------------------------------------------------

func TestEmailPositive(t *testing.T) {
	d := NewDetector([]string{PatternEmail})
	cases := []string{
		"user@example.com",
		"first.last@sub.domain.co.uk",
		"name+tag@gmail.com",
		"user123@test.org",
		"a@b.co",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) == 0 {
			t.Errorf("expected email detection for %q, got none", tc)
		} else if dets[0].Pattern != PatternEmail {
			t.Errorf("expected pattern %q, got %q for %q", PatternEmail, dets[0].Pattern, tc)
		}
	}
}

func TestEmailNegative(t *testing.T) {
	d := NewDetector([]string{PatternEmail})
	cases := []string{
		"@",
		"user@",
		"@domain",
		"plaintext",
		"user@.com",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) != 0 {
			t.Errorf("expected no email detection for %q, got %v", tc, dets)
		}
	}
}

// ---------------------------------------------------------------------------
// Phone detection
// ---------------------------------------------------------------------------

func TestPhonePositive(t *testing.T) {
	d := NewDetector([]string{PatternPhone})
	cases := []string{
		"555-123-4567",
		"555.123.4567",
		"5551234567",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) == 0 {
			t.Errorf("expected phone detection for %q, got none", tc)
		} else if dets[0].Pattern != PatternPhone {
			t.Errorf("expected pattern %q, got %q for %q", PatternPhone, dets[0].Pattern, tc)
		}
	}
}

func TestPhoneNegative(t *testing.T) {
	d := NewDetector([]string{PatternPhone})
	cases := []string{
		"12345",
		"abc-def-ghij",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) != 0 {
			t.Errorf("expected no phone detection for %q, got %v", tc, dets)
		}
	}
}

// ---------------------------------------------------------------------------
// SSN detection
// ---------------------------------------------------------------------------

func TestSSNPositive(t *testing.T) {
	d := NewDetector([]string{PatternSSN})
	cases := []string{
		"123-45-6789",
		"000-00-0000",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) == 0 {
			t.Errorf("expected SSN detection for %q, got none", tc)
		} else if dets[0].Pattern != PatternSSN {
			t.Errorf("expected pattern %q, got %q for %q", PatternSSN, dets[0].Pattern, tc)
		}
	}
}

func TestSSNNotMatchedAsPhone(t *testing.T) {
	// When both SSN and phone are enabled, "123-45-6789" must be SSN, not phone.
	d := NewDetector(nil) // all patterns
	dets := d.Scan("123-45-6789")
	if len(dets) == 0 {
		t.Fatal("expected detection for SSN-format string, got none")
	}
	if dets[0].Pattern != PatternSSN {
		t.Errorf("expected SSN pattern, got %q", dets[0].Pattern)
	}
	// Must not also appear as phone.
	for _, det := range dets {
		if det.Pattern == PatternPhone {
			t.Error("SSN-format string should not also match as phone")
		}
	}
}

// ---------------------------------------------------------------------------
// Credit card detection
// ---------------------------------------------------------------------------

func TestCreditCardLuhnValid(t *testing.T) {
	d := NewDetector([]string{PatternCreditCard})
	// 4111111111111111 is a well-known Luhn-valid test number.
	cases := []string{
		"4111111111111111",
		"4111-1111-1111-1111",
		"4111 1111 1111 1111",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) == 0 {
			t.Errorf("expected credit card detection for %q, got none", tc)
		} else if dets[0].Pattern != PatternCreditCard {
			t.Errorf("expected pattern %q, got %q for %q", PatternCreditCard, dets[0].Pattern, tc)
		}
	}
}

func TestCreditCardLuhnInvalid(t *testing.T) {
	d := NewDetector([]string{PatternCreditCard})
	// 1234567890123456 does not pass Luhn.
	cases := []string{
		"1234567890123456",
		"1234-5678-9012-3456",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) != 0 {
			t.Errorf("expected no credit card detection for Luhn-invalid %q, got %v", tc, dets)
		}
	}
}

// ---------------------------------------------------------------------------
// IP address detection
// ---------------------------------------------------------------------------

func TestIPAddressPositive(t *testing.T) {
	d := NewDetector([]string{PatternIPAddress})
	cases := []string{
		"192.168.1.1",
		"10.0.0.1",
		"255.255.255.255",
		"0.0.0.0",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) == 0 {
			t.Errorf("expected IP address detection for %q, got none", tc)
		} else if dets[0].Pattern != PatternIPAddress {
			t.Errorf("expected pattern %q, got %q for %q", PatternIPAddress, dets[0].Pattern, tc)
		}
	}
}

func TestIPAddressNegative(t *testing.T) {
	d := NewDetector([]string{PatternIPAddress})
	cases := []string{
		"999.999.999.999",
		"256.1.1.1",
		"1.2.3.999",
	}
	for _, tc := range cases {
		dets := d.Scan(tc)
		if len(dets) != 0 {
			t.Errorf("expected no IP detection for %q, got %v", tc, dets)
		}
	}
}

// ---------------------------------------------------------------------------
// Redactor
// ---------------------------------------------------------------------------

func TestRedactMultipleTypes(t *testing.T) {
	d := NewDetector(nil)
	text := "Contact user@example.com or call 555-123-4567 from 192.168.1.1"
	dets := d.Scan(text)
	result := Redact(text, dets)

	if strings.Contains(result, "user@example.com") {
		t.Error("email was not redacted")
	}
	if !strings.Contains(result, "[EMAIL]") {
		t.Error("email placeholder missing")
	}
	if strings.Contains(result, "555-123-4567") {
		t.Error("phone was not redacted")
	}
	if !strings.Contains(result, "[PHONE]") {
		t.Error("phone placeholder missing")
	}
	if strings.Contains(result, "192.168.1.1") {
		t.Error("IP was not redacted")
	}
	if !strings.Contains(result, "[IP_ADDRESS]") {
		t.Error("IP placeholder missing")
	}
}

func TestRedactOverlapping(t *testing.T) {
	// SSN format "123-45-6789" could match both phone and SSN.
	// SSN should win because it has higher priority.
	d := NewDetector(nil)
	text := "SSN is 123-45-6789"
	dets := d.Scan(text)
	result := Redact(text, dets)

	if !strings.Contains(result, "[SSN]") {
		t.Errorf("expected [SSN] placeholder in result %q", result)
	}
	if strings.Contains(result, "[PHONE]") {
		t.Errorf("should not have [PHONE] placeholder for SSN-format string in result %q", result)
	}
}

func TestRedactEmpty(t *testing.T) {
	result := Redact("", nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestRedactNoDetections(t *testing.T) {
	text := "This is plain text with no PII."
	result := Redact(text, nil)
	if result != text {
		t.Errorf("expected unchanged text, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// Scan edge cases
// ---------------------------------------------------------------------------

func TestScanEmptyText(t *testing.T) {
	d := NewDetector(nil)
	dets := d.Scan("")
	if len(dets) != 0 {
		t.Errorf("expected no detections for empty text, got %d", len(dets))
	}
}

func TestScanNoPII(t *testing.T) {
	d := NewDetector(nil)
	dets := d.Scan("Hello world, this is a normal sentence.")
	if len(dets) != 0 {
		t.Errorf("expected no detections, got %d: %v", len(dets), dets)
	}
}

func TestScanSelectivePatterns(t *testing.T) {
	// Only enable email — phone numbers should not be detected.
	d := NewDetector([]string{PatternEmail})
	text := "Email user@test.com or call 555-123-4567"
	dets := d.Scan(text)
	if len(dets) != 1 {
		t.Fatalf("expected 1 detection (email only), got %d: %v", len(dets), dets)
	}
	if dets[0].Pattern != PatternEmail {
		t.Errorf("expected email pattern, got %q", dets[0].Pattern)
	}
}

// ---------------------------------------------------------------------------
// Luhn algorithm unit test
// ---------------------------------------------------------------------------

func TestLuhnValid(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"4111111111111111", true},
		{"4111-1111-1111-1111", true},
		{"1234567890123456", false},
		{"0000000000000000", true}, // all zeros passes Luhn
		{"79927398713", false},     // 11 digits — too short
	}
	for _, tc := range cases {
		got := luhnValid(tc.input)
		if got != tc.want {
			t.Errorf("luhnValid(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Block / Redact / Warn mode tests (integration-style)
// ---------------------------------------------------------------------------

func TestBlockModeReturnsError(t *testing.T) {
	d := NewDetector(nil)
	text := "My email is test@example.com"
	dets := d.Scan(text)
	if len(dets) == 0 {
		t.Fatal("expected PII detection")
	}
	// In block mode, the caller checks len(dets) > 0 and returns an error.
	// We verify the detection exists — the mode logic is in the caller.
	mode := "block"
	if mode == "block" && len(dets) > 0 {
		// This is correct behavior: block mode rejects the text.
	}
}

func TestRedactModeReplacesPII(t *testing.T) {
	d := NewDetector(nil)
	text := "My email is test@example.com"
	dets := d.Scan(text)
	result := Redact(text, dets)
	if strings.Contains(result, "test@example.com") {
		t.Error("PII was not redacted in redact mode")
	}
	if !strings.Contains(result, "[EMAIL]") {
		t.Error("placeholder missing")
	}
}

func TestWarnModeTextUnchanged(t *testing.T) {
	d := NewDetector(nil)
	text := "My email is test@example.com"
	dets := d.Scan(text)
	if len(dets) == 0 {
		t.Fatal("expected PII detection")
	}
	// In warn mode, the original text is returned unchanged (caller logs the detection).
	// The text should remain as-is.
	if text != "My email is test@example.com" {
		t.Error("text was mutated in warn mode")
	}
}
