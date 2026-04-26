package governance

import (
	"context"
	"sync"
	"testing"
)

func TestKillSwitch_FreezeAndIsFrozen(t *testing.T) {
	ks := NewKillSwitch()
	ks.Freeze("sre-agent", "misbehaving", "admin")

	frozen, reason := ks.IsFrozen("sre-agent")
	if !frozen {
		t.Fatal("expected agent to be frozen")
	}
	if reason != "misbehaving" {
		t.Errorf("expected reason %q, got %q", "misbehaving", reason)
	}
}

func TestKillSwitch_UnfreezeClears(t *testing.T) {
	ks := NewKillSwitch()
	ks.Freeze("sre-agent", "misbehaving", "admin")
	ks.Unfreeze("sre-agent")

	frozen, reason := ks.IsFrozen("sre-agent")
	if frozen {
		t.Fatalf("expected agent to be unfrozen, reason=%q", reason)
	}
	if reason != "" {
		t.Errorf("expected empty reason after unfreeze, got %q", reason)
	}
}

func TestKillSwitch_UnfreezeNoop(t *testing.T) {
	ks := NewKillSwitch()
	// Unfreezing an agent that was never frozen should not panic.
	ks.Unfreeze("nonexistent")

	frozen, _ := ks.IsFrozen("nonexistent")
	if frozen {
		t.Fatal("nonexistent agent should not be frozen")
	}
}

func TestKillSwitch_PreventiveFreeze(t *testing.T) {
	// Freezing an agent that doesn't exist in governance.yml is allowed.
	ks := NewKillSwitch()
	ks.Freeze("future-agent", "preemptive lock", "ops")

	frozen, reason := ks.IsFrozen("future-agent")
	if !frozen {
		t.Fatal("expected preventive freeze to work")
	}
	if reason != "preemptive lock" {
		t.Errorf("expected reason %q, got %q", "preemptive lock", reason)
	}
}

func TestKillSwitch_ListFrozen(t *testing.T) {
	ks := NewKillSwitch()
	ks.Freeze("agent-a", "reason-a", "admin")
	ks.Freeze("agent-b", "reason-b", "admin")
	ks.Freeze("agent-c", "reason-c", "ops")

	list := ks.ListFrozen()
	if len(list) != 3 {
		t.Fatalf("expected 3 frozen agents, got %d", len(list))
	}

	byID := make(map[string]FreezeRecord, len(list))
	for _, rec := range list {
		byID[rec.AgentID] = rec
	}

	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("expected %q in frozen list", id)
		}
	}

	// Unfreeze one and verify the list shrinks.
	ks.Unfreeze("agent-b")
	list = ks.ListFrozen()
	if len(list) != 2 {
		t.Fatalf("expected 2 frozen agents after unfreeze, got %d", len(list))
	}
}

func TestKillSwitch_DoubleFreezeUpdatesReason(t *testing.T) {
	ks := NewKillSwitch()
	ks.Freeze("sre-agent", "first reason", "admin")
	ks.Freeze("sre-agent", "updated reason", "ops")

	frozen, reason := ks.IsFrozen("sre-agent")
	if !frozen {
		t.Fatal("expected agent to be frozen")
	}
	if reason != "updated reason" {
		t.Errorf("expected reason %q after double freeze, got %q", "updated reason", reason)
	}

	list := ks.ListFrozen()
	if len(list) != 1 {
		t.Fatalf("expected 1 frozen agent after double freeze, got %d", len(list))
	}
	if list[0].FrozenBy != "ops" {
		t.Errorf("expected frozenBy %q, got %q", "ops", list[0].FrozenBy)
	}
}

func TestKillSwitch_ConcurrentAccess(t *testing.T) {
	ks := NewKillSwitch()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 4)

	for i := 0; i < goroutines; i++ {
		// Concurrent freezes.
		go func() {
			defer wg.Done()
			ks.Freeze("agent-concurrent", "stress test", "bot")
		}()
		// Concurrent IsFrozen reads.
		go func() {
			defer wg.Done()
			ks.IsFrozen("agent-concurrent")
		}()
		// Concurrent ListFrozen reads.
		go func() {
			defer wg.Done()
			ks.ListFrozen()
		}()
		// Concurrent unfreezes.
		go func() {
			defer wg.Done()
			ks.Unfreeze("agent-concurrent")
		}()
	}

	wg.Wait()
	// No panic or race detector failure means pass.
}

// --- Engine integration: frozen agent is denied before other checks ---

func TestEngine_KillSwitch_DeniesBeforeOtherDomains(t *testing.T) {
	cfg := testConfig()
	e := NewEngineFromConfig(cfg)

	// Freeze a known agent via the engine's kill switch.
	e.KillSwitch().Freeze("sre-agent", "incident response", "admin")

	d, err := e.Check(context.Background(), "sre-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny for frozen agent, got Allow")
	}
	if d.Domain != "killswitch" {
		t.Errorf("expected domain killswitch, got %q", d.Domain)
	}
	if d.Reason != "agent frozen: incident response" {
		t.Errorf("unexpected reason: %s", d.Reason)
	}
}

func TestEngine_KillSwitch_AllowsAfterUnfreeze(t *testing.T) {
	cfg := testConfig()
	e := NewEngineFromConfig(cfg)

	e.KillSwitch().Freeze("sre-agent", "lockdown", "admin")
	e.KillSwitch().Unfreeze("sre-agent")

	d, err := e.Check(context.Background(), "sre-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow after unfreeze, got %v: %s", d.Verdict, d.Reason)
	}
}

func TestEngine_KillSwitch_UnknownAgentStillDeniedByIdentity(t *testing.T) {
	cfg := testConfig()
	e := NewEngineFromConfig(cfg)

	// An unfrozen unknown agent should still be denied by identity.
	d, err := e.Check(context.Background(), "unknown-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatal("expected Deny for unknown agent")
	}
	if d.Domain != "identity" {
		t.Errorf("expected domain identity, got %q", d.Domain)
	}
}

func TestEngine_KillSwitch_FrozenUnknownAgent(t *testing.T) {
	cfg := testConfig()
	e := NewEngineFromConfig(cfg)

	// Preventively freeze an agent not in governance.yml.
	e.KillSwitch().Freeze("unknown-agent", "preemptive", "admin")

	d, err := e.Check(context.Background(), "unknown-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatal("expected Deny for frozen unknown agent")
	}
	// Kill switch fires first, before identity check.
	if d.Domain != "killswitch" {
		t.Errorf("expected domain killswitch, got %q", d.Domain)
	}
}
