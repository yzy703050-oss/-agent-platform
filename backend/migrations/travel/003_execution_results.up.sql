ALTER TABLE travel.execution_steps ADD COLUMN result jsonb CHECK(result IS NULL OR jsonb_typeof(result)='object');
CREATE FUNCTION travel.protect_execution_result() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF OLD.result IS NOT NULL AND NEW.result IS DISTINCT FROM OLD.result THEN RAISE EXCEPTION 'immutable execution result' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_execution_result BEFORE UPDATE OF result ON travel.execution_steps FOR EACH ROW EXECUTE FUNCTION travel.protect_execution_result();
