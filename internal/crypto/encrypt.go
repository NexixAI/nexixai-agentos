package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	keyLen   = 32 // AES-256
	nonceLen = 12 // GCM standard nonce size
)

// Encryptor provides AES-256-GCM encryption with optional key rotation support.
// A nil *Encryptor is valid and acts as a no-op (plaintext mode).
type Encryptor struct {
	current  cipher.AEAD
	previous cipher.AEAD
}

// NewEncryptor creates an Encryptor from base64-encoded keys.
// If both keys are empty, it returns nil (plaintext mode).
// Keys must decode to exactly 32 bytes (AES-256).
func NewEncryptor(keyBase64, prevKeyBase64 string) (*Encryptor, error) {
	if keyBase64 == "" && prevKeyBase64 == "" {
		return nil, nil
	}
	if keyBase64 == "" {
		return nil, errors.New("crypto: current key is required when previous key is set")
	}

	current, err := aeadFromBase64(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("crypto: current key: %w", err)
	}

	enc := &Encryptor{current: current}

	if prevKeyBase64 != "" {
		prev, err := aeadFromBase64(prevKeyBase64)
		if err != nil {
			return nil, fmt.Errorf("crypto: previous key: %w", err)
		}
		enc.previous = prev
	}

	return enc, nil
}

// Encrypt encrypts plaintext with AES-256-GCM using additional authenticated data.
// Output format: nonce (12 bytes) || ciphertext || tag.
// A nil Encryptor returns plaintext unchanged.
func (e *Encryptor) Encrypt(plaintext []byte, aad []byte) ([]byte, error) {
	if e == nil {
		return plaintext, nil
	}

	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: failed to generate nonce: %w", err)
	}

	ciphertext := e.current.Seal(nonce, nonce, plaintext, aad)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext produced by Encrypt. It tries the current key first;
// if that fails and a previous key exists, it tries the previous key.
// A nil Encryptor returns ciphertext unchanged.
func (e *Encryptor) Decrypt(ciphertext []byte, aad []byte) ([]byte, error) {
	if e == nil {
		return ciphertext, nil
	}

	if len(ciphertext) < nonceLen {
		return nil, errors.New("crypto: ciphertext too short")
	}

	nonce := ciphertext[:nonceLen]
	data := ciphertext[nonceLen:]

	plaintext, err := e.current.Open(nil, nonce, data, aad)
	if err == nil {
		return plaintext, nil
	}

	if e.previous != nil {
		plaintext, prevErr := e.previous.Open(nil, nonce, data, aad)
		if prevErr == nil {
			return plaintext, nil
		}
	}

	return nil, fmt.Errorf("crypto: decryption failed: %w", err)
}

func aeadFromBase64(encoded string) (cipher.AEAD, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	if len(raw) != keyLen {
		return nil, fmt.Errorf("key must be %d bytes, got %d", keyLen, len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm, nil
}
