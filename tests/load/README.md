# Load Tests

k6 load test suite for AgentOS platform. Three scenarios cover single-tenant burst,
multi-tenant steady-state, and federation forwarding workloads.

## Prerequisites

Install [k6](https://k6.io/docs/getting-started/installation/):

```bash
# macOS
brew install k6

# Debian/Ubuntu
sudo gpg -k
sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg \
  --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D68
echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" \
  | sudo tee /etc/apt/sources.list.d/k6.list
sudo apt-get update && sudo apt-get install k6

# Docker
docker pull grafana/k6
```

## Running

Run all scenarios via Make:

```bash
make load-test
```

Run individual scenarios:

```bash
k6 run tests/load/chat_burst.js
k6 run tests/load/multi_tenant.js
k6 run tests/load/federation.js
```

### Configuration

All scripts accept environment variables:

| Variable   | Default                  | Description              |
|------------|--------------------------|--------------------------|
| `BASE_URL` | `http://localhost:9091`   | Target API base URL      |
| `API_KEY`  | `test-api-key`           | Bearer token for auth    |
| `AUTH_TOKEN`| (falls back to API_KEY) | Alternative auth token   |

Example with overrides:

```bash
k6 run --env BASE_URL=http://staging:9091 --env API_KEY=my-secret tests/load/chat_burst.js
```

## Scenarios

### 1. Single-Tenant Burst (`chat_burst.js`)

Simulates a single tenant sending a burst of traffic.

- **VUs**: 0 -> 100 over 30s, sustain 100 for 2m, ramp down 10s
- **Endpoints**: `POST /v1/runs`, `GET /v1/agents`, `GET /v1/runs`
- **Purpose**: Validate that a single tenant can burst without degradation

### 2. Multi-Tenant Steady State (`multi_tenant.js`)

Simulates 10 tenants each generating steady traffic concurrently.

- **VUs**: 100 total (10 tenants x 10 VUs each), sustain for 5m
- **Endpoints**: `POST /v1/runs`, `GET /v1/runs`, `GET /v1/agents`
- **Purpose**: Validate tenant isolation under concurrent load; ensure no
  cross-tenant leakage or per-tenant throttling issues

### 3. Federation Forwarding (`federation.js`)

Simulates traffic hitting federated endpoints that may be forwarded to peer nodes.

- **VUs**: 0 -> 50 over 15s, sustain 50 for 2m, ramp down 10s
- **Endpoints**: `POST /v1/runs`, `POST /v1/runs/{id}/events`, `GET /v1/agents`
- **Purpose**: Validate federation forwarding latency and reliability

## Thresholds

All scenarios enforce the same thresholds:

| Metric              | Threshold | Description                    |
|---------------------|-----------|--------------------------------|
| `http_req_duration` | p99 < 500ms | 99th percentile latency      |
| `errors`            | rate < 1%   | Custom error rate metric     |
| `http_reqs`         | rate > 50   | Sustained throughput (RPS)   |

If any threshold is breached, k6 exits with a non-zero status code.

## Baseline Metrics

Baseline measurements taken against a single-node deployment (4 CPU, 8 GB RAM)
running AgentOS v1.06 with file-backed storage:

| Scenario         | p50 (ms) | p95 (ms) | p99 (ms) | Error Rate | Avg RPS |
|------------------|----------|----------|----------|------------|---------|
| chat_burst       | < 50     | < 200    | < 500    | < 0.1%     | > 150   |
| multi_tenant     | < 60     | < 250    | < 500    | < 0.1%     | > 100   |
| federation       | < 80     | < 300    | < 500    | < 0.5%     | > 80    |

These are target baselines. After running the suite against your environment,
compare your results to these values. Significant deviations (> 2x latency or
< 50% throughput) should be investigated.

## Interpreting Results

k6 prints a summary at the end of each run. Key metrics to examine:

- **http_req_duration**: Look at p50, p95, p99. If p99 exceeds 500ms, investigate
  slow queries, connection pooling, or resource contention.
- **http_reqs**: The `rate` value shows requests per second. Below 50 RPS indicates
  a bottleneck (often CPU, database, or network).
- **errors**: Any error rate above 1% warrants investigation. Check server logs for
  5xx responses, timeouts, or connection refused errors.
- **iteration_duration**: Total time per VU iteration including sleep. If this is
  much higher than expected, the API is likely throttling or queuing.

### Common failure patterns

| Symptom                        | Likely Cause                        |
|--------------------------------|-------------------------------------|
| p99 spikes during ramp-up      | Connection pool exhaustion          |
| Steady error rate ~5%          | Rate limiting or auth misconfiguration |
| Throughput plateaus at < 50 RPS| CPU saturation or single-threaded bottleneck |
| Errors only in multi_tenant    | Tenant isolation overhead or mutex contention |
| Federation latency >> local    | Network hop cost or slow peer health checks |

## CI Integration

These tests are CI-optional. To run in CI, gate on a label or manual trigger:

```yaml
# Example GitHub Actions snippet
load-test:
  if: contains(github.event.pull_request.labels.*.name, 'load-test') || github.event_name == 'workflow_dispatch'
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: grafana/k6-action@v0.3.1
      with:
        filename: tests/load/chat_burst.js
      env:
        BASE_URL: http://localhost:9091
        API_KEY: ${{ secrets.LOAD_TEST_API_KEY }}
```
