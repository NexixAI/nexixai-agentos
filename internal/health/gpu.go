package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// GPUVRAMCheck returns a Check that queries Prometheus for DCGM GPU memory
// metrics. Reports degraded when average VRAM utilization exceeds 90% while
// vLLM has zero active requests — a sign of memory over-allocation rather
// than active workload.
func GPUVRAMCheck(name string, required bool, prometheusURL string) Check {
	return Check{
		Name:     name,
		Required: required,
		Fn: func(ctx context.Context) CheckResult {
			start := time.Now()
			client := &http.Client{Timeout: 5 * time.Second}

			// Query average VRAM utilization ratio across all GPUs.
			vramUtil, vramErr := queryPrometheusScalar(ctx, client, prometheusURL,
				`avg(DCGM_FI_DEV_FB_USED / (DCGM_FI_DEV_FB_USED + DCGM_FI_DEV_FB_FREE))`)
			latency := time.Since(start).Milliseconds()
			if vramErr != nil {
				return CheckResult{
					Status:    StatusDegraded,
					LatencyMs: latency,
					Error:     "gpu vram query failed: " + vramErr.Error(),
				}
			}

			// Query active vLLM requests.
			activeReqs, reqsErr := queryPrometheusScalar(ctx, client, prometheusURL,
				`sum(vllm:num_requests_running) or vector(0)`)
			latency = time.Since(start).Milliseconds()

			// If we can't determine active requests, report VRAM only.
			if reqsErr != nil {
				if vramUtil > 0.90 {
					return CheckResult{
						Status:    StatusDegraded,
						LatencyMs: latency,
						Error:     fmt.Sprintf("gpu vram %.0f%% (active requests unknown: %v)", vramUtil*100, reqsErr),
					}
				}
				return CheckResult{
					Status:    StatusHealthy,
					LatencyMs: latency,
				}
			}

			// High VRAM + zero requests = over-allocated or leak.
			if vramUtil > 0.90 && activeReqs < 1 {
				return CheckResult{
					Status:    StatusDegraded,
					LatencyMs: latency,
					Error:     fmt.Sprintf("gpu vram %.0f%% with 0 active requests — possible over-allocation (check vLLM --gpu-memory-utilization)", vramUtil*100),
				}
			}

			return CheckResult{
				Status:    StatusHealthy,
				LatencyMs: latency,
			}
		},
	}
}

// promQueryResponse mirrors the subset of the Prometheus /api/v1/query
// response needed to extract a single scalar value.
type promQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value [2]json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// queryPrometheusScalar executes a PromQL query and extracts a single scalar
// value from the first result vector element.
func queryPrometheusScalar(ctx context.Context, client *http.Client, baseURL, promql string) (float64, error) {
	reqURL := baseURL + "/api/v1/query?query=" + url.QueryEscape(promql)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return 0, err
	}

	var result promQueryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	if result.Status != "success" || len(result.Data.Result) == 0 {
		return 0, fmt.Errorf("no data returned")
	}

	// Prometheus instant query value is [unix_timestamp, "string_value"].
	var valStr string
	if err := json.Unmarshal(result.Data.Result[0].Value[1], &valStr); err != nil {
		return 0, fmt.Errorf("parse value: %w", err)
	}

	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse float: %w", err)
	}

	return val, nil
}
