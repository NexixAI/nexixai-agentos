package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Status represents the health state.
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
)

// CheckResult represents one sub-check.
type CheckResult struct {
	Status    Status `json:"status"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Response is the full health check response.
type Response struct {
	Status  Status                 `json:"status"`
	Version string                 `json:"version"`
	Checks  map[string]CheckResult `json:"checks"`
}

// Check is a named health check function.
type Check struct {
	Name     string
	Required bool // if true, failure -> unhealthy; if false -> degraded
	Fn       func(ctx context.Context) CheckResult
}

// Checker runs registered health checks.
type Checker struct {
	mu      sync.Mutex
	version string
	checks  []Check
	ready   bool
}

func NewChecker(version string) *Checker {
	return &Checker{version: version}
}

func (c *Checker) Register(check Check) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks = append(c.checks, check)
}

// Run executes all checks and produces a Response.
func (c *Checker) Run(ctx context.Context) Response {
	c.mu.Lock()
	checks := make([]Check, len(c.checks))
	copy(checks, c.checks)
	c.mu.Unlock()

	resp := Response{
		Status:  StatusHealthy,
		Version: c.version,
		Checks:  make(map[string]CheckResult),
	}

	for _, ch := range checks {
		result := ch.Fn(ctx)
		resp.Checks[ch.Name] = result

		if result.Status != StatusHealthy {
			if ch.Required {
				resp.Status = StatusUnhealthy
			} else if resp.Status == StatusHealthy {
				resp.Status = StatusDegraded
			}
		}
	}

	return resp
}

// Handler returns an http.HandlerFunc for the health endpoint.
func (c *Checker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		resp := c.Run(r.Context())

		w.Header().Set("Content-Type", "application/json")
		switch resp.Status {
		case StatusUnhealthy:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusOK)
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort HTTP response
	}
}

// SetReady marks the service as ready/not-ready.
func (c *Checker) SetReady(ready bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready = ready
}

// ReadyHandler returns an http.HandlerFunc for the readiness endpoint.
func (c *Checker) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		c.mu.Lock()
		ready := c.ready
		c.mu.Unlock()

		if !ready {
			resp := Response{
				Status:  StatusUnhealthy,
				Version: c.version,
				Checks:  map[string]CheckResult{"startup": {Status: StatusUnhealthy, Error: "service starting"}},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort HTTP response
			return
		}

		// Run all checks for readiness.
		resp := c.Run(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if resp.Status == StatusUnhealthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort HTTP response
	}
}

// ProviderCheck returns a Check that verifies a model provider's base URL is reachable.
func ProviderCheck(name string, required bool, baseURL string) Check {
	return Check{
		Name:     name,
		Required: required,
		Fn: func(ctx context.Context) CheckResult {
			start := time.Now()
			client := &http.Client{Timeout: 5 * time.Second}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
			if err != nil {
				return CheckResult{
					Status: StatusUnhealthy,
					Error:  err.Error(),
				}
			}
			resp, err := client.Do(req)
			latency := time.Since(start).Milliseconds()
			if err != nil {
				return CheckResult{
					Status:    StatusUnhealthy,
					LatencyMs: latency,
					Error:     err.Error(),
				}
			}
			resp.Body.Close()
			if resp.StatusCode >= 500 {
				return CheckResult{
					Status:    StatusUnhealthy,
					LatencyMs: latency,
					Error:     "provider returned " + resp.Status,
				}
			}
			return CheckResult{
				Status:    StatusHealthy,
				LatencyMs: latency,
			}
		},
	}
}

// DBCheck returns a Check that pings a database and reports latency.
func DBCheck(name string, required bool, db *sql.DB) Check {
	return Check{
		Name:     name,
		Required: required,
		Fn: func(ctx context.Context) CheckResult {
			start := time.Now()
			err := db.PingContext(ctx)
			latency := time.Since(start).Milliseconds()
			if err != nil {
				return CheckResult{
					Status:    StatusUnhealthy,
					LatencyMs: latency,
					Error:     err.Error(),
				}
			}
			return CheckResult{
				Status:    StatusHealthy,
				LatencyMs: latency,
			}
		},
	}
}
