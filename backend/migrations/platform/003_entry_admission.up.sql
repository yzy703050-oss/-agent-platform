-- Count a user's active Runs without walking their retained terminal history.
-- The user row, not this index or a process counter, serializes admission.
CREATE INDEX run_active_user ON platform.agent_runs(user_id,id)
 WHERE status IN('accepted','running','waiting');
