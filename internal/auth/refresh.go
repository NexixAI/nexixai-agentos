package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/httpx"
)

// RefreshRequest is the expected JSON body for POST /v1/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshHandler returns an http.HandlerFunc that proxies token refresh
// requests to the configured IdP token endpoint. It performs no custom
// token issuance — it is a pure proxy.
//
// The IdP token endpoint is read from AGENTOS_OIDC_TOKEN_ENDPOINT.
// When that variable is empty (OIDC disabled / IdP doesn't support refresh),
// the handler returns 501 Not Implemented.
func RefreshHandler(client *http.Client) http.HandlerFunc {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		cid := httpx.CorrelationID(r)
		httpx.RequestIDHeader(w, cid)

		if r.Method != http.MethodPost {
			httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed",
				"only POST is accepted", cid, false)
			return
		}

		tokenEndpoint := strings.TrimSpace(os.Getenv("AGENTOS_OIDC_TOKEN_ENDPOINT"))
		if tokenEndpoint == "" {
			httpx.Error(w, http.StatusNotImplemented, "not_implemented",
				"OIDC token refresh is not configured", cid, false)
			return
		}

		// Validate token endpoint URL scheme to prevent SSRF (v9.0 M-4).
		if u, err := url.Parse(tokenEndpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			slog.Error("refresh: OIDC token endpoint has disallowed scheme",
				"endpoint", tokenEndpoint, "correlation_id", cid)
			httpx.Error(w, http.StatusBadGateway, "invalid_configuration",
				"OIDC token endpoint URL scheme not allowed", cid, true)
			return
		}

		// Parse request body.
		var req RefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("refresh: invalid request body", "error", err, "correlation_id", cid)
			httpx.Error(w, http.StatusBadRequest, "invalid_request",
				"request body must be JSON with refresh_token field", cid, false)
			return
		}
		if req.RefreshToken == "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_request",
				"refresh_token is required", cid, false)
			return
		}

		// Build IdP token request (standard OAuth2 refresh_token grant).
		idpBody, err := json.Marshal(map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": req.RefreshToken,
		})
		if err != nil {
			slog.Error("refresh: failed to marshal IdP request", "error", err, "correlation_id", cid)
			httpx.Error(w, http.StatusInternalServerError, "internal_error",
				"failed to build IdP request", cid, true)
			return
		}

		idpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, tokenEndpoint, bytes.NewReader(idpBody))
		if err != nil {
			slog.Error("refresh: failed to create IdP request", "error", err, "correlation_id", cid)
			httpx.Error(w, http.StatusInternalServerError, "internal_error",
				"failed to create IdP request", cid, true)
			return
		}
		idpReq.Header.Set("Content-Type", "application/json")

		idpResp, err := client.Do(idpReq)
		if err != nil {
			slog.Error("refresh: IdP request failed", "error", err, "correlation_id", cid)
			httpx.Error(w, http.StatusBadGateway, "idp_unavailable",
				"failed to reach identity provider", cid, true)
			return
		}
		defer idpResp.Body.Close()

		// Read IdP response body (cap at 1 MB to avoid unbounded reads).
		respBody, err := io.ReadAll(io.LimitReader(idpResp.Body, 1<<20))
		if err != nil {
			slog.Error("refresh: failed to read IdP response", "error", err, "correlation_id", cid)
			httpx.Error(w, http.StatusBadGateway, "idp_unavailable",
				"failed to read identity provider response", cid, true)
			return
		}

		// If IdP returned non-2xx, treat as auth failure.
		if idpResp.StatusCode < 200 || idpResp.StatusCode >= 300 {
			slog.Warn("refresh: IdP rejected token refresh",
				"status", idpResp.StatusCode, "correlation_id", cid)
			httpx.Error(w, http.StatusUnauthorized, "refresh_denied",
				"identity provider rejected the refresh token", cid, false)
			return
		}

		// Proxy the IdP's successful response as-is.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(respBody); err != nil {
			slog.Error("refresh: failed to write response", "error", err, "correlation_id", cid)
		}
	}
}
