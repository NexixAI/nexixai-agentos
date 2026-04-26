package governance

import (
	"sync"
	"testing"
)

// registryTestConfig returns a GovernanceConfig with known roles for registry tests.
func registryTestConfig() *GovernanceConfig {
	return &GovernanceConfig{
		Version: "1.0",
		Agents: map[string]AgentPolicy{
			"sre-agent": {
				Role:      "operator",
				Clearance: "execute",
			},
			"executor": {
				Role:      "implementor",
				Clearance: "execute",
			},
			"delegator-bot": {
				Role:      "developer",
				Clearance: "execute",
			},
		},
	}
}

func TestRegister_KnownRole(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	if err := reg.Register("agent-1", "operator"); err != nil {
		t.Fatalf("Register with known role should succeed, got error: %v", err)
	}
}

func TestRegister_UnknownRole(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	err := reg.Register("agent-1", "superadmin")
	if err == nil {
		t.Fatal("Register with unknown role should return error, got nil")
	}
}

func TestGetRole_RegisteredAgent(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	if err := reg.Register("agent-1", "operator"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	role, ok := reg.GetRole("agent-1")
	if !ok {
		t.Fatal("GetRole should return true for registered agent")
	}
	if role != "operator" {
		t.Errorf("expected role %q, got %q", "operator", role)
	}
}

func TestGetRole_UnknownAgent(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	role, ok := reg.GetRole("nonexistent")
	if ok {
		t.Fatal("GetRole should return false for unknown agent")
	}
	if role != "" {
		t.Errorf("expected empty role for unknown agent, got %q", role)
	}
}

func TestRecordAction_IncrementsCount(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	if err := reg.Register("agent-1", "operator"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	reg.RecordAction("agent-1")
	reg.RecordAction("agent-1")
	reg.RecordAction("agent-1")

	stats := reg.GetStats("agent-1")
	if stats.ActionCount != 3 {
		t.Errorf("expected ActionCount 3, got %d", stats.ActionCount)
	}
	if stats.LastActionAt.IsZero() {
		t.Error("expected LastActionAt to be set after RecordAction")
	}
}

func TestRecordAction_UnknownAgentIsNoop(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	// Should not panic or produce side effects.
	reg.RecordAction("nonexistent")

	stats := reg.GetStats("nonexistent")
	if stats.ActionCount != 0 {
		t.Errorf("expected zero ActionCount for unknown agent, got %d", stats.ActionCount)
	}
}

func TestGetStats_RegisteredAgent(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	if err := reg.Register("agent-1", "implementor"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	stats := reg.GetStats("agent-1")
	if stats.Role != "implementor" {
		t.Errorf("expected role %q, got %q", "implementor", stats.Role)
	}
	if stats.ConnectedAt.IsZero() {
		t.Error("expected ConnectedAt to be set after Register")
	}
	if stats.ActionCount != 0 {
		t.Errorf("expected zero ActionCount before any actions, got %d", stats.ActionCount)
	}
}

func TestGetStats_UnknownAgent(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	stats := reg.GetStats("nonexistent")
	if stats.Role != "" {
		t.Errorf("expected empty role for unknown agent, got %q", stats.Role)
	}
	if stats.ActionCount != 0 {
		t.Errorf("expected zero ActionCount, got %d", stats.ActionCount)
	}
}

func TestRegister_ReplacesExisting(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	if err := reg.Register("agent-1", "operator"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	reg.RecordAction("agent-1")

	// Re-register with a different role — should replace.
	if err := reg.Register("agent-1", "developer"); err != nil {
		t.Fatalf("Re-register failed: %v", err)
	}

	role, ok := reg.GetRole("agent-1")
	if !ok {
		t.Fatal("GetRole should return true after re-register")
	}
	if role != "developer" {
		t.Errorf("expected role %q after re-register, got %q", "developer", role)
	}

	stats := reg.GetStats("agent-1")
	if stats.ActionCount != 0 {
		t.Errorf("expected ActionCount reset to 0 after re-register, got %d", stats.ActionCount)
	}
}

func TestConcurrentAccess(t *testing.T) {
	reg := NewAgentRegistry(registryTestConfig())

	// Pre-register an agent so RecordAction and GetRole have something to work with.
	if err := reg.Register("agent-concurrent", "operator"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	var wg sync.WaitGroup
	const goroutines = 50
	const actionsPerGoroutine = 100

	// Spawn goroutines that register, record actions, and read concurrently.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < actionsPerGoroutine; j++ {
				reg.RecordAction("agent-concurrent")
				reg.GetRole("agent-concurrent")
				reg.GetStats("agent-concurrent")
			}
		}()
	}

	// Also spawn goroutines doing registrations concurrently.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = reg.Register("agent-concurrent", "operator")
			_ = reg.Register("agent-temp", "developer")
			reg.GetRole("agent-temp")
		}()
	}

	wg.Wait()

	// If we get here without a race detector failure, the test passes.
	// Verify the agent is still accessible.
	_, ok := reg.GetRole("agent-concurrent")
	if !ok {
		t.Error("expected agent-concurrent to still be registered after concurrent access")
	}
}
