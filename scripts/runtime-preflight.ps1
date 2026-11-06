param([Parameter(Mandatory)][Alias('Profile')][string]$RuntimeProfilePath)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$checks = [System.Collections.Generic.List[object]]::new()
function Add-Check([string]$Name, [bool]$Passed, [string]$State) {
    $checks.Add(@{ name = $Name; ok = $Passed; state = $State })
}
try {
    $settings = Get-Content -LiteralPath $RuntimeProfilePath -Raw | ConvertFrom-Json -AsHashtable
    if ($settings.version -ne 1 -or $settings.isolated -ne $true) { throw 'invalid_profile' }
    foreach ($tool in @('go', 'python')) {
        Add-Check $tool ([bool](Get-Command $tool -ErrorAction SilentlyContinue)) 'executable_only'
    }
    foreach ($service in @('postgres', 'redis', 'kafka', 'milvus')) {
        $endpoint = $settings.services[$service]
        if (-not $endpoint -or -not $endpoint.host -or $endpoint.port -lt 1 -or $endpoint.port -gt 65535) {
            Add-Check $service $false 'endpoint_missing'
            continue
        }
        $client = [Net.Sockets.TcpClient]::new()
        try {
            $pending = $client.ConnectAsync([string]$endpoint.host, [int]$endpoint.port)
            if (-not $pending.Wait(1500)) { throw 'connect_timeout' }
            Add-Check $service $client.Connected 'tcp_only_not_authenticated'
        } catch { Add-Check $service $false 'unreachable' }
        finally { $client.Dispose() }
    }
    foreach ($model in @('embedding', 'reranker')) {
        $entry = $settings.models[$model]
        $path = if ($entry -and $entry.pathEnv) { [Environment]::GetEnvironmentVariable([string]$entry.pathEnv) } else { $null }
        $present = [bool]($path -and (Test-Path -LiteralPath $path -PathType Container))
        Add-Check $model $present 'directory_only_not_loaded'
    }
    $ok = @($checks | Where-Object { -not $_.ok }).Count -eq 0
    @{ ok = $ok; scope = 'preflight_only'; checks = @($checks.ToArray()); reason = 'protocol_auth_models_and_roles_require_integration_tests' } | ConvertTo-Json -Depth 5 -Compress
    if (-not $ok) { exit 1 }
} catch {
    @{ ok = $false; scope = 'preflight_only'; checks = @($checks.ToArray()); reason = 'invalid_or_incomplete_profile' } | ConvertTo-Json -Depth 5 -Compress
    exit 1
}
