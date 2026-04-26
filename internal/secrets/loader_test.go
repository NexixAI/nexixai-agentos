package secrets

import (
	"os"
	"strings"
	"testing"
)

func TestLoadPrefersEnv(t *testing.T) {
	t.Setenv("EXAMPLE_SECRET", "env-secret")
	t.Setenv("EXAMPLE_SECRET_FILE", createTempFile(t, "file-secret"))

	logs := []string{}
	l := NewLoader(WithLogger(func(msg string) { logs = append(logs, msg) }))

	v, err := l.Load("EXAMPLE_SECRET")
	if err != nil {
		t.Fatalf("load env: %v", err)
	}
	if v != "env-secret" {
		t.Fatalf("expected env secret, got %q", v)
	}
	assertNoSecretLeak(t, logs, "env-secret")
}

func TestLoadFromFile(t *testing.T) {
	path := createTempFile(t, "file-secret\n")
	t.Setenv("FILE_ONLY_SECRET_FILE", path)

	logs := []string{}
	l := NewLoader(WithLogger(func(msg string) { logs = append(logs, msg) }))

	v, err := l.Load("FILE_ONLY_SECRET")
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if v != "file-secret" {
		t.Fatalf("expected file secret, got %q", v)
	}
	assertNoSecretLeak(t, logs, "file-secret")
}

func TestLoadExternal(t *testing.T) {
	logs := []string{}
	l := NewLoader(
		WithLogger(func(msg string) { logs = append(logs, msg) }),
		WithExternal(func(name string) (string, error) {
			if name == "EXTERNAL_ONLY" {
				return "external-secret", nil
			}
			return "", nil
		}),
	)
	v, err := l.Load("EXTERNAL_ONLY")
	if err != nil {
		t.Fatalf("load external: %v", err)
	}
	if v != "external-secret" {
		t.Fatalf("expected external secret, got %q", v)
	}
	assertNoSecretLeak(t, logs, "external-secret")
}

func TestRequireMissing(t *testing.T) {
	l := NewLoader()
	if _, err := l.Require("MISSING_SECRET"); err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestEmptyNameErrors(t *testing.T) {
	l := NewLoader()
	if _, err := l.Load(""); err == nil {
		t.Fatal("expected error for empty secret name")
	}
}

func createTempFile(t *testing.T, contents string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "secret-*")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.WriteString(contents); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}
	return f.Name()
}

func assertNoSecretLeak(t *testing.T, logs []string, secret string) {
	t.Helper()
	for _, msg := range logs {
		if strings.Contains(msg, secret) {
			t.Fatalf("log contains secret value: %q", msg)
		}
	}
}
