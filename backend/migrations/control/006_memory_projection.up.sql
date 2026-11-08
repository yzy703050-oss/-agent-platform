CREATE FUNCTION control.read_related_memory(id text,h text,domain text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s control.execution_scopes;
BEGIN
 IF domain IS NULL OR domain='' OR length(domain)>160 THEN RAISE EXCEPTION 'memory domain denied' USING ERRCODE='42501'; END IF;
 s:=control.lock_scope(id,h,'memory','memory:'||domain,false);
 RETURN platform.private_memory_projection(to_jsonb(s),domain);
END $$;
