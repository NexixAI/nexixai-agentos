# Security Hardening Guide

Configuration and procedures for securing AgentOS in production.

<!-- machine-readable: format=ops-runbook, version=v1.07 -->

---

## 1. Authentication

<!-- automation: section=authentication -->

### OIDC (Recommended for Production)

```env
AGENTOS_AUTH_MODE=oidc
AGENTOS_OIDC_ISSUER_URL=https://auth.example.com
AGENTOS_OIDC_AUDIENCE=agentos
AGENTOS_OIDC_TENANT_CLAIM=tenant_id
```

- JWT validated against issuer's JWKS endpoint
- Supported algorithms: RS256, ES256, EdDSA
- **Fail-closed**: invalid/expired/malformed tokens always rejected
- Token refresh: `POST /v1/auth/refresh`

### API Keys

API keys (`aos_live_...`) provide programmatic access. Keys are hashed at rest.

```bash
# Create key
curl -X POST http://agentos:50081/v1/tenants/api-keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "X-Tenant-Id: tnt_acme" \
  -d '{"name":"CI Key","role":"developer","expires_in_days":90}'

# Revoke key
curl -X DELETE http://agentos:50081/v1/tenants/api-keys/key_123 \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### Dev Headers

`AGENTOS_ALLOW_DEV_HEADERS=1` allows `X-Tenant-Id` and `X-Principal-Id` to set auth context. **Never enable in production.**

---

## 2. RBAC

<!-- automation: section=rbac -->

| Role | Agents | Runs | Usage | API Keys | Members | Audit |
|------|--------|------|-------|----------|---------|-------|
| `viewer` | read | read | read | — | — | — |
| `developer` | CRUD | create/cancel | read | — | — | — |
| `admin` | CRUD | create/cancel | read/export | manage | — | read |
| `owner` | CRUD | create/cancel | read/export | manage | manage | read |

Platform admin scope (`tenants:admin`) is separate and controls cross-tenant operations.

---

## 3. TLS

<!-- automation: section=tls -->

### Option A: TLS Termination at Ingress (Recommended)

Terminate at load balancer, reverse proxy, or cloud LB. Application runs plain HTTP internally.

### Option B: Direct TLS

```env
AGENTOS_TLS_CERT=/certs/tls.crt
AGENTOS_TLS_KEY=/certs/tls.key
```

- TLS 1.2 minimum (Go's `crypto/tls` defaults)
- Both must be set together
- Use trusted CA or internal PKI
- Automate rotation with cert-manager (K8s)

### Federation mTLS

```env
AGENTOS_FED_REQUIRE_MTLS=true
AGENTOS_FED_SERVER_CERT=/certs/server.crt
AGENTOS_FED_SERVER_KEY=/certs/server.key
AGENTOS_FED_CA_CERT=/certs/ca.crt
AGENTOS_FED_CLIENT_CERT=/certs/client.crt
AGENTOS_FED_CLIENT_KEY=/certs/client.key
```

---

## 4. Encryption at Rest

<!-- automation: section=encryption -->

AES-256-GCM for sensitive fields: API keys, conversation memory, KV values.

### Setup

```bash
openssl rand -base64 32  # generate key
export AGENTOS_ENCRYPTION_KEY="<base64-key>"
# Or: AGENTOS_ENCRYPTION_KEY_FILE="/run/secrets/encryption_key"
```

### Key Rotation

1. Set new key as `AGENTOS_ENCRYPTION_KEY`
2. Set old key as `AGENTOS_ENCRYPTION_KEY_PREVIOUS`
3. Restart — new writes use new key, reads try both
4. Re-encrypt existing data
5. Remove `AGENTOS_ENCRYPTION_KEY_PREVIOUS`

Plaintext mode: if `AGENTOS_ENCRYPTION_KEY` is unset.

---

## 5. Secrets Management

<!-- automation: section=secrets -->

### `_FILE` Suffix Pattern

| Variable | File Variant |
|----------|-------------|
| `AGENTOS_DB_PASSWORD` | `AGENTOS_DB_PASSWORD_FILE` |
| `AGENTOS_MODEL_API_KEY` | `AGENTOS_MODEL_API_KEY_FILE` |
| `AGENTOS_ENCRYPTION_KEY` | `AGENTOS_ENCRYPTION_KEY_FILE` |

The `_FILE` variant takes precedence. Use with Docker secrets, Vault agent, or K8s secret volumes.

---

## 6. CORS

<!-- automation: section=cors -->

```env
AGENTOS_CORS_ORIGINS=https://app.example.com,https://admin.example.com
```

- Never use `*` in production
- Empty = CORS headers disabled
- Review origins during security audits

---

## 7. PII Detection

<!-- automation: section=pii -->

```env
AGENTOS_PII_ENABLED=true
AGENTOS_PII_DEFAULT_MODE=redact
```

| Mode | Behavior |
|------|----------|
| `warn` | Log PII detection, pass through |
| `redact` | Replace PII with `[REDACTED]` |

Scan specific runs: `GET /v1/admin/pii-scan?run_id=run_abc123`

---

## 8. Input Validation

<!-- automation: section=input-validation -->

Built-in protections (no configuration needed):

| Protection | Details |
|------------|---------|
| Request body size | `AGENTOS_MAX_BODY_SIZE` (default 1MB) |
| Tenant ID format | `tnt_` prefix, alphanumeric + underscore, 3–64 chars |
| Agent ID format | Alphanumeric + hyphen + underscore, 3–64 chars |
| KV value size | `AGENTOS_KV_MAX_VALUE_SIZE` (default 64KB) |
| KV keys per agent | `AGENTOS_KV_MAX_KEYS_PER_AGENT` (default 1000) |
| Memory messages | `AGENTOS_MEMORY_MAX_MESSAGES` (default 50) |
| Metric labels | No unbounded cardinality |

---

## 9. Dependency Scanning

<!-- automation: section=vuln-scanning -->

```bash
make vuln-check    # runs govulncheck ./...
```

| Status | Meaning | Action |
|--------|---------|--------|
| **Called** | Vulnerable function reachable | Fix immediately |
| **Imported** | Package imported, function not called | Fix at next opportunity |
| **stdlib** | Standard library vulnerability | Update Go version |

### Update Policy

| Category | SLA |
|----------|-----|
| Security-critical | Patch within 7 days |
| Non-critical | Monthly review |
| Go toolchain | Update within 2 weeks |

---

## 10. Network Security

<!-- automation: section=network -->

- `AGENTOS_METRICS_REQUIRE_AUTH=true` — protect `/metrics` endpoint
- Federation: always enable mTLS in production
- `AGENTOS_FED_AUTH_DISABLED=1` is dev only — logs warning in prod
- Use K8s network policies to restrict pod-to-pod communication

---

## 11. Audit Trail

<!-- automation: section=audit -->

All security-relevant actions logged:

| Action | Logged |
|--------|--------|
| Auth failures (401/403) | Yes |
| Run creation/cancellation | Yes |
| Agent CRUD | Yes |
| Tenant operations | Yes |
| API key create/revoke | Yes |
| Member changes | Yes |
| Data exports/deletes | Yes |
| Policy decisions | Yes |

```env
AGENTOS_AUDIT_SINK=file:/var/agentos/audit.log
```

Query: `GET /v1/admin/audit-log?action=auth.failed&limit=100`

Retention: `AGENTOS_RETENTION_AUDIT_DAYS=365` (minimum 90 days).

---

## Production Security Checklist

<!-- automation: checklist=security -->

- [ ] `AGENTOS_PROFILE=prod`
- [ ] `AGENTOS_AUTH_MODE=oidc` (not `open`)
- [ ] `AGENTOS_ALLOW_DEV_HEADERS` unset
- [ ] `AGENTOS_METRICS_REQUIRE_AUTH=true`
- [ ] `AGENTOS_ENCRYPTION_KEY` set
- [ ] TLS enabled (ingress or direct)
- [ ] Federation mTLS enabled
- [ ] `AGENTOS_FED_AUTH_DISABLED` unset
- [ ] Secrets loaded via `_FILE` suffix
- [ ] `AGENTOS_CORS_ORIGINS` restricted
- [ ] `AGENTOS_PII_DEFAULT_MODE=redact`
- [ ] Audit sink configured
- [ ] `govulncheck` passing in CI
- [ ] Backup schedule configured
- [ ] Data retention policies reviewed
