ALTER TABLE travel_preference_revisions DROP CONSTRAINT travel_preference_revisions_source_run_id_operation_id_key;
ALTER TABLE travel_preference_revisions ADD CONSTRAINT travel_memory_operation_key UNIQUE(source_run_id,operation_id,preference_key);
ALTER TABLE travel_preference_revisions ADD COLUMN source_runs jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(source_runs)='array');
CREATE TABLE travel_query_jobs (
 id text PRIMARY KEY, run_id text NOT NULL, user_id text NOT NULL, conversation_id text NOT NULL,
 parent_operation_id text NOT NULL, plan_id text NOT NULL, plan_revision bigint NOT NULL CHECK(plan_revision>0),
 ordinal integer NOT NULL CHECK(ordinal>=0), query jsonb NOT NULL CHECK(jsonb_typeof(query)='object'),
 status text NOT NULL CHECK(status IN('accepted','running','succeeded','unknown')), attempt bigint NOT NULL CHECK(attempt>0),
 result jsonb, updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id),
 FOREIGN KEY(plan_id,user_id,conversation_id) REFERENCES travel_plans(id,user_id,conversation_id),
 UNIQUE(run_id,parent_operation_id,ordinal)
);
