package tlsconfig

import (
	"crypto/tls"
	"fmt"
	"os"
)

// LoadFromEnv reads TLS configuration from environment variables.
// Returns nil if neither AGENTOS_TLS_CERT nor AGENTOS_TLS_KEY is set.
// Returns error if only one is set.
func LoadFromEnv() (*tls.Config, string, string, error) {
	certPath := os.Getenv("AGENTOS_TLS_CERT")
	keyPath := os.Getenv("AGENTOS_TLS_KEY")

	if certPath == "" && keyPath == "" {
		return nil, "", "", nil
	}
	if certPath == "" || keyPath == "" {
		return nil, "", "", fmt.Errorf("both AGENTOS_TLS_CERT and AGENTOS_TLS_KEY must be set (got cert=%q, key=%q)", certPath, keyPath)
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("load TLS keypair: %w", err)
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	return cfg, certPath, keyPath, nil
}
