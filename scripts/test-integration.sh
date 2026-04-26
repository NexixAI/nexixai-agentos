#!/usr/bin/env bash
set -euo pipefail

# Run integration tests against a local PostgreSQL instance.
# Requires PostgreSQL running on localhost:5432 with the test database.

export AGENTOS_STORAGE_BACKEND=postgres
export AGENTOS_DB_HOST=${AGENTOS_DB_HOST:-localhost}
export AGENTOS_DB_PORT=${AGENTOS_DB_PORT:-5432}
export AGENTOS_DB_NAME=${AGENTOS_DB_NAME:-agentos_test}
export AGENTOS_DB_USER=${AGENTOS_DB_USER:-agentos}
export AGENTOS_DB_PASSWORD=${AGENTOS_DB_PASSWORD:-testpass}
export AGENTOS_DB_SSLMODE=${AGENTOS_DB_SSLMODE:-disable}

echo "Running integration tests against ${AGENTOS_DB_HOST}:${AGENTOS_DB_PORT}/${AGENTOS_DB_NAME}"
go test -race -tags integration -v ./internal/storage/postgres/...
