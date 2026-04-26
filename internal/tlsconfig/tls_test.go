package tlsconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFromEnv_NilWhenBothEmpty(t *testing.T) {
	t.Setenv("AGENTOS_TLS_CERT", "")
	t.Setenv("AGENTOS_TLS_KEY", "")

	cfg, certPath, keyPath, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil config when both env vars empty")
	}
	if certPath != "" || keyPath != "" {
		t.Fatal("expected empty paths")
	}
}

func TestLoadFromEnv_ErrorWhenOnlyCertSet(t *testing.T) {
	t.Setenv("AGENTOS_TLS_CERT", "/some/cert.pem")
	t.Setenv("AGENTOS_TLS_KEY", "")

	_, _, _, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when only cert is set")
	}
}

func TestLoadFromEnv_ErrorWhenOnlyKeySet(t *testing.T) {
	t.Setenv("AGENTOS_TLS_CERT", "")
	t.Setenv("AGENTOS_TLS_KEY", "/some/key.pem")

	_, _, _, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when only key is set")
	}
}

func TestLoadFromEnv_SuccessWithValidKeypair(t *testing.T) {
	certPath, keyPath := generateSelfSigned(t)

	t.Setenv("AGENTOS_TLS_CERT", certPath)
	t.Setenv("AGENTOS_TLS_KEY", keyPath)

	cfg, cp, kp, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion TLS 1.2, got %d", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(cfg.Certificates))
	}
	if cp != certPath {
		t.Fatalf("cert path mismatch: %q vs %q", cp, certPath)
	}
	if kp != keyPath {
		t.Fatalf("key path mismatch: %q vs %q", kp, keyPath)
	}
}

func TestLoadFromEnv_ErrorWithInvalidKeypair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if err := os.WriteFile(certPath, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENTOS_TLS_CERT", certPath)
	t.Setenv("AGENTOS_TLS_KEY", keyPath)

	_, _, _, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error with invalid keypair")
	}
}

// generateSelfSigned creates a temporary self-signed cert and key for testing.
func generateSelfSigned(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	return certPath, keyPath
}
