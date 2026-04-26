package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSinkPersistsAuditEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "agent-orchestrator.log")
	t.Setenv("AGENTOS_AUDIT_SINK", "file:"+path)
	t.Setenv("AGENTOS_SERVICE", "agent-orchestrator")

	sink := NewFromEnv()
	t.Cleanup(func() {
		_ = sink.Close()
	})

	sink.Log(Entry{
		TenantID: "tnt_demo", Action: "runs.create", Resource: "run/test", Outcome: "allowed",
	})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("audit file missing: %v", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		t.Fatalf("expected audit log entry, file was empty")
	}
}
