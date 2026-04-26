package id

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// New returns a short, URL-safe-ish id with a prefix, e.g. run_XXXX.
func New(prefix string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	enc = strings.ToLower(enc)
	if prefix == "" {
		return enc
	}
	return prefix + "_" + enc
}
