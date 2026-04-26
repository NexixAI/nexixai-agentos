package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	keyPrefix    = "aos_live_"
	keyRandomLen = 32 // hex chars (16 random bytes)
	prefixLen    = 8  // chars after "aos_live_" used as lookup prefix
)

// GenerateKey produces a new API key and returns:
//   - keyID:    a unique identifier (e.g. "key_<hex>")
//   - fullKey:  the complete key "aos_live_<32 hex chars>"
//   - prefix:   the first 8 chars after "aos_live_" (for DB lookup)
//   - keyHash:  bcrypt hash of the full key
func GenerateKey() (keyID, fullKey, prefix, keyHash string, err error) {
	// Generate random bytes for the key body.
	raw := make([]byte, keyRandomLen/2)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", "", fmt.Errorf("generate api key: %w", err)
	}
	body := hex.EncodeToString(raw)
	fullKey = keyPrefix + body
	prefix = body[:prefixLen]

	// Generate key ID.
	idRaw := make([]byte, 8)
	if _, err := rand.Read(idRaw); err != nil {
		return "", "", "", "", fmt.Errorf("generate key id: %w", err)
	}
	keyID = "key_" + hex.EncodeToString(idRaw)

	// Hash with bcrypt (default cost).
	hash, err := bcrypt.GenerateFromPassword([]byte(fullKey), bcrypt.DefaultCost)
	if err != nil {
		return "", "", "", "", fmt.Errorf("hash api key: %w", err)
	}
	keyHash = string(hash)

	return keyID, fullKey, prefix, keyHash, nil
}

// ValidateKey checks fullKey against a bcrypt storedHash.
// Returns false on any error (fail closed, constant-time via bcrypt).
func ValidateKey(fullKey, storedHash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(fullKey))
	return err == nil
}

// MaskKey returns a masked version of the key: "aos_live_****...{last4}".
func MaskKey(fullKey string) string {
	if !strings.HasPrefix(fullKey, keyPrefix) {
		return "****"
	}
	body := fullKey[len(keyPrefix):]
	if len(body) < 4 {
		return keyPrefix + "****"
	}
	return keyPrefix + "****" + body[len(body)-4:]
}

// IsAPIKey returns true if the token looks like an AgentOS API key.
func IsAPIKey(token string) bool {
	return strings.HasPrefix(token, keyPrefix)
}

// ExtractPrefix returns the lookup prefix from a full API key.
// Returns empty string if the key format is invalid.
func ExtractPrefix(fullKey string) string {
	if !strings.HasPrefix(fullKey, keyPrefix) {
		return ""
	}
	body := fullKey[len(keyPrefix):]
	if len(body) < prefixLen {
		return ""
	}
	return body[:prefixLen]
}
