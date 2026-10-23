param([string]$BaseURL='http://127.0.0.1:8080',[switch]$ExerciseRecovery)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'load-env.ps1')
foreach($name in @('LLM_BASE_URL','LLM_MODEL','LLM_API_KEY')){if([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name,'Process'))){throw "Missing $name. Configure root .env locally; never send the key to chat."}}
$webSession=[Microsoft.PowerShell.Commands.WebRequestSession]::new()
$csrf=''
function Invoke-Platform([string]$Method,[string]$Path,$Body=$null,$Page=$null,[string]$Key=''){
 $headers=@{};if($csrf){$headers['X-CSRF-Token']=$csrf};if($Page){$headers['X-Page-Token']=$Page.page_token};if($Key){$headers['Idempotency-Key']=$Key}
 $params=@{Uri=($BaseURL+'/api/v1'+$Path);Method=$Method;WebSession=$webSession;Headers=$headers;SkipHttpErrorCheck=$true;StatusCodeVariable='httpStatus'}
 if($null -ne $Body){$params.ContentType='application/json';$params.Body=($Body|ConvertTo-Json -Depth 6 -Compress)}
 $result=Invoke-RestMethod @params
 if($httpStatus -ge 400){$code=$result.code;if($code -notmatch '^[a-z_]+$'){$code='request_failed'};throw "Platform request failed: $httpStatus $code"}
 return $result
}
$account='smoke-'+[guid]::NewGuid().ToString('N').Substring(0,18)
$password=[Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$credentials=@{username=$account;password=$password}
Invoke-Platform 'POST' '/auth/register' $credentials | Out-Null
$login=Invoke-Platform 'POST' '/auth/login' $credentials
$csrf=$login.csrf_token
$page=$null
try{
 $agents=Invoke-Platform 'GET' '/agents'
 if(-not ($agents.items|Where-Object {$_.id -eq 'general' -and $_.available})){throw 'Model is not configured in the running API. Restart the backend after editing .env.'}
 $page=Invoke-Platform 'POST' '/pages' @{}
 Invoke-Platform 'PATCH' '/settings' @{theme='light';reply_preference='请简洁回复。'} | Out-Null
 $conversation=Invoke-Platform 'POST' '/conversations' @{agent_type='general';title='真实模型工具验收'}
 $run=Invoke-Platform 'POST' ('/conversations/'+$conversation.id+'/runs') @{content='请务必调用 get_my_settings 工具读取我的设置，再用一句话告诉我主题和回复偏好。';page_id=$page.page_id} $page ([guid]::NewGuid().ToString())
 $timer=[Diagnostics.Stopwatch]::StartNew();$lastHeartbeat=0
 do{
  Start-Sleep -Milliseconds 300
  if($timer.Elapsed.TotalSeconds-$lastHeartbeat -ge 5){Invoke-Platform 'POST' ('/pages/'+$page.page_id+'/heartbeat') @{} $page|Out-Null;$lastHeartbeat=$timer.Elapsed.TotalSeconds}
  $state=Invoke-Platform 'GET' ('/runs/'+$run.id)
 }while($state.status -in @('accepted','running','pausing','resuming') -and $timer.Elapsed.TotalSeconds -lt 150)
 if($state.status -ne 'succeeded' -or [string]::IsNullOrWhiteSpace($state.partial_output)){throw 'Real model run did not succeed; inspect the sanitized run status in the workspace.'}
 $history=Invoke-Platform 'GET' ('/conversations/'+$conversation.id+'/messages?limit=100')
 $toolResults=@($history.items|Where-Object {$_.role -eq 'tool'})
 if($toolResults.Count -lt 1){throw 'The model answered without exercising the tool. Real tool smoke is not verified.'}
 $recoveryVerified=$false
 if($ExerciseRecovery){
  $conversation=Invoke-Platform 'POST' '/conversations' @{agent_type='general';title='真实模型恢复验收'}
  # Pause while the first short model/tool step is in flight. A long final
  # answer may require forced stream cancellation and is deliberately unsafe
  # to resume; it is not a suitable successful-checkpoint smoke boundary.
  $run=Invoke-Platform 'POST' ('/conversations/'+$conversation.id+'/runs') @{content='请按顺序调用 get_my_settings 三次，每次收到上一次工具结果后再发起下一次调用，不要并行调用。最后用一句话说明主题和回复偏好。';page_id=$page.page_id} $page ([guid]::NewGuid().ToString())
  $timer.Restart();do{Start-Sleep -Milliseconds 100;$state=Invoke-Platform 'GET' ('/runs/'+$run.id)}while($state.status -eq 'accepted' -and $timer.Elapsed.TotalSeconds -lt 6)
  if($state.status -ne 'running'){throw 'The run ended before the pause boundary. Recovery smoke is not verified.'}
  $paused=Invoke-Platform 'POST' ('/runs/'+$run.id+'/pause') @{}
  if($paused.status -ne 'paused'){throw 'Eino did not produce a valid resumable checkpoint. Recovery smoke is not verified.'}
  $reopened=Invoke-Platform 'GET' ('/runs/'+$run.id)
  if($reopened.status -ne 'paused'){throw 'Reading state unexpectedly changed the paused run.'}
  Invoke-Platform 'POST' ('/pages/'+$page.page_id+'/leave') @{} $page|Out-Null
  $page=Invoke-Platform 'POST' '/pages' @{}
  $resumed=Invoke-Platform 'POST' ('/runs/'+$run.id+'/resume') @{page_id=$page.page_id} $page
  if($resumed.attempt -ne 2 -or $resumed.page_id -ne $page.page_id){throw 'Resume did not retain the run and transfer page ownership.'}
  $timer.Restart();$lastHeartbeat=0
  do{Start-Sleep -Milliseconds 300;if($timer.Elapsed.TotalSeconds-$lastHeartbeat -ge 5){Invoke-Platform 'POST' ('/pages/'+$page.page_id+'/heartbeat') @{} $page|Out-Null;$lastHeartbeat=$timer.Elapsed.TotalSeconds};$state=Invoke-Platform 'GET' ('/runs/'+$run.id)}while($state.status -in @('running','resuming') -and $timer.Elapsed.TotalSeconds -lt 150)
  if($state.status -ne 'succeeded'){throw 'The resumed real model run did not complete.'}
  $recoveryVerified=$true
 }
 $reportDir=Join-Path $projectRoot '.cache/reports';New-Item -ItemType Directory -Path $reportDir -Force|Out-Null
 @{utc=[DateTime]::UtcNow.ToString('o');real_model_chat=$true;tool_results=$toolResults.Count;recovery_verified=$recoveryVerified}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $reportDir 'model-smoke.json')
 Write-Output 'Real model chat and scoped tool smoke: PASS'
 Write-Output ('Real model recovery exercised: '+$recoveryVerified)
}finally{
 if($page){try{Invoke-Platform 'POST' ('/pages/'+$page.page_id+'/leave') @{} $page|Out-Null}catch{}}
 try{Invoke-Platform 'POST' '/auth/logout' @{}|Out-Null}catch{}
 $password=$null;$credentials=$null;$login=$null;$csrf=''
}
