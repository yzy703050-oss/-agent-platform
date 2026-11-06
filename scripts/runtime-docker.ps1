param(
    [ValidateSet('Prepare','Up','Status')][string]$Action = 'Prepare',
    [string]$StateDirectory
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path $PSScriptRoot
if (-not $StateDirectory) { $StateDirectory = Join-Path $repositoryRoot '.cache/runtime-v1' }
$StateDirectory = [IO.Path]::GetFullPath($StateDirectory)
$composeFile = Join-Path $repositoryRoot 'deploy/local/compose.runtime-v1.yaml'

function Fail([string]$Reason) {
    @{ ok=$false; reason=$Reason; servicesValidated=$false } | ConvertTo-Json -Compress
    exit 1
}
function New-Password {
    [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).TrimEnd('=').Replace('+','-').Replace('/','_')
}
function Protect-Credentials([string]$Path) {
    if ($IsWindows) {
        # Persist only the DACL. Set-Acl requests additional privileges when
        # rewriting an already protected file under a filtered Windows token.
        $acl = [Security.AccessControl.FileSecurity]::new()
        $acl.SetAccessRuleProtection($true,$false)
        $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl','Allow'))
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-18'),'FullControl','Allow'))
        [IO.FileSystemAclExtensions]::SetAccessControl([IO.FileInfo]::new($Path),$acl)
    } else {
        [IO.File]::SetUnixFileMode($Path,([IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite))
    }
}
function Invoke-Docker([string[]]$Arguments,[string]$LogName) {
    $output = @(& $script:dockerExecutable @script:dockerOptions @Arguments 2>&1)
    $code = $LASTEXITCODE
    $output | Set-Content -LiteralPath (Join-Path $StateDirectory $LogName) -Encoding utf8
    [pscustomobject]@{ Code=$code; Output=$output }
}
function Host-RebootPending {
    if (-not $IsWindows) { return $false }
    $resultPath = Join-Path $StateDirectory 'wsl-bootstrap-result.json'
    if (-not (Test-Path -LiteralPath $resultPath)) { return $false }
    $result = Get-Content -LiteralPath $resultPath -Raw | ConvertFrom-Json -AsHashtable
    $bootTime = (Get-CimInstance Win32_OperatingSystem).LastBootUpTime
    return $result.rebootPending -and $bootTime -lt (Get-Item -LiteralPath $resultPath).LastWriteTime
}

try {
    New-Item -ItemType Directory -Force -Path $StateDirectory | Out-Null
    $secretPath = Join-Path $StateDirectory 'docker-secrets.json'
    $credentials = if (Test-Path -LiteralPath $secretPath) {
        Get-Content -LiteralPath $secretPath -Raw | ConvertFrom-Json -AsHashtable
    } else {
        @{ compose_project=('xdb-runtime-'+[guid]::NewGuid().ToString('N').Substring(0,8)) }
    }
    if (-not ($credentials -is [System.Collections.IDictionary]) -or $credentials.compose_project -notmatch '^[a-z0-9][a-z0-9_-]{1,62}$') { Fail 'invalid_credential_file' }
    foreach ($key in @('postgres_password','redis_password','minio_password')) {
        if (-not $credentials.Contains($key)) { $credentials[$key] = New-Password }
        if ($credentials[$key] -notmatch '^[A-Za-z0-9_-]{32,}$') { Fail 'invalid_credential_file' }
    }
    $credentials | ConvertTo-Json | Set-Content -LiteralPath $secretPath -Encoding utf8
    Protect-Credentials $secretPath
    $env:XDB_RUNTIME_PROJECT = $credentials.compose_project
    $env:XDB_FIXTURE_PG_PASSWORD = $credentials.postgres_password
    $env:XDB_FIXTURE_REDIS_PASSWORD = $credentials.redis_password
    $env:XDB_FIXTURE_MINIO_PASSWORD = $credentials.minio_password

    $profilePath = Join-Path $StateDirectory 'profile.json'
    if (-not (Test-Path -LiteralPath $profilePath)) {
        $profile = Get-Content -LiteralPath (Join-Path $repositoryRoot 'deploy/local/runtime-v1.example.json') -Raw | ConvertFrom-Json -AsHashtable
        $profile.mode = 'docker'
        $profile.compose_project = $credentials.compose_project
        $profile | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $profilePath -Encoding utf8
    }
    $cliDirectory = Join-Path $StateDirectory 'docker-cli'
    New-Item -ItemType Directory -Force -Path $cliDirectory | Out-Null
    $pluginDirectory = if ($IsWindows) { 'C:/Program Files/Docker/Docker/resources/cli-plugins' } else { $null }
    @{ cliPluginsExtraDirs = @($pluginDirectory | Where-Object { $_ -and (Test-Path -LiteralPath $_) }) } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $cliDirectory 'config.json') -Encoding utf8
    $dockerCommand = Get-Command docker -ErrorAction SilentlyContinue
    $script:dockerExecutable = if ($dockerCommand) { $dockerCommand.Source } elseif ($IsWindows -and (Test-Path -LiteralPath 'C:/Program Files/Docker/Docker/resources/bin/docker.exe')) { 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' } else { $null }
    $script:dockerOptions = @('--config',$cliDirectory)
    if ($IsWindows) { $script:dockerOptions += @('--host','npipe:////./pipe/dockerDesktopLinuxEngine') }
    $composeValidated = $false
    if ($script:dockerExecutable) {
        $validation = Invoke-Docker @('compose','-f',$composeFile,'config','--quiet') 'compose-config.log'
        if ($validation.Code -ne 0) { Fail 'compose_configuration_invalid' }
        $composeValidated = $true
    }
    if ($Action -eq 'Prepare') {
        @{ ok=$true; state='prepared'; profile=$profilePath; composeProject=$credentials.compose_project; composeConfigValidated=$composeValidated; servicesValidated=$false } | ConvertTo-Json -Compress
        exit 0
    }
    if (-not $script:dockerExecutable) { Fail 'docker_cli_missing' }
    $engine = Invoke-Docker @('info','--format','{{.OSType}}') 'docker-engine.log'
    if ($engine.Code -ne 0 -and (Host-RebootPending)) { Fail 'windows_reboot_required_for_wsl' }
    if ($engine.Code -ne 0 -and $Action -eq 'Up' -and $IsWindows) {
        $desktopPath = 'C:/Program Files/Docker/Docker/Docker Desktop.exe'
        if (-not (Get-Process -Name 'Docker Desktop' -ErrorAction SilentlyContinue) -and (Test-Path -LiteralPath $desktopPath)) {
            Start-Process -FilePath $desktopPath -WindowStyle Hidden | Out-Null
        }
        for ($attempt=0;$attempt -lt 30 -and $engine.Code -ne 0;$attempt++) {
            Start-Sleep -Seconds 2
            $engine = Invoke-Docker @('info','--format','{{.OSType}}') 'docker-engine.log'
        }
    }
    if ($engine.Code -ne 0) { Fail 'docker_engine_not_ready' }
    if (($engine.Output -join '').Trim() -ne 'linux') { Fail 'linux_container_engine_required' }
    if ($Action -eq 'Up') {
        $up = Invoke-Docker @('compose','-f',$composeFile,'up','-d','--wait','--wait-timeout','180') 'compose-up.log'
        if ($up.Code -ne 0) { Fail 'containers_not_healthy_see_compose_up_log' }
    }
    $status = Invoke-Docker @('compose','-f',$composeFile,'ps','--format','json') 'compose-status.log'
    if ($status.Code -ne 0) { Fail 'container_status_unavailable' }
    $rawStatus = ($status.Output -join "`n").Trim()
    $rows = if ($rawStatus.StartsWith('[')) { @($rawStatus | ConvertFrom-Json) } else { @($status.Output | Where-Object { $_.ToString().Trim() } | ForEach-Object { $_.ToString() | ConvertFrom-Json }) }
    $checks = @()
    foreach ($service in @('postgres','redis','kafka','etcd','minio','milvus')) {
        $row = @($rows | Where-Object { $_.Service -eq $service })
        $healthy = $row.Count -eq 1 -and $row[0].State -eq 'running' -and $row[0].Health -eq 'healthy'
        $checks += @{ service=$service; healthy=$healthy }
    }
    $allHealthy = @($checks | Where-Object { -not $_.healthy }).Count -eq 0
    @{ ok=$allHealthy; scope='docker_dependency_health_only'; composeProject=$credentials.compose_project; servicesValidated=$allHealthy; checks=$checks } | ConvertTo-Json -Depth 5 -Compress
    if (-not $allHealthy) { exit 1 }
} catch {
    @{ exceptionType=$_.Exception.GetType().FullName; scriptLine=$_.InvocationInfo.ScriptLineNumber } | ConvertTo-Json -Compress | Set-Content -LiteralPath (Join-Path $StateDirectory 'docker-setup-error.json') -Encoding utf8
    Fail 'docker_setup_failed'
}
