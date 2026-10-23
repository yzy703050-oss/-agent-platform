CREATE TABLE users(id text PRIMARY KEY,username text NOT NULL UNIQUE,password_hash text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE sessions(id text PRIMARY KEY,user_id text NOT NULL REFERENCES users(id),token_hash text NOT NULL UNIQUE,csrf_hash text NOT NULL,expires_at timestamptz NOT NULL,revoked_at timestamptz);
CREATE TABLE user_settings(user_id text PRIMARY KEY REFERENCES users(id),theme text NOT NULL DEFAULT 'system',reply_preference text NOT NULL DEFAULT '');
CREATE TABLE page_leases(id text PRIMARY KEY,user_id text NOT NULL REFERENCES users(id),session_id text NOT NULL REFERENCES sessions(id),page_token_hash text NOT NULL,last_seen_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL,closed_at timestamptz);
CREATE TABLE conversations(id text PRIMARY KEY,user_id text NOT NULL REFERENCES users(id),agent_type text NOT NULL CHECK(agent_type IN('general','travel')),title text NOT NULL,deleted_at timestamptz,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(id,user_id));
CREATE TABLE agent_runs(
 id text PRIMARY KEY,user_id text NOT NULL,conversation_id text NOT NULL,page_id text NOT NULL REFERENCES page_leases(id),
 idempotency_key text NOT NULL,request_hash text NOT NULL,
 status text NOT NULL CHECK(status IN('accepted','running','pausing','paused','resuming','cancelling','succeeded','failed','timed_out','cancelled','interrupted')),
 attempt bigint NOT NULL DEFAULT 1 CHECK(attempt>0),version bigint NOT NULL DEFAULT 1,
 context_eligible boolean NOT NULL DEFAULT true,error_code text NOT NULL DEFAULT '',config_fingerprint text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(conversation_id,user_id) REFERENCES conversations(id,user_id),
 UNIQUE(user_id,conversation_id,idempotency_key),UNIQUE(id,user_id,conversation_id));
CREATE UNIQUE INDEX one_nonterminal_run ON agent_runs(conversation_id) WHERE status IN('accepted','running','pausing','paused','resuming','cancelling');
CREATE INDEX user_conversations ON conversations(user_id,created_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE TABLE messages(
 id text PRIMARY KEY,conversation_id text NOT NULL,run_id text NOT NULL,user_id text NOT NULL,role text NOT NULL CHECK(role IN('user','assistant','tool')),
 content text NOT NULL,tool_call_id text NOT NULL DEFAULT '',tool_calls jsonb NOT NULL DEFAULT '[]',ordinal bigint NOT NULL,display_status text NOT NULL DEFAULT 'complete',
 created_at timestamptz NOT NULL DEFAULT now(),FOREIGN KEY(run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id),UNIQUE(conversation_id,ordinal));
CREATE TABLE run_events(run_id text NOT NULL REFERENCES agent_runs(id),seq bigint NOT NULL,attempt bigint NOT NULL,type text NOT NULL,payload jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(run_id,seq));
CREATE TABLE checkpoints(run_id text PRIMARY KEY,user_id text NOT NULL,conversation_id text NOT NULL,engine text NOT NULL,engine_version text NOT NULL,format_version integer NOT NULL,attempt bigint NOT NULL,fingerprint text NOT NULL,payload bytea NOT NULL,valid boolean NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),FOREIGN KEY(run_id,user_id,conversation_id) REFERENCES agent_runs(id,user_id,conversation_id));

