package auth

import (
	"strings"
	"testing"
)

func TestGenerateKey_Format(t *testing.T) {
	t.Parallel()
	keyID, fullKey, prefix, keyHash, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if !strings.HasPrefix(keyID, "key_") {
		t.Errorf("keyID should start with key_, got %q", keyID)
	}
	if !strings.HasPrefix(fullKey, "aos_live_") {
		t.Errorf("fullKey should start with aos_live_, got %q", fullKey)
	}
	// Full key body should be 32 hex chars.
	body := fullKey[len("aos_live_"):]
	if len(body) != 32 {
		t.Errorf("expected 32 char body, got %d", len(body))
	}
	if len(prefix) != 8 {
		t.Errorf("expected 8 char prefix, got %d: %q", len(prefix), prefix)
	}
	if prefix != body[:8] {
		t.Errorf("prefix %q should match first 8 chars of body %q", prefix, body[:8])
	}
	if keyHash == "" {
		t.Error("keyHash should not be empty")
	}
}

func TestGenerateKey_Unique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		_, fullKey, _, _, err := GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if seen[fullKey] {
			t.Fatalf("duplicate key generated: %s", fullKey)
		}
		seen[fullKey] = true
	}
}

func TestValidateKey_Correct(t *testing.T) {
	t.Parallel()
	_, fullKey, _, keyHash, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !ValidateKey(fullKey, keyHash) {
		t.Error("expected ValidateKey to return true for correct key")
	}
}

func TestValidateKey_WrongKey(t *testing.T) {
	t.Parallel()
	_, _, _, keyHash, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if ValidateKey("<AGENTOS_KEY>", keyHash) {
		t.Error("expected ValidateKey to return false for wrong key")
	}
}

func TestValidateKey_InvalidHash(t *testing.T) {
	t.Parallel()
	if ValidateKey("<AGENTOS_KEY>", "not-a-bcrypt-hash") {
		t.Error("expected ValidateKey to return false for invalid hash")
	}
}

func TestMaskKey(t *testing.T) {
	t.Parallel()
	masked := MaskKey("<AGENTOS_KEY>")
	if masked != "aos_live_****7890" {
		t.Errorf("expected aos_live_****7890, got %q", masked)
	}
}

func TestMaskKey_ShortBody(t *testing.T) {
	t.Parallel()
	masked := MaskKey("<AGENTOS_KEY>")
	if masked != "aos_live_****" {
		t.Errorf("expected aos_live_****, got %q", masked)
	}
}

func TestMaskKey_NotAPIKey(t *testing.T) {
	t.Parallel()
	masked := MaskKey("some-random-token")
	if masked != "****" {
		t.Errorf("expected ****, got %q", masked)
	}
}

func TestIsAPIKey(t *testing.T) {
	t.Parallel()
	if !IsAPIKey("<AGENTOS_KEY>") {
		t.Error("expected true for aos_live_ prefix")
	}
	if IsAPIKey("sk_test_abc123") {
		t.Error("expected false for non-API key")
	}
	if IsAPIKey("") {
		t.Error("expected false for empty string")
	}
}

func TestExtractPrefix(t *testing.T) {
	t.Parallel()
	p := ExtractPrefix("<AGENTOS_KEY>")
	if p != "abcdef12" {
		t.Errorf("expected abcdef12, got %q", p)
	}
	// Too short.
	if ExtractPrefix("<AGENTOS_KEY>") != "" {
		t.Error("expected empty for short key")
	}
	// Not an API key.
	if ExtractPrefix("bearer_token") != "" {
		t.Error("expected empty for non-API key")
	}
}
