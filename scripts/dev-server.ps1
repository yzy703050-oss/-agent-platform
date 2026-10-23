$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'load-env.ps1')
Push-Location (Join-Path $projectRoot 'backend')
try { & go run ./cmd/server; if($LASTEXITCODE -ne 0){throw 'API process exited with an error'} }
finally { Pop-Location }

