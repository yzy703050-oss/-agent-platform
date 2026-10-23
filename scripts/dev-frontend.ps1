$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
Push-Location (Join-Path $projectRoot 'frontend')
try{& npm run dev -- --strictPort;if($LASTEXITCODE -ne 0){throw 'Frontend process exited with an error'}}finally{Pop-Location}
