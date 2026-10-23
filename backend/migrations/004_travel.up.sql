CREATE TABLE travel_plans (
 id text PRIMARY KEY, user_id text NOT NULL, conversation_id text NOT NULL, source_run_id text NOT NULL,
 current_revision bigint NOT NULL CHECK(current_revision>0),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,user_id,conversation_id),
 FOREIGN KEY(source_run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id)
);
CREATE TABLE travel_plan_revisions (
 plan_id text NOT NULL, user_id text NOT NULL, conversation_id text NOT NULL, revision bigint NOT NULL CHECK(revision>0), source_run_id text NOT NULL,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(plan_id,revision),
 FOREIGN KEY(plan_id,user_id,conversation_id) REFERENCES travel_plans(id,user_id,conversation_id),
 FOREIGN KEY(source_run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id)
);
CREATE TABLE travel_query_evidence (
 id text NOT NULL, result_revision bigint NOT NULL CHECK(result_revision>0), user_id text NOT NULL, conversation_id text NOT NULL, source_run_id text NOT NULL,
 plan_id text, task_id text NOT NULL DEFAULT '', query_key text NOT NULL,
 fetched_at timestamptz NOT NULL, expires_at timestamptz NOT NULL CHECK(expires_at>fetched_at), snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 PRIMARY KEY(id,result_revision),
 FOREIGN KEY(source_run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id),
 FOREIGN KEY(plan_id,user_id,conversation_id) REFERENCES travel_plans(id,user_id,conversation_id)
);
CREATE INDEX travel_evidence_lookup ON travel_query_evidence(user_id,conversation_id,query_key,expires_at DESC);
CREATE TABLE travel_decisions (
 run_id text NOT NULL, user_id text NOT NULL, conversation_id text NOT NULL, decision_key text NOT NULL, stage text NOT NULL, request_hash text NOT NULL,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(run_id,decision_key),
 FOREIGN KEY(run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id)
);
CREATE TABLE travel_actions (
 run_id text NOT NULL, user_id text NOT NULL, conversation_id text NOT NULL, operation_id text NOT NULL, kind text NOT NULL,
 arguments jsonb NOT NULL DEFAULT '{}', request_hash text NOT NULL DEFAULT '',
 status text NOT NULL CHECK(status IN('accepted','running','succeeded','failed','unknown')), attempt bigint NOT NULL CHECK(attempt>0),
 result_reference jsonb NOT NULL DEFAULT '{}', updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(run_id,operation_id),
 FOREIGN KEY(run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id)
);
CREATE TABLE travel_operation_receipts (
 run_id text NOT NULL, user_id text NOT NULL, conversation_id text NOT NULL, operation_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN('trip','preference')), request_hash text NOT NULL,
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object'), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,run_id,operation_id),
 FOREIGN KEY(run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id),
 FOREIGN KEY(run_id,operation_id) REFERENCES travel_actions(run_id,operation_id)
);
CREATE TABLE travel_preference_revisions (
 user_id text NOT NULL, preference_key text NOT NULL, revision bigint NOT NULL CHECK(revision>0), conversation_id text NOT NULL, source_run_id text NOT NULL,
 value jsonb NOT NULL, retracted boolean NOT NULL DEFAULT false, origin text NOT NULL CHECK(origin IN('explicit','inferred')), operation_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,preference_key,revision), UNIQUE(source_run_id,operation_id),
 FOREIGN KEY(source_run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id)
);
CREATE TABLE travel_memory_summaries (
 id text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), conversation_id text NOT NULL, source_run_id text NOT NULL,
 source_runs jsonb NOT NULL CHECK(jsonb_typeof(source_runs)='array'), version bigint NOT NULL CHECK(version>0), content text NOT NULL, valid boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(source_run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id)
);
CREATE INDEX travel_summaries_lookup ON travel_memory_summaries(user_id,conversation_id,created_at DESC);
