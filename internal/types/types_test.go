package types

import (
	"encoding/json"
	"testing"
)

func TestRunJSONRoundTrip(t *testing.T) {
	r := Run{
		TenantID: "tnt_test",
		AgentID:  "agt_1",
		RunID:    "run_abc",
		Status:   "queued",
		RetryOf:  "run_prev",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Run
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RunID != r.RunID {
		t.Errorf("RunID = %q, want %q", got.RunID, r.RunID)
	}
	if got.RetryOf != r.RetryOf {
		t.Errorf("RetryOf = %q, want %q", got.RetryOf, r.RetryOf)
	}
}

func TestAgentJSONRoundTrip(t *testing.T) {
	a := Agent{
		AgentID:  "agt_test",
		TenantID: "tnt_test",
		Name:     "Test Agent",
		Version:  "v1",
		Status:   "active",
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Agent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.AgentID != a.AgentID {
		t.Errorf("AgentID = %q, want %q", got.AgentID, a.AgentID)
	}
}

func TestRunOptionsStreamEventsDefault(t *testing.T) {
	var opts RunOptions
	if opts.StreamEvents {
		t.Error("StreamEvents should default to false")
	}
}
