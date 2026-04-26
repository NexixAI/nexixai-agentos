# Backup & Restore Operations Guide

Procedures for backing up and restoring AgentOS PostgreSQL data.

<!-- machine-readable: format=ops-runbook, version=v1.07 -->

---

## 1. Automated Backup with pg_dump

<!-- automation: step=schedule-backup -->

Schedule nightly backups via cron:

```cron
0 3 * * * pg_dump --format=custom --compress=9 -d agentos -f /backup/agentos_$(date +\%Y\%m\%d).dump 2>&1 | logger -t agentos-backup
```

Recommended `pg_dump` flags:

| Flag | Purpose |
|------|---------|
| `--format=custom` | Enables selective restore, compression, parallel restore |
| `--compress=9` | Maximum zlib compression |
| `--no-owner` | Omit ownership commands (useful for cross-env restores) |
| `--verbose` | Progress output for logging |

After a successful backup, update the environment variable so `GET /v1/admin/backup-check` reports it:

```bash
export AGENTOS_LAST_BACKUP_TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
```

**Retention policy**: keep 7 daily, 4 weekly, and 3 monthly backups. Use a cleanup script or object lifecycle rules if storing in cloud storage.

---

## 2. WAL Archiving for Point-in-Time Recovery

<!-- automation: step=wal-archiving -->

Enable continuous archiving in `postgresql.conf`:

```
wal_level = replica
archive_mode = on
archive_command = 'cp %p /wal_archive/%f'
```

This allows point-in-time recovery (PITR) to any moment between base backups.

For production, use `pgBackRest` or `wal-g` instead of raw `cp` for compression, encryption, and cloud storage support.

---

## 3. Restore Procedures

### Full Restore from pg_dump

<!-- automation: step=full-restore -->

```bash
# 1. Stop the application
systemctl stop agentos

# 2. Drop and recreate the database
psql -c "DROP DATABASE IF EXISTS agentos;"
psql -c "CREATE DATABASE agentos;"

# 3. Restore from backup
pg_restore --dbname=agentos --verbose --clean --if-exists /backup/agentos_20260310.dump

# 4. Verify (see Section 5)
psql -d agentos -c "SELECT count(*) FROM runs;"

# 5. Restart the application (runs migrations automatically)
systemctl start agentos
```

### Point-in-Time Recovery (PITR)

<!-- automation: step=pitr-restore -->

```bash
# 1. Stop PostgreSQL
systemctl stop postgresql

# 2. Clear data directory and restore base backup
rm -rf /var/lib/postgresql/data/*
pg_basebackup -D /var/lib/postgresql/data -R

# 3. Configure recovery target
echo "recovery_target_time = '2026-03-10 02:59:00 UTC'" >> /var/lib/postgresql/data/postgresql.conf
echo "restore_command = 'cp /wal_archive/%f %p'" >> /var/lib/postgresql/data/postgresql.conf
touch /var/lib/postgresql/data/recovery.signal

# 4. Start PostgreSQL (replays WAL up to target time)
systemctl start postgresql
```

---

## 4. Schema Migrations

<!-- automation: step=migrations -->

AgentOS runs versioned migrations automatically on startup. Each migration has a SHA-256 checksum for integrity verification.

### Dry-Run Mode

Preview migrations without applying:

```bash
AGENTOS_MIGRATION_DRY_RUN=true agentos serve agent-orchestrator
```

### Migration Table

```sql
SELECT version, description, checksum, applied_at FROM schema_migrations ORDER BY version;
```

If checksums don't match (modified migration after applying), a warning is logged. Investigate before deploying further.

---

## 5. Verification Checklist

<!-- automation: step=verify-restore -->

After every restore:

- [ ] Row counts match pre-backup (`GET /v1/admin/backup-check`)
- [ ] Test query: `SELECT * FROM runs ORDER BY created_at DESC LIMIT 5;`
- [ ] FK integrity: `SELECT count(*) FROM agents a LEFT JOIN tenants t ON a.tenant_id = t.tenant_id WHERE t.tenant_id IS NULL;` (must be 0)
- [ ] Application starts without errors
- [ ] Health: `curl -sf http://localhost:50081/v1/health` (host port; use `:8081` if running inside the container network)
- [ ] Readiness: `curl -sf http://localhost:50081/v1/ready`
- [ ] Audit log entries present up to expected timestamp
- [ ] API keys intact: `SELECT key_id, tenant_id, name, role, created_at, revoked_at FROM api_keys;`
- [ ] Schema migrations table: `SELECT * FROM schema_migrations ORDER BY version;`

---

## 6. Data Retention

<!-- automation: step=data-retention -->

AgentOS purges expired data daily (default 03:00 UTC).

| Data | Default | Min | Env Var |
|------|---------|-----|---------|
| Completed runs | 90 days | 7 | `AGENTOS_RETENTION_RUNS_DAYS` |
| Events | 30 days | 7 | `AGENTOS_RETENTION_EVENTS_DAYS` |
| Audit log | 365 days | 90 | `AGENTOS_RETENTION_AUDIT_DAYS` |
| Usage records | 730 days | 90 | `AGENTOS_RETENTION_USAGE_DAYS` |

Running/queued runs are never purged. Manual trigger: `POST /v1/admin/purge`.

---

## 7. Disaster Recovery

<!-- automation: step=disaster-recovery -->

### Full Cluster Loss

1. Provision new infrastructure (database + application servers)
2. Restore latest base backup from off-site storage (S3, GCS)
3. Replay WAL archives up to most recent segment
4. Run verification checklist (Section 5)
5. Update DNS / load balancer to new cluster
6. Notify on-call and document incident

### RTO / RPO Targets

| Metric | Target |
|--------|--------|
| RPO (Recovery Point Objective) | < 1 hour (with WAL archiving) |
| RTO (Recovery Time Objective) | < 4 hours (manual), < 1 hour (automated) |

### Off-Site Storage

Store backups in separate region or cloud provider. Encrypt at rest using `gpg` or cloud-native KMS. Test restore from off-site quarterly.

---

## 8. GDPR Data Operations

<!-- automation: step=gdpr -->

### Export Tenant Data

```bash
curl -X POST http://agentos:50081/v1/tenants/data-export \
  -H "Authorization: Bearer $KEY" \
  -H "X-Tenant-Id: tnt_acme"
# Returns 202 — async export to AGENTOS_EXPORT_DIR
```

### Delete Tenant Data

```bash
curl -X POST http://agentos:50081/v1/tenants/data-delete \
  -H "Authorization: Bearer $KEY" \
  -H "X-Tenant-Id: tnt_acme"
# Returns 202 — removes agents, runs, events, memory, KV, usage, audit
```

### Full Tenant Deletion

```bash
curl -X DELETE http://agentos:50081/v1/admin/tenants/tnt_acme \
  -H "Authorization: Bearer $ADMIN_KEY"
```
