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
$available = @{
    A00 = @{ package = './internal/config'; pattern = '^TestRuntimeConfig'; scope = 'A00_config_modules_build_only' }
    A01 = @{ package = './internal/runtimeprotocol'; pattern = '.'; scope = 'A01_protocol_modules_build_only' }
    A02 = @{ package = './tests/runtimeintegration'; pattern = '^Test(RuntimeMigration|SchemaRoles)'; scope = 'A02_postgres_fresh_schema_roles_only' }
    A03 = @{ package = './tests/runtimeintegration'; pattern = '^Test(AuthorizationCommit|ServiceRole|Budget|Control|AtomicSession|Redis)'; scope = 'A03_postgres_authority_budget_redis_loss_only' }
    A04 = @{ package = './tests/runtimeintegration'; pattern = '^Test(Admit|DeadlineStarts|ExpiredEpoch|TerminalMessage)'; scope = 'A04_postgres_admission_entry_lifecycle_only' }
    A05 = @{ package = @('./tests/runtimeintegration','./internal/client/llm','./internal/client/mcp','./internal/client/providerhttp','./internal/toolquery','./internal/evidence'); pattern = '^Test(DirectQuery|RetryFour|SavedEvidence|MCPHTTP|Metered|RuntimeHTTP|RuntimeStatus|SingleAttempt|ResilienceFour|CrossServiceEvidence|BackgroundExecution|ForegroundExecution|WebProvider|PolicyProvider|TravelProvider|RemoteIdentity)'; scope = 'A05_scoped_queries_model_accounting_http_mcp_evidence_only' }
}
$requested = if ($Case -eq 'All') { @(0..16 | ForEach-Object { 'A{0:D2}' -f $_ }) } else { @($Case) }
$missing = @($requested | Where-Object { -not $available.ContainsKey($_) })
if ($missing.Count -gt 0) { Finish-Failure ('required_cases_missing:' + ($missing -join ',')) }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Finish-Failure 'go_missing' }
$repositoryRoot = Split-Path $PSScriptRoot
$logDir = Join-Path $repositoryRoot '.cache/runtime-v1'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
Push-Location (Join-Path $repositoryRoot 'backend')
try {
    $definition = $available[$Case]
    $result = @(& go test -json $definition.package -run $definition.pattern -count=1 2>&1)
    $testExit = $LASTEXITCODE
    $result | Set-Content -LiteralPath (Join-Path $logDir ($Case + '-tests.jsonl'))
    if ($testExit -ne 0) { Finish-Failure ($Case + '_tests_failed') }
    $events = @($result | ForEach-Object { try { $_.ToString() | ConvertFrom-Json -AsHashtable } catch {} })
    $passed = @($events | Where-Object { $_.Action -eq 'pass' -and $_.ContainsKey('Test') }).Count
    $skipped = @($events | Where-Object { $_.Action -eq 'skip' }).Count
    if ($passed -eq 0 -or $skipped -gt 0) { Finish-Failure ($Case + '_tests_missing_or_skipped') }
    $output = @(& go mod verify 2>&1)
    $output | Set-Content -LiteralPath (Join-Path $logDir ($Case + '-modules.log'))
    if ($LASTEXITCODE -ne 0) { Finish-Failure ($Case + '_modules_failed') }
    $buildDir = Join-Path $logDir 'bin'
    New-Item -ItemType Directory -Force -Path $buildDir | Out-Null
    $output = @(& go build -o ($buildDir + [IO.Path]::DirectorySeparatorChar) ./cmd/... 2>&1)
    $output | Set-Content -LiteralPath (Join-Path $logDir ($Case + '-build.log'))
    if ($LASTEXITCODE -ne 0) { Finish-Failure ($Case + '_build_failed') }
    @{ ok = $true; scope = $definition.scope; passedTests = $passed; servicesValidated = $false } | ConvertTo-Json -Compress
} finally { Pop-Location }
