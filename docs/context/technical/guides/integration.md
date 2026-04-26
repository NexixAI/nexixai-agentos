# Integration Guide

How to integrate AgentOS with external systems: webhooks, OIDC providers, OpenAI-compatible clients, federation, and observability pipelines.

<!-- machine-readable: format=integration-guide, version=v1.07 -->

---

## 1. Webhooks

<!-- integration: webhooks -->

AgentOS sends HTTP POST notifications for run lifecycle events.

### Configuration

```env
AGENTOS_WEBHOOK_URL=https://your-server.com/webhooks/agentos
AGENTOS_WEBHOOK_SECRET=your-hmac-secret
AGENTOS_WEBHOOK_STEP_EVENTS=false    # set true for verbose step events
```

### Event Types

| Event | Trigger |
|-------|---------|
| `run.started` | Run transitions from `queued` to `running` |
| `run.completed` | Run finishes successfully |
| `run.failed` | Run fails (provider error, tool error, timeout) |
| `run.step` | Individual step completes (only if `AGENTOS_WEBHOOK_STEP_EVENTS=true`) |

### Payload Format

```json
{
  "event_type": "run.completed",
  "timestamp": "2026-03-10T12:00:00Z",
  "tenant_id": "tnt_acme",
  "agent_id": "support-bot",
  "run_id": "run_abc123",
  "payload": {
    "status": "completed",
    "output": "...",
    "usage": {
      "prompt_tokens": 150,
      "completion_tokens": 200,
      "total_tokens": 350
    }
  }
}
```

### HMAC Verification

When `AGENTOS_WEBHOOK_SECRET` is set, every request includes:

```
X-Webhook-Signature: sha256=<hex-encoded-hmac>
```

Verify in your handler:

```python
import hmac, hashlib

def verify_webhook(body: bytes, signature: str, secret: str) -> bool:
    expected = "sha256=" + hmac.new(
        secret.encode(), body, hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(expected, signature)
```

```go
func verifyWebhook(body []byte, signature, secret string) bool {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(expected), []byte(signature))
}
```

### Retry Behavior

- 3 attempts with exponential backoff
- Retries on 5xx responses and network errors
- No retry on 2xx or 4xx responses

---

## 2. OIDC / OAuth2

<!-- integration: oidc -->

AgentOS supports OIDC for authentication in production deployments.

### Configuration

```env
AGENTOS_AUTH_MODE=oidc
AGENTOS_OIDC_ISSUER_URL=https://auth.example.com
AGENTOS_OIDC_AUDIENCE=agentos
AGENTOS_OIDC_TENANT_CLAIM=tenant_id    # or "tid", "org_id", etc.
```

### How It Works

1. Client obtains JWT from your OIDC provider
2. Client sends `Authorization: Bearer <jwt>` to AgentOS
3. AgentOS validates the JWT against the issuer's JWKS endpoint
4. Tenant ID is extracted from the configured claim
5. RBAC role is determined from the `role` or `roles` claim

### Required JWT Claims

| Claim | Purpose | Example |
|-------|---------|---------|
| `iss` | Must match `AGENTOS_OIDC_ISSUER_URL` | `https://auth.example.com` |
| `aud` | Must match `AGENTOS_OIDC_AUDIENCE` | `agentos` |
| `tenant_id` (configurable) | Tenant association | `tnt_acme` |
| `sub` | Principal ID (user identity) | `user@acme.com` |
| `role` or `roles` | RBAC role | `developer` |
| `exp` | Expiration time | Unix timestamp |

### Token Refresh

If `AGENTOS_OIDC_TOKEN_ENDPOINT` is set (or auto-discovered from issuer):

```bash
curl -X POST http://127.0.0.1:50081/v1/auth/refresh \
  -H "Content-Type: application/json" \
  -d '{"refresh_token": "your-refresh-token"}'
```

### Supported Algorithms

RS256, ES256, EdDSA (auto-detected from JWKS).

### Fail-Closed Behavior

If OIDC validation fails for any reason (network error, invalid token, missing claims), the request is **denied**. AgentOS never falls through to permissive mode on auth errors.

---

## 3. OpenAI SDK Compatibility

<!-- integration: openai-compat -->

AgentOS exposes OpenAI-compatible endpoints, allowing direct use of OpenAI SDKs.

### Python

```python
import openai

client = openai.OpenAI(
    base_url="http://agentos-host:50081/v1",
    api_key="<AGENTOS_KEY>..."   # AgentOS API key
)

# Chat completions
response = client.chat.completions.create(
    model="nvidia/Llama-3.3-70B-Instruct-FP8",
    messages=[
        {"role": "system", "content": "You are helpful."},
        {"role": "user", "content": "Hello!"}
    ],
    temperature=0.7,
    max_tokens=1024
)
print(response.choices[0].message.content)

# Streaming
stream = client.chat.completions.create(
    model="nvidia/Llama-3.3-70B-Instruct-FP8",
    messages=[{"role": "user", "content": "Tell me a story"}],
    stream=True
)
for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")

# Embeddings
embeddings = client.embeddings.create(
    model="text-embedding-ada-002",
    input="Hello world"
)

# List models
models = client.models.list()
```

### JavaScript/TypeScript

```typescript
import OpenAI from 'openai';

const client = new OpenAI({
  baseURL: 'http://agentos-host:50081/v1',
  apiKey: '<AGENTOS_KEY>...',
});

const response = await client.chat.completions.create({
  model: 'nvidia/Llama-3.3-70B-Instruct-FP8',
  messages: [{ role: 'user', content: 'Hello!' }],
});
```

### curl

```bash
curl -X POST http://127.0.0.1:50081/v1/chat/completions \
  -H "Authorization: Bearer <AGENTOS_KEY>..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "nvidia/Llama-3.3-70B-Instruct-FP8",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### Compatible Endpoints

| OpenAI Endpoint | AgentOS Endpoint | Notes |
|-----------------|------------------|-------|
| `POST /v1/chat/completions` | Same | Full support including streaming |
| `POST /v1/embeddings` | Same | Proxied to model provider |
| `GET /v1/models` | Same | Returns configured models |
| `GET /v1/models/{id}` | Same | Single model lookup |

### Integration with Open WebUI

Point Open WebUI at AgentOS:

```env
OPENAI_API_BASE_URL=http://agentos-host:50081/v1
OPENAI_API_KEY=<AGENTOS_KEY>...
```

---

## 4. Federation

<!-- integration: federation -->

Connect multiple AgentOS instances for cross-node agent execution.

### Architecture

```
Node A (us-east)              Node B (eu-west)
┌──────────────────┐         ┌──────────────────┐
│ Agent Orchestrator│         │ Agent Orchestrator│
│ Model Policy     │         │ Model Policy     │
│ Federation ←─────┼────────►│ Federation       │
└──────────────────┘  mTLS   └──────────────────┘
```

### Peer Configuration

Create `peers.seed.json`:

```json
[
  {
    "stack_id": "stk_us_east",
    "region": "us-east-1",
    "endpoints": {
      "agent_orchestrator_base_url": "https://node-a:8081",
      "model_policy_base_url": "https://node-a:8082",
      "federation_base_url": "https://node-a:8083"
    }
  },
  {
    "stack_id": "stk_eu_west",
    "region": "eu-west-1",
    "endpoints": {
      "agent_orchestrator_base_url": "https://node-b:8081",
      "model_policy_base_url": "https://node-b:8082",
      "federation_base_url": "https://node-b:8083"
    }
  }
]
```

Set on each node:

```env
AGENTOS_PEERS_FILE=/etc/agentos/peers.seed.json
AGENTOS_STACK_ID=stk_us_east          # unique per node
AGENTOS_ENVIRONMENT=prod
AGENTOS_REGION=us-east-1
```

### mTLS Setup

Generate certificates (example with openssl):

```bash
# CA
openssl req -x509 -newkey rsa:4096 -keyout ca.key -out ca.crt -days 365 -nodes

# Server cert (per node)
openssl req -newkey rsa:4096 -keyout server.key -out server.csr -nodes
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -out server.crt -days 365

# Client cert (per node)
openssl req -newkey rsa:4096 -keyout client.key -out client.csr -nodes
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -out client.crt -days 365
```

Configure each node:

```env
AGENTOS_FED_REQUIRE_MTLS=true
AGENTOS_FED_SERVER_CERT=/certs/server.crt
AGENTOS_FED_SERVER_KEY=/certs/server.key
AGENTOS_FED_CA_CERT=/certs/ca.crt
AGENTOS_FED_CLIENT_CERT=/certs/client.crt
AGENTOS_FED_CLIENT_KEY=/certs/client.key
```

### Forward a Run

```bash
curl -X POST http://node-a:50083/v1/federation/runs:forward \
  -H "X-Tenant-Id: tnt_acme" \
  -H "Content-Type: application/json" \
  -d '{
    "forward": {
      "target_selector": {"stack_id": "stk_eu_west"},
      "run_request": {
        "agent_id": "support-bot",
        "input": "Handle this query in EU"
      }
    }
  }'
```

### Stream Federated Events

```bash
curl -N http://node-a:50083/v1/federation/runs/{remote_run_id}/events \
  -H "X-Tenant-Id: tnt_acme"
```

---

## 5. Prometheus & Grafana

<!-- integration: observability -->

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: 'agentos-agent-orchestrator'
    static_configs:
      - targets: ['agentos-host:50081']
    metrics_path: /metrics
    # If AGENTOS_METRICS_REQUIRE_AUTH=true:
    bearer_token: '<service-token>'

  - job_name: 'agentos-model-policy'
    static_configs:
      - targets: ['agentos-host:50082']
    metrics_path: /metrics

  - job_name: 'agentos-federation'
    static_configs:
      - targets: ['agentos-host:50083']
    metrics_path: /metrics
```

### Key Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `agentos_model_call_duration_seconds` | histogram | Model API call latency |
| `agentos_tokens_total` | counter | Token consumption (prompt/completion) |
| `agentos_runs_total` | counter | Run lifecycle transitions |
| `agentos_tool_duration_seconds` | histogram | Tool execution latency |
| `agentos_active_runs` | gauge | Currently running runs |
| `agentos_queue_depth` | gauge | Queued runs waiting for workers |
| `agentos_circuit_breaker_state` | gauge | Circuit breaker state (0=closed, 1=open, 2=half-open) |
| `agentos_quota_denied_total` | counter | Quota/rate limit denials |

### OpenTelemetry

```env
AGENTOS_OTEL_ENDPOINT=http://otel-collector:4318
AGENTOS_OTEL_SERVICE_NAME=agentos
AGENTOS_OTEL_SAMPLE_RATE=0.1
```

Traces include spans for: HTTP requests, model provider calls, storage operations, tool execution, federation forwarding.

W3C Trace Context headers (`traceparent`, `tracestate`) are propagated across services and to federated nodes.

---

## 6. Agent-to-Agent Delegation

<!-- integration: agent-delegation -->

Agents can invoke other agents within the same tenant using the built-in `agent_invoke` tool.

### Agent Config

```json
{
  "agent_id": "orchestrator-agent",
  "config": {
    "tools": ["agent_invoke"],
    "system_prompt": "You coordinate tasks by delegating to specialist agents."
  }
}
```

The `agent_invoke` tool is available to the agent during execution. When called, it creates a child run on the target agent and waits for completion.

### Constraints

- Max delegation depth: `AGENTOS_MAX_DELEGATION_DEPTH` (default 5)
- Same-tenant only (no cross-tenant delegation)
- Child runs visible via `GET /v1/runs/{run_id}/children`

---

## 7. CI/CD Integration

<!-- integration: ci-cd -->

### GitHub Actions Example

```yaml
name: Deploy Agent
on:
  push:
    branches: [main]
    paths: ['agents/**']

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Create/Update Agent
        run: |
          curl -X PUT ${{ secrets.AGENTOS_URL }}/v1/agents/support-bot \
            -H "Authorization: Bearer ${{ secrets.AGENTOS_API_KEY }}" \
            -H "Content-Type: application/json" \
            -d @agents/support-bot.json

      - name: Smoke Test
        run: |
          RUN=$(curl -s -X POST ${{ secrets.AGENTOS_URL }}/v1/agents/support-bot/runs \
            -H "Authorization: Bearer ${{ secrets.AGENTOS_API_KEY }}" \
            -H "Content-Type: application/json" \
            -d '{"input":"health check"}')

          RUN_ID=$(echo $RUN | jq -r '.run.run_id')

          # Poll for completion (max 60s)
          for i in $(seq 1 12); do
            STATUS=$(curl -s ${{ secrets.AGENTOS_URL }}/v1/runs/$RUN_ID \
              -H "Authorization: Bearer ${{ secrets.AGENTOS_API_KEY }}" | jq -r '.run.status')
            if [ "$STATUS" = "completed" ]; then exit 0; fi
            if [ "$STATUS" = "failed" ] || [ "$STATUS" = "error" ]; then exit 1; fi
            sleep 5
          done
          exit 1
```

### Automating with AI Agents

An AI agent can manage AgentOS programmatically using the API:

```python
# Example: automated agent deployment pipeline
import requests

AGENTOS_URL = "http://agentos:50081"
API_KEY = "aos_live_..."

def deploy_agent(agent_id: str, config: dict):
    """Create or update an agent."""
    r = requests.put(
        f"{AGENTOS_URL}/v1/agents/{agent_id}",
        headers={"Authorization": f"Bearer {API_KEY}"},
        json=config
    )
    if r.status_code == 404:
        r = requests.post(
            f"{AGENTOS_URL}/v1/agents",
            headers={"Authorization": f"Bearer {API_KEY}"},
            json={"agent_id": agent_id, **config}
        )
    r.raise_for_status()
    return r.json()

def run_agent(agent_id: str, input: str) -> str:
    """Create a run and wait for completion."""
    r = requests.post(
        f"{AGENTOS_URL}/v1/agents/{agent_id}/runs",
        headers={"Authorization": f"Bearer {API_KEY}"},
        json={"input": input}
    )
    r.raise_for_status()
    run_id = r.json()["run"]["run_id"]

    # Poll for completion
    while True:
        r = requests.get(
            f"{AGENTOS_URL}/v1/runs/{run_id}",
            headers={"Authorization": f"Bearer {API_KEY}"}
        )
        status = r.json()["run"]["status"]
        if status in ("completed", "failed", "error"):
            return r.json()["run"]
        time.sleep(2)

def create_tenant(tenant_id: str, name: str):
    """Provision a new tenant."""
    r = requests.post(
        f"{AGENTOS_URL}/v1/admin/tenants",
        headers={"Authorization": f"Bearer {API_KEY}"},
        json={"tenant_id": tenant_id, "name": name}
    )
    r.raise_for_status()
    return r.json()

def create_api_key(tenant_id: str, name: str, role: str = "developer"):
    """Create an API key for a tenant."""
    r = requests.post(
        f"{AGENTOS_URL}/v1/tenants/api-keys",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "X-Tenant-Id": tenant_id
        },
        json={"name": name, "role": role}
    )
    r.raise_for_status()
    return r.json()["api_key"]["key"]  # Store securely!
```
