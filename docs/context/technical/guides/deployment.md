# Deployment Guide

How to deploy AgentOS in local, Docker Compose, and Kubernetes environments. Structured for AI-driven automation.

<!-- machine-readable: format=deployment-guide, version=v1.07 -->

---

## Deployment Methods

| Method | Use case | Complexity |
|--------|----------|------------|
| CLI (`agentos up`) | Local development | Low |
| Docker Compose | Single-node, staging | Low |
| Docker Compose (federation) | Multi-node testing | Medium |
| Kubernetes (kustomize) | Production | Medium-High |

---

## 1. Local Development (CLI)

### Prerequisites

- Docker + Docker Compose v2
- Go 1.22+ (to build CLI)

### Steps

```bash
# 1. Build CLI
go build -o agentos ./cmd/agentos

# 2. Deploy (idempotent)
./agentos up

# 3. Validate
./agentos validate

# 4. Check status
./agentos status
```

### Default Ports

| Service | Container Port | Host Port |
|---------|---------------|-----------|
| Agent Orchestrator | 8081 | 50081 |
| Model Policy | 8082 | 50082 |
| Federation | 8083 | 50083 |

### Configuration

Copy and edit the env file:

```bash
cp deploy/local/secrets.example.env deploy/local/secrets.env
# Edit secrets.env with real values
```

### Tear Down

```bash
./agentos nuke           # stop containers, remove networks
./agentos nuke --hard    # also remove volumes (destructive)
```

---

## 2. Docker Compose (Single Node)

### File: `deploy/local/compose.yaml`

<!-- automation: compose-file=deploy/local/compose.yaml -->

```bash
# Start all services
docker compose -f deploy/local/compose.yaml up -d --build

# With GPU workers (optional)
COMPOSE_PROFILES=workers docker compose -f deploy/local/compose.yaml up -d --build

# View logs
docker compose -f deploy/local/compose.yaml logs -f

# Stop
docker compose -f deploy/local/compose.yaml down
```

### Services

| Service | Image | Command | Port |
|---------|-------|---------|------|
| agent-orchestrator | Built from Dockerfile | `serve --addr :8081 agent-orchestrator` | 50081:8081 |
| model-policy | Built from Dockerfile | `serve --addr :8082 model-policy` | 50082:8082 |
| federation | Built from Dockerfile | `serve --addr :8083 federation` | 50083:8083 |
| worker-node-a | busybox (placeholder) | GPU executor | Profile: workers |
| worker-node-b | busybox (placeholder) | GPU executor | Profile: workers |

### Environment

Environment set via `env_file` (secrets.example.env) + `environment` block in compose. See [Configuration Reference](configuration.md) for all variables.

### TLS (Optional)

Uncomment TLS lines in compose.yaml:

```yaml
environment:
  - AGENTOS_TLS_CERT=/certs/tls.crt
  - AGENTOS_TLS_KEY=/certs/tls.key
volumes:
  - ./certs/agent-orchestrator:/certs:ro
```

---

## 3. Docker Compose (Federation, 2-Node)

### File: `deploy/local/compose.federation-2node.yaml`

Runs two full AgentOS nodes on one host for federation testing.

<!-- automation: compose-file=deploy/local/compose.federation-2node.yaml -->

```bash
# Start both nodes
docker compose \
  -f deploy/local/compose.federation-2node.yaml \
  -f deploy/local/compose.federation-2node.ADDENDUM.yaml \
  up -d --build

# Run federation E2E tests
docker compose \
  -f deploy/local/compose.federation-2node.yaml \
  -f deploy/local/compose.federation-2node.ADDENDUM.yaml \
  up --build --abort-on-container-exit federation-e2e

# Tear down
docker compose \
  -f deploy/local/compose.federation-2node.yaml \
  -f deploy/local/compose.federation-2node.ADDENDUM.yaml \
  down -v
```

### Node Layout

| Node | Agent Orchestrator | Model Policy | Federation |
|------|-------------------|--------------|------------|
| Node A | :8081 | :8082 | :8083 |
| Node B | :8084 | :8085 | :8086 |

**Invariant**: All multi-service tests use Docker DNS names (e.g., `nodea-federation`), never `localhost`.

---

## 4. Kubernetes (Kustomize)

### Directory: `deploy/k8s/`

<!-- automation: k8s-dir=deploy/k8s/base -->

### Prerequisites

- kubectl configured for target cluster
- Container image pushed to registry

### Build and Push Image

```bash
# Build
docker build -t ghcr.io/nexixai/agentos:latest .

# Push
docker push ghcr.io/nexixai/agentos:latest

# Or via Makefile
make docker-build docker-push
```

### Deploy

```bash
# Create namespace
kubectl create namespace agentos

# Create secrets
kubectl -n agentos create secret generic agentos-secrets \
  --from-literal=AGENTOS_DB_PASSWORD='<password>' \
  --from-literal=AGENTOS_MODEL_API_KEY='<key>' \
  --from-literal=AGENTOS_ENCRYPTION_KEY='<base64-key>'

# Create peers ConfigMap (federation)
kubectl -n agentos create configmap agentos-peers \
  --from-file=peers.seed.json=deploy/local/peers.seed.json

# Apply manifests
kubectl apply -k deploy/k8s/base

# Or via Makefile
make k8s-apply
```

### Resources Created

| Resource | Name | Description |
|----------|------|-------------|
| Deployment | agentos-agent-orchestrator | Agent Orchestrator (1 replica) |
| Deployment | agentos-model-policy | Model Policy (1 replica) |
| Deployment | agentos-federation | Federation (1 replica) |
| Service | agentos-agent-orchestrator | ClusterIP :8081 |
| Service | agentos-model-policy | ClusterIP :8082 |
| Service | agentos-federation | ClusterIP :8083 |
| PVC | agentos-data | 10Gi ReadWriteOnce |
| Secret | agentos-secrets | Credentials (user-created) |
| ConfigMap | agentos-peers | Peer registry (user-created) |

### Probes

All deployments include:

```yaml
livenessProbe:
  httpGet:
    path: /v1/health
    port: <service-port>
  initialDelaySeconds: 5
  periodSeconds: 15

readinessProbe:
  httpGet:
    path: /v1/ready
    port: <service-port>
  initialDelaySeconds: 3
  periodSeconds: 5
```

### Resource Limits

Default per container:

```yaml
resources:
  requests:
    memory: "128Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"
    cpu: "1000m"
```

### TLS in Kubernetes

Create a TLS secret and uncomment the volume mount in the deployment:

```bash
kubectl -n agentos create secret tls agentos-agent-orchestrator-tls \
  --cert=tls.crt --key=tls.key
```

Then add to deployment env:
```yaml
- name: AGENTOS_TLS_CERT
  value: /certs/tls.crt
- name: AGENTOS_TLS_KEY
  value: /certs/tls.key
```

---

## 5. Production Checklist

<!-- automation: checklist=production -->

### Required

- [ ] `AGENTOS_PROFILE=prod`
- [ ] `AGENTOS_STORAGE_BACKEND=postgres` with production database
- [ ] `AGENTOS_AUTH_MODE=oidc` with valid OIDC issuer
- [ ] `AGENTOS_METRICS_REQUIRE_AUTH=true`
- [ ] `AGENTOS_LOG_FORMAT=json`
- [ ] `AGENTOS_ENCRYPTION_KEY` set (AES-256-GCM)
- [ ] TLS terminated at ingress or via `AGENTOS_TLS_CERT`/`AGENTOS_TLS_KEY`
- [ ] Secrets loaded via `_FILE` suffix (Docker secrets, Vault)
- [ ] Backup schedule configured (see [Backup & Restore](../ops/backup-restore.md))
- [ ] Monitoring configured (Prometheus scraping `/metrics`)

### Recommended

- [ ] `AGENTOS_OTEL_ENDPOINT` set for distributed tracing
- [ ] `AGENTOS_WEBHOOK_URL` configured for run lifecycle notifications
- [ ] `AGENTOS_CORS_ORIGINS` restricted to known origins
- [ ] `AGENTOS_PII_DEFAULT_MODE=redact` for PII protection
- [ ] Federation mTLS enabled (`AGENTOS_FED_REQUIRE_MTLS=true`)
- [ ] Resource limits tuned for workload
- [ ] Data retention policies reviewed

---

## 6. Observability Stack

### Directory: `deploy/observability/`

Optional Prometheus + Grafana + Tempo stack for metrics, dashboards, and traces.

```bash
docker compose -f deploy/observability/compose.yaml up -d
```

Configure AgentOS to export:
```env
AGENTOS_OTEL_ENDPOINT=http://otel-collector:4318
AGENTOS_OTEL_SERVICE_NAME=agentos
AGENTOS_OTEL_SAMPLE_RATE=0.1
```

---

## Dockerfile

<!-- automation: dockerfile=Dockerfile -->

Multi-stage build:

```
Stage 1 (build): golang:1.24-alpine → CGO_ENABLED=0 go build
Stage 2 (run):   alpine:3.20 → non-root user, ca-certificates, curl
```

- Runs as non-root user `agentos`
- HEALTHCHECK built-in: `curl -sf http://localhost:${AGENTOS_HEALTH_PORT:-8081}/v1/health`
- Entrypoint: `agentos`

---

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make test` | Run tests |
| `make test-race` | Run tests with race detector |
| `make test-integration` | Run Postgres integration tests |
| `make test-all` | All tests (race + integration) |
| `make coverage` | Generate coverage report |
| `make vuln-check` | Run govulncheck |
| `make load-test` | Run k6 load tests |
| `make docker-build` | Build Docker image |
| `make docker-push` | Push to registry |
| `make k8s-apply` | Apply Kubernetes manifests |
