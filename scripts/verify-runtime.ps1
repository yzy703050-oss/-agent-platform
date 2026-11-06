param(
    [Parameter(Mandatory)][Alias('Profile')][string]$RuntimeProfilePath,
    [string]$Case = 'A00',
    [switch]$RequireCases
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
function Finish-Failure([string]$Reason) {
    @{ ok = $false; scope = 'runtime_verification'; reason = $Reason } | ConvertTo-Json -Compress
    exit 1
}
try {
    $settings = Get-Content -LiteralPath $RuntimeProfilePath -Raw | ConvertFrom-Json -AsHashtable
    if ($settings.version -ne 1 -or $settings.isolated -ne $true) { Finish-Failure 'invalid_or_unisolated_profile' }
} catch { Finish-Failure 'invalid_or_unisolated_profile' }
# Register a case only after its real implementation and checks exist.
$available = @('A00')
$requested = if ($Case -eq 'All') { @(0..16 | ForEach-Object { 'A{0:D2}' -f $_ }) } else { @($Case) }
$missing = @($requested | Where-Object { $_ -notin $available })
if ($missing.Count -gt 0) { Finish-Failure ('required_cases_missing:' + ($missing -join ',')) }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Finish-Failure 'go_missing' }
$repositoryRoot = Split-Path $PSScriptRoot
$logDir = Join-Path $repositoryRoot '.cache/runtime-v1'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
Push-Location (Join-Path $repositoryRoot 'backend')
try {
    $result = @(& go test -json ./internal/config -run '^TestRuntimeConfig' -count=1 2>&1)
    $testExit = $LASTEXITCODE
    $result | Set-Content -LiteralPath (Join-Path $logDir 'A00-config.jsonl')
    if ($testExit -ne 0) { Finish-Failure 'A00_config_failed' }
    $events = @($result | ForEach-Object { try { $_.ToString() | ConvertFrom-Json -AsHashtable } catch {} })
    $passed = @($events | Where-Object { $_.Action -eq 'pass' -and $_.ContainsKey('Test') }).Count
    $skipped = @($events | Where-Object { $_.Action -eq 'skip' }).Count
    if ($passed -eq 0 -or $skipped -gt 0) { Finish-Failure 'A00_tests_missing_or_skipped' }
    $output = @(& go mod verify 2>&1)
    $output | Set-Content -LiteralPath (Join-Path $logDir 'A00-modules.log')
    if ($LASTEXITCODE -ne 0) { Finish-Failure 'A00_modules_failed' }
    $output = @(& go build ./cmd/... 2>&1)
    $output | Set-Content -LiteralPath (Join-Path $logDir 'A00-build.log')
    if ($LASTEXITCODE -ne 0) { Finish-Failure 'A00_build_failed' }
    @{ ok = $true; scope = 'A00_config_modules_build_only'; passedTests = $passed; servicesValidated = $false } | ConvertTo-Json -Compress
} finally { Pop-Location }
