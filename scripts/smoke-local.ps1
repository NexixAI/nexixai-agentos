# Local smoke test for AgentOS platform (Windows PowerShell)
# Hits health endpoints with retries and bounded timeout
#
# Usage:
#   .\scripts\smoke-local.ps1
#
# Note: Uses curl.exe (not PowerShell's Invoke-WebRequest alias)
#       Forces IPv4 with -4 flag to avoid Windows DNS issues

$ErrorActionPreference = "Stop"

# Configuration
$MaxRetries = 10
$RetryDelay = 2
$Timeout = 5

# Endpoints (use 127.0.0.1 for IPv4)
$Endpoints = @(
    @{ Name = "Agent Orchestrator health"; Url = "http://127.0.0.1:50081/v1/health" },
    @{ Name = "Model Policy health"; Url = "http://127.0.0.1:50082/v1/health" },
    @{ Name = "Federation health"; Url = "http://127.0.0.1:50083/v1/federation/health" }
)

function Write-Info { param($msg) Write-Host "[INFO] $msg" -ForegroundColor Green }
function Write-Warn { param($msg) Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Write-Err { param($msg) Write-Host "[ERROR] $msg" -ForegroundColor Red }

function Test-Endpoint {
    param(
        [string]$Name,
        [string]$Url
    )

    for ($attempt = 1; $attempt -le $MaxRetries; $attempt++) {
        Write-Info "Checking $Name (attempt $attempt/$MaxRetries)..."

        try {
            # Use curl.exe (not Invoke-WebRequest), force IPv4
            $result = & curl.exe -4 -sf --max-time $Timeout $Url 2>$null
            if ($LASTEXITCODE -eq 0) {
                Write-Info "$Name`: OK"
                return $true
            }
        } catch {
            # Ignore, will retry
        }

        if ($attempt -lt $MaxRetries) {
            Write-Warn "$Name`: failed, retrying in ${RetryDelay}s..."
            Start-Sleep -Seconds $RetryDelay
        }
    }

    Write-Err "$Name`: FAILED after $MaxRetries attempts"
    return $false
}

Write-Host "========================================"
Write-Host "AgentOS Local Smoke Test (Windows)"
Write-Host "========================================"
Write-Host ""

$failed = $false

foreach ($ep in $Endpoints) {
    if (-not (Test-Endpoint -Name $ep.Name -Url $ep.Url)) {
        $failed = $true
    }
}

Write-Host ""
Write-Host "========================================"

if ($failed) {
    Write-Err "Some smoke tests FAILED"
    exit 1
} else {
    Write-Info "All smoke tests PASSED"
    exit 0
}
