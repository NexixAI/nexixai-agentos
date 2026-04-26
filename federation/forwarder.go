package federation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Forwarder struct {
	Client      *http.Client
	MaxAttempts int
	BaseBackoff time.Duration
}

func NewForwarder() *Forwarder {
	maxAttempts := 3
	if v := strings.TrimSpace(os.Getenv("AGENTOS_FED_FORWARD_MAX_ATTEMPTS")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			maxAttempts = parsed
		}
	}
	baseBackoff := 250 * time.Millisecond
	if v := strings.TrimSpace(os.Getenv("AGENTOS_FED_FORWARD_BASE_BACKOFF_MS")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			baseBackoff = time.Duration(parsed) * time.Millisecond
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Configure mTLS if certs are provided
	tlsConfig := loadMTLSClientConfig()
	if tlsConfig != nil {
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
	}

	return &Forwarder{
		Client:      client,
		MaxAttempts: maxAttempts,
		BaseBackoff: baseBackoff,
	}
}

// loadMTLSClientConfig loads mTLS client configuration from environment.
// Returns nil if certs not configured (dev mode fallback to plain HTTP).
func loadMTLSClientConfig() *tls.Config {
	clientCertPath := strings.TrimSpace(os.Getenv("AGENTOS_FED_CLIENT_CERT"))
	clientKeyPath := strings.TrimSpace(os.Getenv("AGENTOS_FED_CLIENT_KEY"))
	caCertPath := strings.TrimSpace(os.Getenv("AGENTOS_FED_CA_CERT"))

	// If no certs configured, fall back to HTTP (dev mode)
	if clientCertPath == "" || clientKeyPath == "" {
		return nil
	}

	// Load client cert/key pair
	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		slog.Error("federation: failed to load mTLS client cert/key pair",
			"cert", clientCertPath, "key", clientKeyPath, "error", err)
		return nil
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS12,
	}

	// Load CA cert for server verification if provided
	if caCertPath != "" {
		caCert, err := os.ReadFile(caCertPath)
		if err == nil {
			caCertPool := x509.NewCertPool()
			if caCertPool.AppendCertsFromPEM(caCert) {
				tlsConfig.RootCAs = caCertPool
			}
		}
	}

	return tlsConfig
}

// ForwardRun calls the remote Agent Orchestrator Run Create endpoint and returns (remote_run_id, remote_events_url, status).
func (f *Forwarder) ForwardRun(remoteAgentOrchestratorBaseURL string, agentID string, tenantID string, principalID string, bearerToken string, runCreateReq map[string]any) (string, string, string, error) {
	maxAttempts := f.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	backoff := f.BaseBackoff
	if backoff <= 0 {
		backoff = 250 * time.Millisecond
	}

	url := strings.TrimRight(remoteAgentOrchestratorBaseURL, "/") + "/v1/agents/" + agentID + "/runs"

	body, err := json.Marshal(runCreateReq)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to marshal run create request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(body))
		if err != nil {
			return "", "", "", fmt.Errorf("failed to create HTTP request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-Id", tenantID)
		if principalID != "" {
			req.Header.Set("X-Principal-Id", principalID)
		}
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
		}

		resp, err := f.Client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var decoded map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
					_ = resp.Body.Close()
					return "", "", "", err
				}
				_ = resp.Body.Close()

				runObj, _ := decoded["run"].(map[string]any)
				runID, _ := runObj["run_id"].(string)
				eventsURL, _ := runObj["events_url"].(string)
				status, _ := runObj["status"].(string)

				if runID == "" || eventsURL == "" {
					return "", "", "", fmt.Errorf("remote response missing run_id/events_url")
				}

				// events_url is expected to be relative; normalize to absolute.
				absEvents := eventsURL
				if strings.HasPrefix(eventsURL, "/") {
					absEvents = strings.TrimRight(remoteAgentOrchestratorBaseURL, "/") + eventsURL
				} else if strings.HasPrefix(eventsURL, "http://") || strings.HasPrefix(eventsURL, "https://") {
					absEvents = eventsURL
				} else {
					absEvents = strings.TrimRight(remoteAgentOrchestratorBaseURL, "/") + "/" + eventsURL
				}

				if status == "" {
					status = "queued"
				}

				return runID, absEvents, status, nil
			}

			lastErr = fmt.Errorf("remote agent-orchestrator returned %s", resp.Status)
			_ = resp.Body.Close()

			// Do not retry non-retryable client errors.
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return "", "", "", lastErr
			}
		}

		// Retry transient failures with simple linear backoff.
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * backoff)
		}
	}

	return "", "", "", lastErr
}
