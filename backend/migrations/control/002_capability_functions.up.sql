-- All helpers remain private. Public entry points are granted explicitly by the
-- component migrator; session_user survives every SECURITY DEFINER boundary.
CREATE FUNCTION control.caller_service() RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE result text;
BEGIN
 SELECT service_id INTO result FROM control.service_principals WHERE login_role=session_user;
 IF result IS NULL THEN RAISE EXCEPTION 'unregistered service' USING ERRCODE='42501'; END IF;
 RETURN result;
END $$;

-- Resource snapshots expose only permit identifiers and expiries, never tool
-- arguments or responses. Keep the row lock through the Redis repair and commit.
CREATE FUNCTION control.resource_snapshot(k text,expected_generation bigint) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE r control.resource_limits; holds jsonb;
BEGIN
 PERFORM control.caller_service();
 SELECT * INTO r FROM control.resource_limits WHERE resource_key=k FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'resource not configured' USING ERRCODE='42501'; END IF;
 IF expected_generation IS NOT NULL THEN
  IF r.redis_generation<>expected_generation THEN RAISE EXCEPTION 'resource generation changed' USING ERRCODE='40001'; END IF;
  UPDATE control.resource_limits SET redis_generation=redis_generation+1 WHERE resource_key=k RETURNING * INTO r;
 END IF;
 SELECT COALESCE(jsonb_agg(jsonb_build_object('attempt_id',attempt_id,'until_ms',floor(extract(epoch FROM hold_until)*1000))), '[]'::jsonb)
 INTO holds FROM control.outbound_calls WHERE resource_keys ? k AND hold_until>clock_timestamp() AND status IN('reserved','running','unknown');
 RETURN jsonb_build_object('generation',r.redis_generation,'limit',r.max_concurrency,'holds',holds);
END $$;

CREATE FUNCTION control.require_platform() RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF control.caller_service()<>'platform' THEN RAISE EXCEPTION 'platform required' USING ERRCODE='42501'; END IF;
END $$;

CREATE FUNCTION control.valid_budget(b jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE k text; v numeric;
BEGIN
 IF b IS NULL OR jsonb_typeof(b)<>'object' THEN RETURN false; END IF;
 FOREACH k IN ARRAY ARRAY['model_attempts','tool_attempts','token_upper_bound','cost_upper_micros'] LOOP
  IF jsonb_typeof(b->k) IS DISTINCT FROM 'number' THEN RETURN false; END IF;
  v:=(b->>k)::numeric;
  IF v<0 OR v<>trunc(v) OR v>9223372036854775807 THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;

CREATE FUNCTION control.register_session_guard(s text,u text,e timestamptz) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 PERFORM control.require_platform();
 IF s='' OR u='' OR e IS NULL OR NOT isfinite(e) OR e<=clock_timestamp() THEN RAISE EXCEPTION 'invalid session' USING ERRCODE='22023'; END IF;
 INSERT INTO control.session_guards(origin_session_id,subject_id,expires_at) VALUES(s,u,e);
END $$;

CREATE FUNCTION control.revoke_session_guard(s text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 PERFORM control.require_platform();
 UPDATE control.session_guards SET revoked_at=COALESCE(revoked_at,clock_timestamp()),version=version+1 WHERE origin_session_id=s;
 IF NOT FOUND THEN RAISE EXCEPTION 'session guard missing' USING ERRCODE='42501'; END IF;
END $$;

CREATE FUNCTION control.register_run_guard(r text,u text,c text,s text,d timestamptz,b jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE sg control.session_guards;
BEGIN
 PERFORM control.require_platform();
 SELECT * INTO sg FROM control.session_guards WHERE origin_session_id=s FOR SHARE;
 IF NOT FOUND OR sg.subject_id<>u OR sg.revoked_at IS NOT NULL OR sg.expires_at<=clock_timestamp()
 OR d IS NULL OR NOT isfinite(d) OR d<=clock_timestamp() OR NOT control.valid_budget(b) THEN
  RAISE EXCEPTION 'invalid run authority or budget' USING ERRCODE='42501';
 END IF;
 INSERT INTO control.run_guards(source_run_id,subject_id,conversation_id,origin_session_id,deadline_at,budget_limits) VALUES(r,u,c,s,d,b);
END $$;

CREATE FUNCTION control.close_run_guard(r text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE sid text;
BEGIN
 PERFORM control.require_platform();
 SELECT origin_session_id INTO sid FROM control.run_guards WHERE source_run_id=r;
 PERFORM 1 FROM control.session_guards WHERE origin_session_id=sid FOR SHARE;
 UPDATE control.run_guards SET active=false,version=version+1 WHERE source_run_id=r;
 IF NOT FOUND THEN RAISE EXCEPTION 'run guard missing' USING ERRCODE='42501'; END IF;
END $$;

CREATE FUNCTION control.set_context_guard(u text,c text,expected bigint,e bigint,a boolean) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 PERFORM control.require_platform();
 IF e<>expected+1 OR expected<0 THEN RAISE EXCEPTION 'invalid context epoch' USING ERRCODE='22023'; END IF;
 IF expected=0 THEN
  INSERT INTO control.context_guards VALUES(u,c,e,a);
 ELSE
  UPDATE control.context_guards SET context_epoch=e,active=a WHERE subject_id=u AND conversation_id=c AND context_epoch=expected;
  IF NOT FOUND THEN RAISE EXCEPTION 'context changed' USING ERRCODE='40001'; END IF;
 END IF;
END $$;

-- Lock authority in the same order for normal commits and budget admission.
CREATE FUNCTION control.lock_authority(r text,u text,c text,e bigint,budget boolean) RETURNS timestamptz
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE rg control.run_guards; sg control.session_guards; cg control.context_guards; deadline timestamptz;
BEGIN
 IF r IS NOT NULL THEN
  SELECT * INTO rg FROM control.run_guards WHERE source_run_id=r;
  IF NOT FOUND THEN RAISE EXCEPTION 'run missing' USING ERRCODE='42501'; END IF;
  SELECT * INTO sg FROM control.session_guards WHERE origin_session_id=rg.origin_session_id FOR SHARE;
  IF budget THEN
   SELECT * INTO rg FROM control.run_guards WHERE source_run_id=r FOR UPDATE;
  ELSE
   SELECT * INTO rg FROM control.run_guards WHERE source_run_id=r FOR SHARE;
  END IF;
  IF sg.subject_id<>u OR sg.revoked_at IS NOT NULL OR sg.expires_at<=clock_timestamp()
  OR rg.subject_id<>u OR rg.conversation_id<>c OR NOT rg.active OR rg.deadline_at<=clock_timestamp() THEN
   RAISE EXCEPTION 'run authority expired or revoked' USING ERRCODE='42501';
  END IF;
  deadline:=least(sg.expires_at,rg.deadline_at);
 END IF;
 SELECT * INTO cg FROM control.context_guards WHERE subject_id=u AND conversation_id=c FOR SHARE;
 IF NOT FOUND OR NOT cg.active OR cg.context_epoch<>e THEN RAISE EXCEPTION 'context unavailable' USING ERRCODE='42501'; END IF;
 RETURN deadline;
END $$;

CREATE FUNCTION control.issue_scope(i jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE caller text:=control.caller_service(); s control.execution_scopes; d timestamptz; definition jsonb;
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
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(s.capabilities||s.targets) v WHERE jsonb_typeof(v)<>'string' OR v='""'::jsonb) THEN
  RAISE EXCEPTION 'invalid scope capability or target' USING ERRCODE='22023';
 END IF;
 d:=control.lock_authority(s.source_run_id,s.subject_id,s.conversation_id,s.context_epoch,false);
 IF s.source_run_id IS NOT NULL AND s.expires_at>(SELECT deadline_at FROM control.run_guards WHERE source_run_id=s.source_run_id) THEN RAISE EXCEPTION 'scope outlives run' USING ERRCODE='42501'; END IF;
 definition:=to_jsonb(s)-ARRAY['scope_hash','created_at','revoked_at'];
 s.scope_hash:=encode(sha256(convert_to(definition::text,'UTF8')),'hex');
 INSERT INTO control.execution_scopes SELECT s.*;
 RETURN to_jsonb(s);
END $$;

CREATE FUNCTION control.authority_deadline(s control.execution_scopes) RETURNS timestamptz
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE d timestamptz;
BEGIN
 IF s.source_run_id IS NULL THEN RETURN s.expires_at; END IF;
 SELECT least(s.expires_at,r.deadline_at,g.expires_at) INTO d FROM control.run_guards r
 JOIN control.session_guards g ON g.origin_session_id=r.origin_session_id WHERE r.source_run_id=s.source_run_id;
 RETURN d;
END $$;

CREATE FUNCTION control.lock_scope(id text,h text,cap text,target text,budget boolean) RETURNS control.execution_scopes
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.execution_scopes; caller text:=control.caller_service();
BEGIN
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=id;
 IF NOT FOUND OR s.owner_service<>caller OR s.scope_hash IS DISTINCT FROM h THEN
  RAISE EXCEPTION 'scope denied' USING ERRCODE='42501';
 END IF;
 PERFORM control.lock_authority(s.source_run_id,s.subject_id,s.conversation_id,s.context_epoch,budget);
 IF budget AND s.operation_id IS NOT NULL THEN
  SELECT * INTO s FROM control.execution_scopes WHERE scope_id=id FOR UPDATE;
 ELSE
  SELECT * INTO s FROM control.execution_scopes WHERE scope_id=id FOR SHARE;
 END IF;
 IF cap IS NULL OR target IS NULL OR cap='' OR target='' OR s.revoked_at IS NOT NULL OR control.authority_deadline(s)<=clock_timestamp() OR NOT s.capabilities ? cap OR NOT s.targets ? target THEN
  RAISE EXCEPTION 'scope capability, target or deadline denied' USING ERRCODE='42501';
 END IF;
 RETURN s;
END $$;

CREATE FUNCTION control.assert_scope(id text,h text,cap text,target text,purpose text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF purpose NOT IN('read','commit','admit') OR purpose IS NULL THEN RAISE EXCEPTION 'invalid purpose' USING ERRCODE='22023'; END IF;
 RETURN to_jsonb(control.lock_scope(id,h,cap,target,false));
END $$;

CREATE UNIQUE INDEX one_background_scope ON control.execution_scopes(operation_id) WHERE operation_id IS NOT NULL;

CREATE FUNCTION control.reserve_attempt(i jsonb) RETURNS jsonb
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
 OR i->>'capability' NOT IN('model','tool') THEN RAISE EXCEPTION 'invalid attempt class' USING ERRCODE='22023'; END IF;
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

CREATE FUNCTION control.settle_attempt(i jsonb) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE a control.outbound_calls; s control.execution_scopes; v_status text:=i->>'status'; v_usage jsonb:=NULLIF(i->'usage','null'::jsonb); dimension text; key text;
BEGIN
 SELECT * INTO a FROM control.outbound_calls WHERE attempt_id=i->>'attempt_id';
 IF NOT FOUND OR a.owner_service<>control.caller_service() THEN RAISE EXCEPTION 'attempt denied' USING ERRCODE='42501'; END IF;
 SELECT * INTO s FROM control.execution_scopes WHERE scope_id=a.scope_id;
 -- Settlement intentionally does not require currently valid execution
 -- authority. It records the original fact, never grants a new commit.
 IF s.source_run_id IS NOT NULL THEN
  PERFORM 1 FROM control.run_guards WHERE source_run_id=s.source_run_id FOR UPDATE;
 ELSE PERFORM 1 FROM control.execution_scopes WHERE scope_id=s.scope_id FOR UPDATE; END IF;
 FOR key IN SELECT jsonb_array_elements_text(a.resource_keys) ORDER BY 1 LOOP
  PERFORM 1 FROM control.resource_limits WHERE resource_key=key FOR UPDATE;
 END LOOP;
 SELECT * INTO a FROM control.outbound_calls WHERE attempt_id=i->>'attempt_id' FOR UPDATE;
 IF v_status IS NULL OR v_status NOT IN('succeeded','failed','unknown','not_sent')
 OR (v_usage IS NOT NULL AND NOT control.valid_budget(v_usage)) OR (v_status IN('unknown','not_sent') AND v_usage IS NOT NULL) THEN
  RAISE EXCEPTION 'invalid settlement' USING ERRCODE='22023';
 END IF;
 IF v_usage IS NOT NULL THEN
  FOREACH dimension IN ARRAY ARRAY['model_attempts','tool_attempts','token_upper_bound','cost_upper_micros'] LOOP
   IF (v_usage->>dimension)::numeric>(a.reservation->>dimension)::numeric
   OR (dimension IN('model_attempts','tool_attempts') AND v_usage->dimension<>a.reservation->dimension) THEN
    RAISE EXCEPTION 'usage outside reservation' USING ERRCODE='22023';
   END IF;
  END LOOP;
 END IF;
 IF a.status=v_status AND a.usage IS NOT DISTINCT FROM v_usage AND a.response_ref IS NOT DISTINCT FROM NULLIF(i->'response_ref','null'::jsonb) THEN RETURN false; END IF;
 IF a.status IN('succeeded','failed','not_sent') THEN RAISE EXCEPTION 'settlement already final' USING ERRCODE='23505'; END IF;
 UPDATE control.outbound_calls SET status=v_status,usage=v_usage,response_ref=NULLIF(i->'response_ref','null'::jsonb),settled_at=clock_timestamp() WHERE attempt_id=a.attempt_id;
 RETURN true;
END $$;
