-- 002 was already applied locally. Keep its receipt immutable and extend the
-- original ledger for bounded control RPCs without provider-budget charges.
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
 INSERT INTO control.outbound_calls SELECT a.*;
 RETURN to_jsonb(a)||'{"replayed":false}'::jsonb;
END $$;
