CREATE UNIQUE INDEX one_entry_scope_per_run ON control.execution_scopes(source_run_id)
 WHERE source_run_id IS NOT NULL AND owner_service='platform' AND capabilities ? 'execute' AND targets ? 'entry';

CREATE FUNCTION control.admission_authority(sid text,uid text,cid text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.session_guards; c control.context_guards; r control.resource_limits;
BEGIN
 PERFORM control.require_platform();
 SELECT * INTO s FROM control.session_guards WHERE origin_session_id=sid FOR SHARE;
 IF NOT FOUND OR s.subject_id<>uid THEN RAISE EXCEPTION 'Session denied' USING ERRCODE='42501'; END IF;
 SELECT * INTO c FROM control.context_guards WHERE subject_id=uid AND conversation_id=cid FOR SHARE;
 IF NOT FOUND OR NOT c.active THEN RAISE EXCEPTION 'context denied' USING ERRCODE='42501'; END IF;
 SELECT * INTO r FROM control.resource_limits WHERE resource_key='run:active_per_user' FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'active Run limit not configured' USING ERRCODE='P0001'; END IF;
 IF s.revoked_at IS NOT NULL OR s.expires_at<=clock_timestamp() THEN RAISE EXCEPTION 'Session expired or revoked' USING ERRCODE='42501'; END IF;
 RETURN jsonb_build_object('context_epoch',c.context_epoch,'max_active_runs',r.max_concurrency);
END $$;

-- Run transitions start with UPDATE, including normal successful completion.
-- They must never SHARE-lock authority and then upgrade while another finisher
-- holds the same SHARE lock.
CREATE FUNCTION control.assert_entry_control(id text,h text,cap text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 PERFORM control.require_platform();
 IF cap IS NULL OR cap NOT IN('execute','commit') THEN RAISE EXCEPTION 'entry control capability denied' USING ERRCODE='42501'; END IF;
 RETURN to_jsonb(control.lock_scope(id,h,cap,'entry',true));
END $$;

-- Only a real persisted invalid condition permits system closure. The caller
-- supplies a Run ID, never an asserted timeout, user, scope or failure reason.
CREATE FUNCTION control.close_invalid_run_guard(rid text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE r control.run_guards; s control.session_guards; c control.context_guards; scope control.execution_scopes;
 reason text:=''; caller text:=control.caller_service();
BEGIN
 IF caller NOT IN('platform','maintenance') THEN RAISE EXCEPTION 'cleanup denied' USING ERRCODE='42501'; END IF;
 SELECT * INTO r FROM control.run_guards WHERE source_run_id=rid;
 IF NOT FOUND THEN RAISE EXCEPTION 'Run guard missing' USING ERRCODE='42501'; END IF;
 SELECT * INTO s FROM control.session_guards WHERE origin_session_id=r.origin_session_id FOR SHARE;
 SELECT * INTO r FROM control.run_guards WHERE source_run_id=rid FOR UPDATE;
 SELECT * INTO c FROM control.context_guards WHERE subject_id=r.subject_id AND conversation_id=r.conversation_id FOR SHARE;
 SELECT * INTO scope FROM control.execution_scopes WHERE source_run_id=rid AND owner_service='platform' AND capabilities ? 'execute' AND targets ? 'entry' FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'entry scope missing' USING ERRCODE='42501'; END IF;
 IF r.deadline_at<=clock_timestamp() THEN reason:='deadline_exceeded';
 ELSIF s.revoked_at IS NOT NULL THEN reason:='auth_revoked';
 ELSIF s.expires_at<=clock_timestamp() THEN reason:='auth_expired';
 ELSIF NOT r.active THEN reason:='run_closed';
 ELSIF c.subject_id IS NULL OR NOT c.active OR c.context_epoch<>scope.context_epoch THEN reason:='context_invalidated';
 ELSIF scope.revoked_at IS NOT NULL THEN reason:='scope_revoked';
 ELSIF scope.expires_at<=clock_timestamp() THEN reason:='scope_expired'; END IF;
 IF reason<>'' AND r.active THEN UPDATE control.run_guards SET active=false,version=version+1 WHERE source_run_id=rid; END IF;
 RETURN jsonb_build_object('subject_id',r.subject_id,'conversation_id',r.conversation_id,'source_run_id',r.source_run_id,'context_epoch',scope.context_epoch,'reason',reason);
END $$;
