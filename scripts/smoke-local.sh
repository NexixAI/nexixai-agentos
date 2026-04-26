#!/usr/bin/env bash
#
# Local smoke test for AgentOS platform
# Hits health endpoints with retries and bounded timeout
#
# Usage:
#   ./scripts/smoke-local.sh
#
# Windows (PowerShell):
#   See scripts/smoke-local.ps1 or run manually:
#   curl.exe -4 --retry 5 --retry-delay 2 --retry-max-time 30 http://127.0.0.1:50081/v1/health
#
set -euo pipefail

# Configuration
MAX_RETRIES=10
RETRY_DELAY=2
TIMEOUT=5

# Endpoints (use 127.0.0.1 for IPv4, avoid localhost DNS issues)
AGENT_ORCHESTRATOR="http://127.0.0.1:50081"
MODEL_POLICY="http://127.0.0.1:50082"
FEDERATION="http://127.0.0.1:50083"

# Colors (if terminal supports it)
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Retry a curl request
# Usage: retry_curl "name" "url"
retry_curl() {
    local name="$1"
    local url="$2"
    local attempt=1

    while [ $attempt -le $MAX_RETRIES ]; do
        log_info "Checking $name (attempt $attempt/$MAX_RETRIES)..."

        # Force IPv4 with -4, timeout with --max-time
        if curl -4 -sf --max-time $TIMEOUT "$url" > /dev/null 2>&1; then
            log_info "$name: OK"
            return 0
        fi

        if [ $attempt -lt $MAX_RETRIES ]; then
            log_warn "$name: failed, retrying in ${RETRY_DELAY}s..."
            sleep $RETRY_DELAY
        fi
        attempt=$((attempt + 1))
    done

    log_error "$name: FAILED after $MAX_RETRIES attempts"
    return 1
}

main() {
    echo "========================================"
    echo "AgentOS Local Smoke Test"
    echo "========================================"
    echo ""

    local failed=0

    # Health checks
    retry_curl "Agent Orchestrator health" "${AGENT_ORCHESTRATOR}/v1/health" || failed=1
    retry_curl "Model Policy health" "${MODEL_POLICY}/v1/health" || failed=1
    retry_curl "Federation health" "${FEDERATION}/v1/federation/health" || failed=1

    echo ""
    echo "========================================"

    if [ $failed -eq 0 ]; then
        log_info "All smoke tests PASSED"
        exit 0
    else
        log_error "Some smoke tests FAILED"
        exit 1
    fi
}

main "$@"
