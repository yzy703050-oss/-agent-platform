$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
$localEnvPath=Join-Path $projectRoot '.env'
$allowed=@('DB_DSN','TEST_DATABASE_URL','HTTP_ADDR','PUBLIC_ORIGIN','COOKIE_SECURE','LLM_BASE_URL','LLM_MODEL','LLM_API_KEY','TRAIN_API_KEY','AMAP_API_KEY','TRAVEL_QUERY_MODE','RAG_MCP_COMMAND','RAG_MCP_DIR','RAG_COLLECTION','RAG_MCP_ARGS','RAG_SETTINGS_PATH','MAX_ITERATIONS','RUN_SECONDS','TOOL_SECONDS','HEARTBEAT_SECONDS','LEASE_SECONDS','PAUSE_SECONDS','TRAVEL_PLANNING_ROUNDS','TRAVEL_TURN_SECONDS','TRAVEL_CONTEXT_TOKENS')
if(Test-Path -LiteralPath $localEnvPath){
 foreach($line in Get-Content -LiteralPath $localEnvPath){
  if($line -match '^([A-Z_]+)=(.*)$' -and $allowed -contains $Matches[1]){
   $value=$Matches[2].Trim()
   if($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))){$value=$value.Substring(1,$value.Length-2)}
   [Environment]::SetEnvironmentVariable($Matches[1],$value,'Process')
  }
 }
}
$env:GOMODCACHE=Join-Path $projectRoot '.cache/go-mod'
$env:GOCACHE=Join-Path $projectRoot '.cache/go-build'
$env:GIN_MODE='release'
