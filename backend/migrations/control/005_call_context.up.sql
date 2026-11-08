ALTER TABLE control.outbound_calls ADD COLUMN input_hash text NOT NULL DEFAULT '';
ALTER TABLE control.outbound_calls ADD COLUMN next_retry_at timestamptz;

-- Background execution limits become part of the original signed definition,
-- rather than a later mutable projection borrowed from a foreground Run.
CREATE OR REPLACE FUNCTION control.issue_scope(i jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE caller text:=control.caller_service(); s control.execution_scopes; d timestamptz; definition jsonb; limits jsonb;
BEGIN
 IF caller NOT IN('platform','maintenance') THEN RAISE EXCEPTION 'scope issuer denied' USING ERRCODE='42501'; END IF;
 s.scope_id:=i->>'scope_id'; s.issuer_service:=caller; s.owner_service:=i->>'owner_service';
 s.subject_id:=i->>'subject_id'; s.source_run_id:=NULLIF(i->>'source_run_id',''); s.conversation_id:=i->>'conversation_id';
 s.delegation_id:=NULLIF(i->>'delegation_id',''); s.operation_id:=NULLIF(i->>'operation_id','');
 s.capabilities:=i->'capabilities'; s.targets:=i->'targets'; s.source_scope:=i->'source_scope';
 s.context_epoch:=(i->>'context_epoch')::bigint; s.expires_at:=(i->>'expires_at')::timestamptz;
 s.budget_limits:=COALESCE(i->'budget_limits','{}'); s.created_at:=clock_timestamp();
 IF COALESCE(s.scope_id,'')='' OR COALESCE(s.subject_id,'')='' OR COALESCE(s.conversation_id,'')=''
 OR s.owner_service IS NULL OR s.owner_service NOT IN('platform','travel','maintenance')
 OR (s.source_run_id IS NULL)=(s.operation_id IS NULL)
 OR (caller='maintenance' AND (s.owner_service<>'maintenance' OR s.source_run_id IS NOT NULL))
 OR (s.operation_id IS NOT NULL AND (s.owner_service<>'maintenance' OR NOT control.valid_budget(s.budget_limits)))
 OR jsonb_typeof(s.capabilities) IS DISTINCT FROM 'array' OR jsonb_typeof(s.targets) IS DISTINCT FROM 'array'
 OR jsonb_typeof(s.source_scope) IS DISTINCT FROM 'object' OR s.context_epoch IS NULL OR s.context_epoch<1
 OR s.expires_at IS NULL OR NOT isfinite(s.expires_at) OR s.expires_at<=clock_timestamp() THEN
  RAISE EXCEPTION 'invalid scope definition' USING ERRCODE='22023';
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(s.capabilities||s.targets) v WHERE jsonb_typeof(v)<>'string' OR v='""'::jsonb) THEN RAISE EXCEPTION 'invalid scope capability or target' USING ERRCODE='22023'; END IF;
 IF i ? 'execution_limits' THEN
  limits:=i->'execution_limits';
  IF s.operation_id IS NULL OR jsonb_typeof(limits) IS DISTINCT FROM 'object' OR COALESCE(i->>'config_fingerprint','')=''
  OR (limits->>'MaxModelAttempts')::numeric IS DISTINCT FROM (s.budget_limits->>'model_attempts')::numeric
  OR (limits->>'MaxToolAttempts')::numeric IS DISTINCT FROM (s.budget_limits->>'tool_attempts')::numeric
  OR (limits->>'TokenUpperBound')::numeric IS DISTINCT FROM (s.budget_limits->>'token_upper_bound')::numeric
  OR (limits->>'CostUpperMicros')::numeric IS DISTINCT FROM (s.budget_limits->>'cost_upper_micros')::numeric
  OR COALESCE((limits->>'MaxModelInputTokens')::numeric,0)<=0 OR COALESCE((limits->>'MaxModelOutputTokens')::numeric,0)<=0
  OR (limits->>'MaxModelInputTokens')::numeric+(limits->>'MaxModelOutputTokens')::numeric>(s.budget_limits->>'token_upper_bound')::numeric
  OR COALESCE((limits->>'ToolTimeout')::numeric,0)<=0 OR (limits->>'DeadlineAt')::timestamptz IS DISTINCT FROM s.expires_at
  THEN RAISE EXCEPTION 'invalid background execution limits' USING ERRCODE='22023'; END IF;
  s.budget_limits:=s.budget_limits||jsonb_build_object('execution',jsonb_build_object('limits',limits,'fingerprint',i->>'config_fingerprint'));
 END IF;
 d:=control.lock_authority(s.source_run_id,s.subject_id,s.conversation_id,s.context_epoch,false);
 IF s.source_run_id IS NOT NULL AND s.expires_at>(SELECT deadline_at FROM control.run_guards WHERE source_run_id=s.source_run_id) THEN RAISE EXCEPTION 'scope outlives run' USING ERRCODE='42501'; END IF;
 definition:=to_jsonb(s)-ARRAY['scope_hash','created_at','revoked_at'];
 s.scope_hash:=encode(sha256(convert_to(definition::text,'UTF8')),'hex');
 INSERT INTO control.execution_scopes SELECT s.*;
 RETURN to_jsonb(s);
END $$;

CREATE FUNCTION control.evidence_call_context(id text,h text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE target text;
BEGIN
 SELECT targets->>0 INTO target FROM control.execution_scopes WHERE scope_id=id;
 RETURN control.scope_call_context(id,h,'evidence',target);
END $$;

CREATE FUNCTION control.peer_evidence_context(id text,h text,peer text,origin_id text,rev bigint,hash text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.execution_scopes; caller text:=control.caller_service(); grant_entry jsonb;
BEGIN
 IF caller IS NULL OR peer IS NULL OR caller NOT IN('platform','travel') OR peer NOT IN('platform','travel') OR peer=caller THEN RAISE EXCEPTION 'peer service denied' USING ERRCODE='42501'; END IF;
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=id;
 IF NOT FOUND OR s.owner_service<>peer OR s.scope_hash IS DISTINCT FROM h THEN RAISE EXCEPTION 'peer scope denied' USING ERRCODE='42501'; END IF;
 PERFORM control.lock_authority(s.source_run_id,s.subject_id,s.conversation_id,s.context_epoch,false);
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=id FOR SHARE;
 grant_entry:=jsonb_build_object('origin_service',caller,'origin_id',origin_id,'origin_revision',rev,'content_hash',hash);
 IF s.revoked_at IS NOT NULL OR control.authority_deadline(s)<=clock_timestamp() OR NOT s.capabilities ? 'evidence'
 OR origin_id IS NULL OR rev IS NULL OR rev<1 OR hash IS NULL OR hash!~'^[a-f0-9]{64}$'
 OR jsonb_typeof(s.source_scope->'evidence') IS DISTINCT FROM 'array'
 OR NOT (s.source_scope->'evidence' @> jsonb_build_array(grant_entry)) THEN RAISE EXCEPTION 'evidence manifest denied' USING ERRCODE='42501'; END IF;
 RETURN to_jsonb(s);
END $$;

CREATE FUNCTION control.scope_call_context(id text,h text,cap text,target text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.execution_scopes; b jsonb;
BEGIN
 s:=control.lock_scope(id,h,cap,target,true);
 IF s.source_run_id IS NOT NULL THEN SELECT budget_limits INTO b FROM control.run_guards WHERE source_run_id=s.source_run_id;
 ELSE b:=s.budget_limits; END IF;
 RETURN jsonb_build_object('scope',to_jsonb(s),'execution',b->'execution');
END $$;

CREATE FUNCTION control.attach_run_execution(id text,h text,limits jsonb,fingerprint text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.execution_scopes; r control.run_guards; v jsonb;
BEGIN
 PERFORM control.require_platform();
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=id;
 IF NOT FOUND OR s.owner_service<>'platform' OR s.scope_hash IS DISTINCT FROM h OR s.source_run_id IS NULL THEN RAISE EXCEPTION 'execution limits denied' USING ERRCODE='42501'; END IF;
 PERFORM control.lock_authority(s.source_run_id,s.subject_id,s.conversation_id,s.context_epoch,true);
 SELECT * INTO r FROM control.run_guards WHERE source_run_id=s.source_run_id;
 IF jsonb_typeof(limits) IS DISTINCT FROM 'object' OR COALESCE(fingerprint,'')='' OR
 (limits->>'MaxModelAttempts')::numeric IS DISTINCT FROM (r.budget_limits->>'model_attempts')::numeric OR
 (limits->>'MaxToolAttempts')::numeric IS DISTINCT FROM (r.budget_limits->>'tool_attempts')::numeric OR
 (limits->>'TokenUpperBound')::numeric IS DISTINCT FROM (r.budget_limits->>'token_upper_bound')::numeric OR
 (limits->>'CostUpperMicros')::numeric IS DISTINCT FROM (r.budget_limits->>'cost_upper_micros')::numeric OR
 (limits->>'MaxModelInputTokens')::numeric IS NULL OR (limits->>'MaxModelInputTokens')::numeric<=0 OR
 (limits->>'MaxModelOutputTokens')::numeric IS NULL OR (limits->>'MaxModelOutputTokens')::numeric<=0 OR
 (limits->>'MaxModelInputTokens')::numeric+(limits->>'MaxModelOutputTokens')::numeric>(r.budget_limits->>'token_upper_bound')::numeric OR
 (limits->>'ToolTimeout')::numeric IS NULL OR (limits->>'ToolTimeout')::numeric<=0 OR
 (limits->>'DeadlineAt')::timestamptz IS DISTINCT FROM r.deadline_at THEN RAISE EXCEPTION 'invalid execution limits' USING ERRCODE='22023'; END IF;
 v:=jsonb_build_object('limits',limits,'fingerprint',fingerprint);
 IF r.budget_limits ? 'execution' AND r.budget_limits->'execution'<>v THEN RAISE EXCEPTION 'execution limits immutable' USING ERRCODE='23505'; END IF;
 UPDATE control.run_guards SET budget_limits=jsonb_set(budget_limits,'{execution}',v) WHERE source_run_id=s.source_run_id AND NOT budget_limits ? 'execution';
END $$;

CREATE FUNCTION control.call_history(id text,h text,key text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.execution_scopes; result jsonb;
BEGIN
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=id;
 IF NOT FOUND OR s.owner_service<>control.caller_service() OR s.scope_hash IS DISTINCT FROM h THEN RAISE EXCEPTION 'call history denied' USING ERRCODE='42501'; END IF;
 SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY attempt_no),'[]'::jsonb) INTO result FROM control.outbound_calls c WHERE c.scope_id=id AND c.logical_call_key=key;
 RETURN result;
END $$;

CREATE FUNCTION control.bind_attempt_input(aid text,hash text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE a control.outbound_calls; s control.execution_scopes;
BEGIN
 SELECT * INTO a FROM control.outbound_calls WHERE attempt_id=aid;
 IF NOT FOUND OR a.owner_service<>control.caller_service() OR hash IS NULL OR hash!~'^[a-f0-9]{64}$' THEN RAISE EXCEPTION 'attempt input denied' USING ERRCODE='42501'; END IF;
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=a.scope_id;
 IF s.source_run_id IS NOT NULL THEN PERFORM 1 FROM control.run_guards WHERE source_run_id=s.source_run_id FOR UPDATE;
 ELSE PERFORM 1 FROM control.execution_scopes WHERE scope_id=s.scope_id FOR UPDATE; END IF;
 IF EXISTS(SELECT 1 FROM control.outbound_calls WHERE scope_id=a.scope_id AND logical_call_key=a.logical_call_key AND input_hash<>'' AND input_hash<>hash) THEN RAISE EXCEPTION 'call input changed' USING ERRCODE='23505'; END IF;
 UPDATE control.outbound_calls SET input_hash=hash WHERE attempt_id=aid AND input_hash='';
END $$;

CREATE FUNCTION control.schedule_call_retry(aid text,at_time timestamptz) RETURNS timestamptz
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE a control.outbound_calls; s control.execution_scopes; result timestamptz;
BEGIN
 SELECT * INTO a FROM control.outbound_calls WHERE attempt_id=aid;
 IF NOT FOUND OR a.owner_service<>control.caller_service() OR at_time IS NULL OR NOT isfinite(at_time) THEN RAISE EXCEPTION 'retry denied' USING ERRCODE='42501'; END IF;
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=a.scope_id;
 IF s.source_run_id IS NOT NULL THEN PERFORM 1 FROM control.run_guards WHERE source_run_id=s.source_run_id FOR UPDATE;
 ELSE PERFORM 1 FROM control.execution_scopes WHERE scope_id=s.scope_id FOR UPDATE; END IF;
 IF a.attempt_no>=4 THEN RAISE EXCEPTION 'retry exhausted' USING ERRCODE='22023'; END IF;
 UPDATE control.outbound_calls SET next_retry_at=COALESCE(next_retry_at,at_time) WHERE attempt_id=aid RETURNING next_retry_at INTO result;
 RETURN result;
END $$;

CREATE FUNCTION control.own_attempt(aid text,sid text,h text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE a control.outbound_calls;
BEGIN
 SELECT c.* INTO a FROM control.outbound_calls c JOIN control.execution_scopes s USING(scope_id)
 WHERE c.attempt_id=aid AND c.scope_id=sid AND s.scope_hash=h AND c.owner_service=control.caller_service();
 IF NOT FOUND THEN RAISE EXCEPTION 'original attempt denied' USING ERRCODE='42501'; END IF;
 RETURN to_jsonb(a);
END $$;

CREATE FUNCTION control.model_call_context(id text,h text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE target text;
BEGIN
 SELECT targets->>0 INTO target FROM control.execution_scopes WHERE scope_id=id;
 RETURN control.scope_call_context(id,h,'model',target);
END $$;

-- The composite row acquired new fields; initialize them explicitly instead
-- of relying on column defaults, which INSERT ... SELECT a.* bypasses.
CREATE OR REPLACE FUNCTION control.reserve_attempt(i jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.execution_scopes; a control.outbound_calls; old control.outbound_calls;
 limits jsonb; key text; resource control.resource_limits; dimension text; spent numeric; wanted numeric;
BEGIN
 -- First authority acquisition uses UPDATE for the budget guard, never an
 -- assertion SHARE followed by an upgrade in the same admission transaction.
 s:=control.lock_scope(i->>'scope_id',i->>'scope_hash',i->>'capability',i->>'target',true);
 a.attempt_id:=i->>'attempt_id'; a.scope_id:=s.scope_id; a.owner_service:=s.owner_service;
 a.logical_call_key:=i->>'logical_call_key'; a.attempt_no:=(i->>'attempt_no')::integer;
 a.owner_job_id:=NULLIF(i->>'owner_job_id',''); a.owner_operation_id:=NULLIF(i->>'owner_operation_id','');
 a.lease_epoch:=(i->>'owner_epoch')::bigint; a.reservation:=i->'reservation';
 a.hold_until:=(i->>'hold_until')::timestamptz; a.status:='reserved'; a.created_at:=clock_timestamp();
 IF jsonb_typeof(i->'resource_keys') IS DISTINCT FROM 'array' OR jsonb_array_length(i->'resource_keys')=0
 OR EXISTS(SELECT 1 FROM jsonb_array_elements(i->'resource_keys') v WHERE jsonb_typeof(v)<>'string' OR v='""'::jsonb)
 OR COALESCE(a.attempt_id,'')='' OR COALESCE(a.logical_call_key,'')=''
 OR a.attempt_no IS NULL OR a.attempt_no NOT BETWEEN 1 AND 4 OR a.lease_epoch IS NULL OR a.lease_epoch<1
 OR a.hold_until IS NULL OR NOT isfinite(a.hold_until) OR a.hold_until<=clock_timestamp()
 OR (a.owner_job_id IS NULL)=(a.owner_operation_id IS NULL)
 OR (s.operation_id IS NOT NULL AND a.owner_operation_id IS DISTINCT FROM s.operation_id)
 OR (s.operation_id IS NULL AND a.owner_job_id IS NULL)
 OR NOT control.valid_budget(a.reservation) THEN RAISE EXCEPTION 'invalid attempt' USING ERRCODE='22023'; END IF;
 IF (i->>'capability'='model' AND ((a.reservation->>'model_attempts')::numeric<>1 OR (a.reservation->>'tool_attempts')::numeric<>0 OR (a.reservation->>'token_upper_bound')::numeric<=0))
 OR (i->>'capability'='tool' AND ((a.reservation->>'model_attempts')::numeric<>0 OR (a.reservation->>'tool_attempts')::numeric<>1))
 OR (i->>'capability'='control_rpc' AND a.reservation<>'{"model_attempts":0,"tool_attempts":0,"token_upper_bound":0,"cost_upper_micros":0}'::jsonb)
 OR i->>'capability' NOT IN('model','tool','control_rpc') THEN RAISE EXCEPTION 'invalid attempt class' USING ERRCODE='22023'; END IF;
 SELECT jsonb_agg(v ORDER BY v) INTO a.resource_keys FROM (SELECT DISTINCT v FROM jsonb_array_elements_text(i->'resource_keys') v) keys;
 FOR key IN SELECT jsonb_array_elements_text(a.resource_keys) LOOP
  SELECT * INTO resource FROM control.resource_limits WHERE resource_key=key FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'resource not configured' USING ERRCODE='42501'; END IF;
 END LOOP;
 IF control.authority_deadline(s)<=clock_timestamp() OR a.hold_until<=clock_timestamp() THEN
  RAISE EXCEPTION 'attempt authority expired during admission' USING ERRCODE='42501';
 END IF;
 SELECT * INTO old FROM control.outbound_calls WHERE scope_id=s.scope_id AND logical_call_key=a.logical_call_key AND attempt_no=a.attempt_no;
 IF FOUND THEN
  IF old.attempt_id<>a.attempt_id OR old.owner_job_id IS DISTINCT FROM a.owner_job_id OR old.owner_operation_id IS DISTINCT FROM a.owner_operation_id
  OR old.lease_epoch<>a.lease_epoch OR old.reservation<>a.reservation OR old.resource_keys<>a.resource_keys THEN
   RAISE EXCEPTION 'attempt identity conflict' USING ERRCODE='23505';
  END IF;
  RETURN to_jsonb(old)||'{"replayed":true}'::jsonb;
 END IF;
 IF s.source_run_id IS NOT NULL THEN
  SELECT budget_limits INTO limits FROM control.run_guards WHERE source_run_id=s.source_run_id;
 ELSE limits:=s.budget_limits; END IF;
 IF NOT control.valid_budget(limits) THEN RAISE EXCEPTION 'finite budget required' USING ERRCODE='42501'; END IF;
 FOREACH dimension IN ARRAY ARRAY['model_attempts','tool_attempts','token_upper_bound','cost_upper_micros'] LOOP
  SELECT COALESCE(sum(CASE WHEN oc.status='not_sent' THEN 0
    WHEN dimension IN('model_attempts','tool_attempts') THEN (oc.reservation->>dimension)::numeric
    ELSE COALESCE((oc.usage->>dimension)::numeric,(oc.reservation->>dimension)::numeric) END),0)
  INTO spent FROM control.outbound_calls oc JOIN control.execution_scopes sc USING(scope_id)
  WHERE (s.source_run_id IS NOT NULL AND sc.source_run_id=s.source_run_id)
     OR (s.operation_id IS NOT NULL AND sc.operation_id=s.operation_id);
  wanted:=(a.reservation->>dimension)::numeric;
  IF spent+wanted>(limits->>dimension)::numeric THEN RAISE EXCEPTION 'cumulative budget exhausted' USING ERRCODE='P0001'; END IF;
 END LOOP;
 FOR key IN SELECT jsonb_array_elements_text(a.resource_keys) LOOP
  SELECT * INTO resource FROM control.resource_limits WHERE resource_key=key;
  IF (SELECT count(*) FROM control.outbound_calls oc WHERE oc.resource_keys ? key AND oc.hold_until>clock_timestamp()
      AND oc.status IN('reserved','running','unknown'))>=resource.max_concurrency THEN
   RAISE EXCEPTION 'resource concurrency exhausted' USING ERRCODE='P0001';
  END IF;
 END LOOP;
 a.input_hash:=''; a.next_retry_at:=NULL;
 INSERT INTO control.outbound_calls SELECT a.*;
 RETURN to_jsonb(a)||'{"replayed":false}'::jsonb;
END $$;
