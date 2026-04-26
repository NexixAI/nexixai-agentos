package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func genKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	enc, err := NewEncryptor(genKey(t), "")
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("hello world")
	aad := []byte("tenant-1")

	ct, err := enc.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(ct, plaintext) {
		t.Error("ciphertext should differ from plaintext")
	}

	pt, err := enc.Decrypt(ct, aad)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(pt, plaintext) {
		t.Errorf("roundtrip failed: got %q, want %q", pt, plaintext)
	}
}

func TestKeyRotation(t *testing.T) {
	keyA := genKey(t)
	keyB := genKey(t)

	// Encrypt with key A.
	encA, err := NewEncryptor(keyA, "")
	if err != nil {
		t.Fatal(err)
	}

	ct, err := encA.Encrypt([]byte("secret"), []byte("tenant-1"))
	if err != nil {
		t.Fatal(err)
	}

	// Decrypt with key B as current, key A as previous — should succeed.
	encB, err := NewEncryptor(keyB, keyA)
	if err != nil {
		t.Fatal(err)
	}

	pt, err := encB.Decrypt(ct, []byte("tenant-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "secret" {
		t.Errorf("key rotation decrypt failed: got %q", pt)
	}
}

func TestWrongKeyRejection(t *testing.T) {
	encA, err := NewEncryptor(genKey(t), "")
	if err != nil {
		t.Fatal(err)
	}
	encB, err := NewEncryptor(genKey(t), "")
	if err != nil {
		t.Fatal(err)
	}

	ct, err := encA.Encrypt([]byte("data"), []byte("tenant-1"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = encB.Decrypt(ct, []byte("tenant-1"))
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

func TestAADMismatch(t *testing.T) {
	enc, err := NewEncryptor(genKey(t), "")
	if err != nil {
		t.Fatal(err)
	}

	ct, err := enc.Encrypt([]byte("data"), []byte("tenant-A"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = enc.Decrypt(ct, []byte("tenant-B"))
	if err == nil {
		t.Error("expected error when AAD mismatches")
	}
}

func TestNilEncryptor_Noop(t *testing.T) {
	var enc *Encryptor // nil

	plaintext := []byte("hello")

	ct, err := enc.Encrypt(plaintext, []byte("tenant"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ct, plaintext) {
		t.Error("nil encryptor Encrypt should return plaintext unchanged")
	}

	pt, err := enc.Decrypt(ct, []byte("tenant"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Error("nil encryptor Decrypt should return ciphertext unchanged")
	}
}

func TestNewEncryptor_BothEmpty(t *testing.T) {
	enc, err := NewEncryptor("", "")
	if err != nil {
		t.Fatal(err)
	}
	if enc != nil {
		t.Error("expected nil encryptor when both keys empty")
	}
}

func TestNewEncryptor_InvalidKeyLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("tooshort"))
	_, err := NewEncryptor(short, "")
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

func TestNewEncryptor_InvalidBase64(t *testing.T) {
	_, err := NewEncryptor("not-valid-base64!!!", "")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestNewEncryptor_PreviousKeyWithoutCurrent(t *testing.T) {
	_, err := NewEncryptor("", genKey(t))
	if err == nil {
		t.Error("expected error when previous key set without current")
	}
}
