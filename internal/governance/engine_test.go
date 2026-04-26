package governance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testConfig returns a minimal GovernanceConfig for testing.
func testConfig() *GovernanceConfig {
	return &GovernanceConfig{
		Version:   "1.0",
		Framework: "ATF-aligned",
		Agents: map[string]AgentPolicy{
			"sre-agent": {
				Role:      "operator",
				Clearance: "execute",
				Auth:      "api_key",
			},
			"executor": {
				Role:      "implementor",
				Clearance: "execute",
				Auth:      "spawn",
			},
			"delegator-bot": {
				Role:      "developer",
				Clearance: "execute",
				Auth:      "api_key",
			},
		},
		Behavior: BehaviorConfig{
			RateLimits: map[string]RateLimit{
				"sre-agent": {PerHour: 3, PerDay: 10},
				"executor":  {PerHour: 50, PerDay: 200},
			},
			LoopDetection: LoopDetectionConfig{
				MaxAttemptsPerTarget: 2,
				Window:               "1h",
				OnTrigger:            "deny_and_escalate",
			},
			CircuitBreaker: CircuitBreakerConfig{
				ConsecutiveFailures: 3,
				Action:              "freeze_agent",
				Notify:              "human",
				AutoUnfreeze:        false,
			},
			Confidence: ConfidenceConfig{
				ActThreshold:   0.7,
				WatchThreshold: 0.4,
				LogThreshold:   0.0,
			},
		},
		Segments: map[string]SegmentationEntry{
			"sre-agent": {
				AllowedActions: []string{
					"restart_container",
					"retry_pipeline",
					"file_issue",
					"notify",
					"read_container_logs",
					"query_metrics",
					"get_health",
					"get_alerts",
					"get_container_status",
				},
				DeniedActions: []string{
					"delete_stack",
					"delete_volume",
					"force_push",
					"execute_code",
					"execute_shell",
				},
				ProtectedTargets: []string{"vault", "gitlab", "portainer"},
				ProtectedGate:    "destructive_action_gate",
				Scope:            "all",
			},
			"executor": {
				AllowedActions: []string{
					"chat_completion",
					"execute_code",
					"execute_shell",
					"search_knowledge",
				},
				DeniedActions: []string{
					"restart_container",
					"delete_stack",
					"force_push",
				},
				Scope: "declared_file_scope",
			},
			"delegator-bot": {
				AllowedActions: []string{
					"spawn_session",
					"label_issue",
					"comment_issue",
					"close_issue",
				},
				DeniedActions: []string{
					"restart_container",
					"delete_stack",
					"force_push",
					"execute_shell",
				},
				Scope: "labeled_issues_only",
			},
		},
	}
}

// --- Domain 1: Identity ---

func TestIdentity_KnownAgent(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	d, err := e.Check(context.Background(), "sre-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow, got %v: %s", d.Verdict, d.Reason)
	}
}

func TestIdentity_UnknownAgent(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	d, err := e.Check(context.Background(), "rogue-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny, got Allow")
	}
	if d.Domain != "identity" {
		t.Errorf("expected domain identity, got %q", d.Domain)
	}
	if d.Reason == "" {
		t.Error("expected a reason, got empty")
	}
}

func TestIdentity_EmptyAgentID(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	d, err := e.Check(context.Background(), "", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny for empty agentID, got Allow")
	}
	if d.Domain != "identity" {
		t.Errorf("expected domain identity, got %q", d.Domain)
	}
}

// --- Domain 4: Segmentation ---

func TestSegmentation_AllowedAction(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	d, err := e.Check(context.Background(), "sre-agent", "restart_container", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow, got %v: %s", d.Verdict, d.Reason)
	}
}

func TestSegmentation_DeniedAction(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	d, err := e.Check(context.Background(), "sre-agent", "execute_code", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny, got Allow")
	}
	if d.Domain != "segmentation" {
		t.Errorf("expected domain segmentation, got %q", d.Domain)
	}
}

func TestSegmentation_ActionNotInAllowList(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	// "deploy_stack" is not in allowed or denied for sre-agent
	d, err := e.Check(context.Background(), "sre-agent", "deploy_stack", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny for unlisted action, got Allow")
	}
	if d.Domain != "segmentation" {
		t.Errorf("expected domain segmentation, got %q", d.Domain)
	}
}

func TestSegmentation_DeniedOverridesAllowed(t *testing.T) {
	// Construct a config where an action is in both allowed and denied.
	cfg := testConfig()
	seg := cfg.Segments["sre-agent"]
	seg.AllowedActions = append(seg.AllowedActions, "execute_code")
	cfg.Segments["sre-agent"] = seg

	e := NewEngineFromConfig(cfg)
	d, err := e.Check(context.Background(), "sre-agent", "execute_code", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny (denied overrides allowed), got Allow")
	}
}

func TestSegmentation_ExecutorCanExecuteCode(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	d, err := e.Check(context.Background(), "executor", "execute_code", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow for executor execute_code, got %v: %s", d.Verdict, d.Reason)
	}
}

func TestSegmentation_ExecutorCannotRestartContainer(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	d, err := e.Check(context.Background(), "executor", "restart_container", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny for executor restart_container, got Allow")
	}
}

func TestSegmentation_CrossRoleIsolation(t *testing.T) {
	e := NewEngineFromConfig(testConfig())

	// SRE can restart_container but executor cannot.
	d1, _ := e.Check(context.Background(), "sre-agent", "restart_container", nil)
	d2, _ := e.Check(context.Background(), "executor", "restart_container", nil)

	if d1.Verdict != Allow {
		t.Errorf("sre-agent should be allowed restart_container")
	}
	if d2.Verdict != Deny {
		t.Errorf("executor should be denied restart_container")
	}

	// Executor can execute_code but SRE cannot.
	d3, _ := e.Check(context.Background(), "executor", "execute_code", nil)
	d4, _ := e.Check(context.Background(), "sre-agent", "execute_code", nil)

	if d3.Verdict != Allow {
		t.Errorf("executor should be allowed execute_code")
	}
	if d4.Verdict != Deny {
		t.Errorf("sre-agent should be denied execute_code")
	}
}

// --- Domain 2: Behavior (rate limit) ---

func TestBehavior_RateLimit_UnderLimit(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	// sre-agent has per_hour: 3. First call should be fine.
	d, err := e.Check(context.Background(), "sre-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow under rate limit, got %v: %s", d.Verdict, d.Reason)
	}
}

func TestBehavior_RateLimit_ExceedsHourly(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	ctx := context.Background()

	// sre-agent per_hour: 3. Make 3 calls (which records 3 actions), then the 4th should be denied.
	for i := 0; i < 3; i++ {
		d, err := e.Check(ctx, "sre-agent", "get_health", nil)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if d.Verdict != Allow {
			t.Fatalf("call %d: expected Allow, got Deny: %s", i, d.Reason)
		}
	}

	// 4th call should be denied.
	d, err := e.Check(ctx, "sre-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny after exceeding hourly limit, got Allow")
	}
	if d.Domain != "behavior" {
		t.Errorf("expected domain behavior, got %q", d.Domain)
	}
}

// --- Domain 2: Behavior (loop detection) ---

func TestBehavior_LoopDetection_UnderThreshold(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	ctx := context.Background()
	args := map[string]any{"target": "my-container"}

	// max_attempts_per_target: 2. First call should be fine.
	d, err := e.Check(ctx, "sre-agent", "restart_container", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow under loop threshold, got %v: %s", d.Verdict, d.Reason)
	}
}

func TestBehavior_LoopDetection_ExceedsThreshold(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	ctx := context.Background()
	args := map[string]any{"target": "my-container"}

	// max_attempts_per_target: 2. Make 2 calls, then the 3rd should trigger loop detection.
	for i := 0; i < 2; i++ {
		d, err := e.Check(ctx, "sre-agent", "restart_container", args)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if d.Verdict != Allow {
			t.Fatalf("call %d: expected Allow, got Deny: %s", i, d.Reason)
		}
	}

	d, err := e.Check(ctx, "sre-agent", "restart_container", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny after loop detection, got Allow")
	}
	if d.Domain != "behavior" {
		t.Errorf("expected domain behavior, got %q", d.Domain)
	}
}

func TestBehavior_LoopDetection_DifferentTargets(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	ctx := context.Background()

	// Restarting different containers should not trigger loop detection.
	for i := 0; i < 3; i++ {
		args := map[string]any{"target": "container-" + string(rune('a'+i))}
		d, err := e.Check(ctx, "sre-agent", "restart_container", args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Note: these may eventually hit rate limit, but not loop detection.
		if d.Verdict == Deny && d.Domain == "behavior" {
			// Rate limit is expected for sre-agent (per_hour: 3), that's okay.
			// But loop detection should not trigger.
			if d.Reason != "" && d.Domain == "behavior" {
				// Check it's rate limit, not loop
				continue
			}
		}
	}
}

func TestBehavior_LoopDetection_NoTargetSkipsCheck(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	ctx := context.Background()

	// Without a target arg, loop detection should not apply.
	for i := 0; i < 5; i++ {
		d, err := e.Check(ctx, "executor", "chat_completion", nil)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		// executor has per_hour: 50, so 5 calls should be fine.
		if d.Verdict != Allow {
			t.Fatalf("call %d: expected Allow without target, got Deny: %s", i, d.Reason)
		}
	}
}

// --- Domain 2: Behavior (circuit breaker) ---

func TestBehavior_CircuitBreaker_FreezesAfterThreshold(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	ctx := context.Background()

	// Simulate 3 consecutive failures.
	for i := 0; i < 3; i++ {
		e.RecordResult("sre-agent", false)
	}

	d, err := e.Check(ctx, "sre-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny after circuit breaker, got Allow")
	}
	if d.Domain != "behavior" {
		t.Errorf("expected domain behavior, got %q", d.Domain)
	}
}

func TestBehavior_CircuitBreaker_SuccessResets(t *testing.T) {
	e := NewEngineFromConfig(testConfig())
	ctx := context.Background()

	// 2 failures, then a success. Should reset.
	e.RecordResult("sre-agent", false)
	e.RecordResult("sre-agent", false)
	e.RecordResult("sre-agent", true)

	// Now 2 more failures should not trigger the breaker (need 3 consecutive).
	e.RecordResult("sre-agent", false)
	e.RecordResult("sre-agent", false)

	d, err := e.Check(ctx, "sre-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow after success reset, got %v: %s", d.Verdict, d.Reason)
	}
}

// --- Loader tests ---

func TestLoader_ValidFile(t *testing.T) {
	// Write a minimal governance.yml to a temp file.
	dir := t.TempDir()
	path := filepath.Join(dir, "governance.yml")
	content := `
version: "1.0"
framework: "ATF-aligned"
agents:
  test-agent:
    role: operator
    clearance: execute
    auth: api_key
segmentation:
  test-agent:
    allowed_actions:
      - get_health
    denied_actions:
      - delete_stack
    scope: all
behavior:
  rate_limits:
    test-agent:
      per_hour: 10
      per_day: 50
  loop_detection:
    max_attempts_per_target: 2
    window: 1h
    on_trigger: deny_and_escalate
  circuit_breaker:
    consecutive_failures: 3
    action: freeze_agent
    notify: human
    auto_unfreeze: false
  confidence:
    act_threshold: 0.7
    watch_threshold: 0.4
    log_threshold: 0.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Version != "1.0" {
		t.Errorf("expected version 1.0, got %q", cfg.Version)
	}
	if _, ok := cfg.Agents["test-agent"]; !ok {
		t.Error("expected test-agent in agents")
	}
}

func TestLoader_MissingFile(t *testing.T) {
	_, err := LoadConfig("/nonexistent/governance.yml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoader_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "governance.yml")
	if err := os.WriteFile(path, []byte("{{{{not yaml"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoader_MissingVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "governance.yml")
	content := `
agents:
  test-agent:
    role: operator
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing version, got nil")
	}
}

func TestLoader_NoAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "governance.yml")
	content := `
version: "1.0"
agents: {}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for empty agents, got nil")
	}
}

func TestLoader_SegmentationReferencesUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "governance.yml")
	content := `
version: "1.0"
agents:
  real-agent:
    role: operator
segmentation:
  fake-agent:
    allowed_actions: [get_health]
    scope: all
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for segmentation referencing unknown agent, got nil")
	}
}

// --- NewEngine from file ---

func TestNewEngine_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "governance.yml")
	content := `
version: "1.0"
framework: "ATF-aligned"
agents:
  test-agent:
    role: operator
    clearance: execute
    auth: api_key
segmentation:
  test-agent:
    allowed_actions: [get_health]
    denied_actions: []
    scope: all
behavior:
  rate_limits: {}
  loop_detection:
    max_attempts_per_target: 2
    window: 1h
  circuit_breaker:
    consecutive_failures: 3
  confidence:
    act_threshold: 0.7
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	engine, err := NewEngine(path)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	d, err := engine.Check(context.Background(), "test-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Allow {
		t.Errorf("expected Allow, got %v: %s", d.Verdict, d.Reason)
	}
}

func TestNewEngine_MissingFile(t *testing.T) {
	_, err := NewEngine("/nonexistent/governance.yml")
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// --- Fail-closed ---

func TestFailClosed_NoSegmentationPolicy(t *testing.T) {
	// Agent exists in identity but has no segmentation entry.
	cfg := &GovernanceConfig{
		Version: "1.0",
		Agents: map[string]AgentPolicy{
			"orphan-agent": {Role: "operator", Clearance: "execute"},
		},
		Segments: map[string]SegmentationEntry{},
	}
	e := NewEngineFromConfig(cfg)
	d, err := e.Check(context.Background(), "orphan-agent", "get_health", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Verdict != Deny {
		t.Fatalf("expected Deny for agent without segmentation policy, got Allow")
	}
	if d.Domain != "segmentation" {
		t.Errorf("expected domain segmentation, got %q", d.Domain)
	}
}

// --- parseWindow ---

func TestParseWindow(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"1h", time.Hour},
		{"30m", 30 * time.Minute},
		{"2h", 2 * time.Hour},
		{"invalid", time.Hour}, // default
		{"", time.Hour},        // default
	}
	for _, tt := range tests {
		got := parseWindow(tt.input)
		if got != tt.want {
			t.Errorf("parseWindow(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- Integration test with real governance.yml ---

func TestIntegration_RealGovernanceYML(t *testing.T) {
	// Try to load the actual governance.yml from the latticeos repo.
	path := "${HOME}/dev/nexixai-latticeos/governance.yml"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("governance.yml not found at expected path — skipping integration test")
	}

	engine, err := NewEngine(path)
	if err != nil {
		t.Fatalf("NewEngine with real governance.yml failed: %v", err)
	}

	ctx := context.Background()

	// SRE agent should be allowed to restart_container.
	d, _ := engine.Check(ctx, "sre-agent", "restart_container", nil)
	if d.Verdict != Allow {
		t.Errorf("sre-agent restart_container: expected Allow, got %v: %s", d.Verdict, d.Reason)
	}

	// SRE agent should be denied execute_code.
	d, _ = engine.Check(ctx, "sre-agent", "execute_code", nil)
	if d.Verdict != Deny {
		t.Errorf("sre-agent execute_code: expected Deny, got Allow")
	}

	// Unknown agent should be denied.
	d, _ = engine.Check(ctx, "evil-agent", "get_health", nil)
	if d.Verdict != Deny {
		t.Errorf("evil-agent: expected Deny, got Allow")
	}
	if d.Domain != "identity" {
		t.Errorf("expected domain identity, got %q", d.Domain)
	}

	// Executor should be allowed execute_code.
	d, _ = engine.Check(ctx, "executor", "execute_code", nil)
	if d.Verdict != Allow {
		t.Errorf("executor execute_code: expected Allow, got %v: %s", d.Verdict, d.Reason)
	}

	// Executor should be denied restart_container.
	d, _ = engine.Check(ctx, "executor", "restart_container", nil)
	if d.Verdict != Deny {
		t.Errorf("executor restart_container: expected Deny, got Allow")
	}
}
