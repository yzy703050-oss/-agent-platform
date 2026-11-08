CREATE FUNCTION platform.create_session(s text,u text,token text,csrf text,e timestamptz) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 PERFORM control.register_session_guard(s,u,e);
 INSERT INTO platform.sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES(s,u,token,csrf,e);
END $$;

CREATE FUNCTION platform.revoke_session(token text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE sid text;
BEGIN
 SELECT id INTO sid FROM platform.sessions WHERE token_hash=token;
 IF sid IS NULL THEN RETURN; END IF;
 -- Guard first, business row second: the same order as Travel commits.
 PERFORM control.revoke_session_guard(sid);
 UPDATE platform.sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=sid;
END $$;
