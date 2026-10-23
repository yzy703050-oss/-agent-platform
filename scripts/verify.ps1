param([switch]$SkipNpmInstall)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'load-env.ps1')
if([string]::IsNullOrWhiteSpace($env:TEST_DATABASE_URL)){throw 'TEST_DATABASE_URL is required. Release verification cannot skip PostgreSQL.'}
$env:REQUIRE_DATABASE_TESTS='1'
$reportDir=Join-Path $projectRoot '.cache/reports'
New-Item -ItemType Directory -Path $reportDir -Force | Out-Null
Push-Location (Join-Path $projectRoot 'backend')
try{
 & go test ./... -v -count=1 -timeout 120s *> (Join-Path $reportDir 'backend-tests.log');if($LASTEXITCODE -ne 0){throw 'Backend tests failed; see .cache/reports/backend-tests.log'}
 & go vet ./... *> (Join-Path $reportDir 'backend-vet.log');if($LASTEXITCODE -ne 0){throw 'Go vet failed; see .cache/reports/backend-vet.log'}
 & go build -o (Join-Path $projectRoot '.cache/bin/server-verified.exe') ./cmd/server;if($LASTEXITCODE -ne 0){throw 'Backend build failed'}
}finally{Pop-Location}
Write-Output 'Backend tests, real PostgreSQL, vet and build: PASS'
Push-Location (Join-Path $projectRoot 'frontend')
try{
 if(-not $SkipNpmInstall){& npm ci *> (Join-Path $reportDir 'npm-ci.log');if($LASTEXITCODE -ne 0){throw 'npm ci failed. On Windows, stop Vite to release native module locks; see .cache/reports/npm-ci.log'}}
 & npm run test -- --run *> (Join-Path $reportDir 'frontend-tests.log');if($LASTEXITCODE -ne 0){throw 'Frontend tests failed; see .cache/reports/frontend-tests.log'}
 & npm run build *> (Join-Path $reportDir 'frontend-build.log');if($LASTEXITCODE -ne 0){throw 'Frontend build failed; see .cache/reports/frontend-build.log'}
}finally{Pop-Location}
Write-Output 'Frontend tests, TypeScript and build: PASS'
Write-Output 'Real model smoke and browser recovery must be verified separately; see docs/runbook.md.'
