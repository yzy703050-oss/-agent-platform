-- Only the control owner can invoke this private projection. Runtime callers
-- enter through control.read_related_memory and never gain platform access.
CREATE FUNCTION platform.private_memory_projection(scope jsonb,domain text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE snapshot jsonb; result jsonb;
BEGIN
 PERFORM 1 FROM platform.users WHERE id=scope->>'subject_id' FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'memory subject denied' USING ERRCODE='42501'; END IF;
 SELECT r.snapshot INTO snapshot FROM platform.user_memory_revisions r WHERE r.user_id=scope->>'subject_id' ORDER BY r.revision DESC LIMIT 1;
 SELECT COALESCE(jsonb_agg(m.value),'[]'::jsonb) INTO result
 FROM jsonb_array_elements(COALESCE(snapshot->'memories','[]'::jsonb)) m(value)
 WHERE m.value->>'domain'=domain AND m.value->>'status'='active'
 AND (scope->>'owner_service' IN('platform','maintenance') OR
  (jsonb_typeof(scope#>'{source_scope,memory_refs}')='array' AND
   scope#>'{source_scope,memory_refs}' @> jsonb_build_array(jsonb_build_object('memory_id',m.value->>'memory_id','revision',(m.value->>'revision')::bigint))));
 RETURN result;
END $$;
