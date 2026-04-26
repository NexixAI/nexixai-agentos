#!/usr/bin/env pwsh
# Local smoke test for AgentOS stack running on localhost.
# Verifies health for agent-orchestrator/model-policy/federation and federation peer endpoints.

[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

function Invoke-SmokeCheck {
    param(
        [string]$Name,
        [string]$Url,
        [bool]$ExpectJson = $true,
        [hashtable]$ExpectFields = @{},
        [int[]]$AllowedStatusCodes = @(200)
    )

    $maxAttempts = 20
    $sleepSeconds = 1
    $lastBody = ""
    $lastCode = 0
    $lastError = $null

    for ($i = 1; $i -le $maxAttempts; $i++) {
        $tmpBody = [System.IO.Path]::GetTempFileName()
        try {
            $raw = & curl.exe -4 -s -S --max-time 5 -o $tmpBody -w "%{http_code}" $Url 2>&1
            $codeStr = ($raw | Select-Object -Last 1).Trim()
            if (-not $codeStr) { $codeStr = "000" }
            $lastCode = [int]$codeStr
            $lastBody = ""
            if (Test-Path $tmpBody) {
                $lastBody = Get-Content $tmpBody -Raw
            }
        } catch {
            $lastError = $_.Exception.Message
            $lastCode = 0
        } finally {
            Remove-Item $tmpBody -ErrorAction SilentlyContinue
        }

        if ($AllowedStatusCodes -contains $lastCode) {
            if (-not $ExpectJson) {
                return [pscustomobject]@{
                    Name    = $Name
                    Url     = $Url
                    Success = $true
                    Notes   = "status $lastCode"
                }
            }

            try {
                $json = $lastBody | ConvertFrom-Json -ErrorAction Stop
            } catch {
                $lastError = "invalid JSON: $($_.Exception.Message)"
                # retry if attempts remain
                if ($i -lt $maxAttempts) { Start-Sleep -Seconds $sleepSeconds; continue }
                break
            }

            $bad = @()
            foreach ($k in $ExpectFields.Keys) {
                $expected = $ExpectFields[$k]
                $actual = $json.$k
                if ($actual -ne $expected) {
                    $bad += "$k=$actual (expected $expected)"
                }
            }

            if ($bad.Count -eq 0) {
                return [pscustomobject]@{
                    Name    = $Name
                    Url     = $Url
                    Success = $true
                    Notes   = "status $lastCode"
                }
            } else {
                $lastError = "field mismatch: " + ($bad -join "; ")
            }
        } else {
            $lastError = "status $lastCode"
        }

        if ($i -lt $maxAttempts) {
            Start-Sleep -Seconds $sleepSeconds
        }
    }

    $bodySnippet = ""
    if ($lastBody) {
        $bodySnippet = $lastBody.Substring(0, [Math]::Min($lastBody.Length, 200))
    }

    $notes = @()
    if ($lastCode -ne 0) { $notes += "status=$lastCode" }
    if ($lastError) { $notes += $lastError }
    if ($bodySnippet) { $notes += "body=$bodySnippet" }
    if ($notes.Count -eq 0) { $notes += "unknown error" }

    return [pscustomobject]@{
        Name    = $Name
        Url     = $Url
        Success = $false
        Notes   = ($notes -join " | ")
    }
}

$targets = @(
    @{ Name = "agent-orchestrator"; Url = "http://127.0.0.1:50081/v1/health"; ExpectJson = $true; ExpectFields = @{ service = "agent-orchestrator"; status = "ok" } },
    @{ Name = "model-policy"; Url = "http://127.0.0.1:50082/v1/health"; ExpectJson = $true; ExpectFields = @{ service = "model-policy"; status = "ok" } },
    @{ Name = "federation-health"; Url = "http://127.0.0.1:50083/v1/federation/health"; ExpectJson = $true; ExpectFields = @{ service = "federation"; status = "ok" } },
    @{ Name = "federation-peer-info"; Url = "http://127.0.0.1:50083/v1/federation/peer"; ExpectJson = $true; ExpectFields = @{}; AllowedStatusCodes = @(200,503) },
    @{ Name = "federation-peer-capabilities"; Url = "http://127.0.0.1:50083/v1/federation/peer/capabilities"; ExpectJson = $true; ExpectFields = @{}; AllowedStatusCodes = @(200,503) }
)

$results = @()
foreach ($t in $targets) {
    Write-Host "Checking $($t.Name) -> $($t.Url)..."
    $results += Invoke-SmokeCheck @t
}

Write-Host ""
Write-Host "Summary:"
$results |
    Select-Object @{Name = "service"; Expression = { $_.Name } },
                  @{Name = "url"; Expression = { $_.Url } },
                  @{Name = "status"; Expression = { if ($_.Success) { "ok" } else { "fail" } } },
                  @{Name = "notes"; Expression = { $_.Notes } } |
    Format-Table -AutoSize

if ($results | Where-Object { -not $_.Success }) {
    Write-Error "Smoke test failed."
    exit 1
}

Write-Host "Smoke test passed."
exit 0
