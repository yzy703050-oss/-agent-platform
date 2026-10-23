$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
$configPath=Join-Path $projectRoot '.env'
$allowed=@('DB_DSN','HTTP_ADDR','PUBLIC_ORIGIN','COOKIE_SECURE','LLM_BASE_URL','LLM_MODEL','LLM_API_KEY')
if(Test-Path -LiteralPath $configPath){
 foreach($line in Get-Content -LiteralPath $configPath){
  if($line -match '^([A-Z_]+)=(.*)$' -and $allowed -contains $Matches[1]){
   [Environment]::SetEnvironmentVariable($Matches[1],$Matches[2],'Process')
  }
 }
}
$env:GOMODCACHE=Join-Path $projectRoot '.cache/go-mod'
$env:GOCACHE=Join-Path $projectRoot '.cache/go-build'
$env:GIN_MODE='release'
Push-Location (Join-Path $projectRoot 'backend')
try { & go run ./cmd/server; if($LASTEXITCODE -ne 0){throw 'API process exited with an error'} }
finally { Pop-Location }

