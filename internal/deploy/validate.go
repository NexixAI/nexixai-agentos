package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Validator struct {
	AgentOrchestrator string
	ModelPolicy       string
	Fed               string
	RepoRoot          string
	TenantID          string
	PrincipalID       string
}

func (v Validator) tenant() string {
	if v.TenantID != "" {
		return v.TenantID
	}
	return "tnt_demo"
}

func (v Validator) principal() string {
	if v.PrincipalID != "" {
		return v.PrincipalID
	}
	return "prn_local"
}

func (v Validator) ValidateAll() ([]CheckResult, error) {
	checks := []CheckResult{}
	// Health
	checks = append(checks, v.checkGET("agent-orchestrator health", v.AgentOrchestrator+"/v1/health"))
	checks = append(checks, v.checkGET("model-policy health", v.ModelPolicy+"/v1/health"))
	checks = append(checks, v.checkGET("federation health", v.Fed+"/v1/federation/health"))

	// Metrics
	checks = append(checks, v.checkGET("agent-orchestrator metrics", v.AgentOrchestrator+"/metrics"))
	checks = append(checks, v.checkGET("model-policy metrics", v.ModelPolicy+"/metrics"))
	checks = append(checks, v.checkGET("federation metrics", v.Fed+"/metrics"))

	// Smoke: Agent Orchestrator create run using canonical request
	reqPath := filepath.Join(v.RepoRoot, "docs", "api", "agent-orchestrator", "examples", "runs-create.request.json")
	b, err := os.ReadFile(reqPath)
	if err == nil {
		checks = append(checks, v.checkPOST("agent-orchestrator create run", v.AgentOrchestrator+"/v1/agents/agt_demo/runs", b))
	} else {
		checks = append(checks, CheckResult{Name: "agent-orchestrator create run", OK: false, Detail: "missing example file", URL: reqPath})
	}

	// Smoke: Model Policy invoke using canonical request (chat.request.json)
	invPath := filepath.Join(v.RepoRoot, "docs", "api", "model-policy", "examples", "chat.request.json")
	b2, err2 := os.ReadFile(invPath)
	if err2 == nil {
		checks = append(checks, v.checkPOST("model-policy invoke", v.ModelPolicy+"/v1/models:invoke", b2))
	} else {
		checks = append(checks, CheckResult{Name: "model-policy invoke", OK: false, Detail: "missing example file", URL: invPath})
	}

	// Optional observability checks (Prometheus/Grafana) if env provided
	promURL := strings.TrimSpace(os.Getenv("AGENTOS_PROM_URL"))
	if promURL != "" {
		checks = append(checks, v.checkGET("prometheus ready", strings.TrimRight(promURL, "/")+"/-/ready"))
	}
	grafURL := strings.TrimSpace(os.Getenv("AGENTOS_GRAFANA_URL"))
	if grafURL != "" {
		checks = append(checks, v.checkGET("grafana health", strings.TrimRight(grafURL, "/")+"/api/health"))
	}

	failed := false
	for _, c := range checks {
		if !c.OK {
			failed = true
			break
		}
	}
	if failed {
		return checks, fmt.Errorf("validation failed (one or more checks failed)")
	}
	return checks, nil
}

func (v Validator) checkGET(name, url string) CheckResult {
	start := time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		latency := int(time.Since(start).Milliseconds())
		return CheckResult{Name: name, OK: false, Detail: err.Error(), URL: url, LatencyMs: latency}
	}
	req.Header.Set("X-Tenant-Id", v.tenant())
	req.Header.Set("X-Principal-Id", v.principal())
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return CheckResult{Name: name, OK: false, Detail: err.Error(), URL: url, LatencyMs: latency}
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body) //nolint:errcheck // drain response body
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return CheckResult{Name: name, OK: true, Detail: resp.Status, URL: url, LatencyMs: latency}
	}
	return CheckResult{Name: name, OK: false, Detail: resp.Status, URL: url, LatencyMs: latency}
}

func (v Validator) checkPOST(name, url string, body []byte) CheckResult {
	start := time.Now()
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		latency := int(time.Since(start).Milliseconds())
		return CheckResult{Name: name, OK: false, Detail: err.Error(), URL: url, LatencyMs: latency}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", v.tenant())
	req.Header.Set("X-Principal-Id", v.principal())
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return CheckResult{Name: name, OK: false, Detail: err.Error(), URL: url, LatencyMs: latency}
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return CheckResult{Name: name, OK: false, Detail: "failed to read response: " + err.Error(), URL: url, LatencyMs: latency}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var tmp any
		if json.Unmarshal(rb, &tmp) == nil {
			return CheckResult{Name: name, OK: true, Detail: resp.Status, URL: url, LatencyMs: latency}
		}
		return CheckResult{Name: name, OK: true, Detail: resp.Status + " (non-json body)", URL: url, LatencyMs: latency}
	}
	return CheckResult{Name: name, OK: false, Detail: resp.Status, URL: url, LatencyMs: latency}
}
