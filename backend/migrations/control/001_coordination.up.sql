CREATE TABLE control.service_principals(login_role text PRIMARY KEY,
 service_id text NOT NULL CHECK(service_id IN('platform','travel','maintenance')));
CREATE TABLE control.session_guards(origin_session_id text PRIMARY KEY,
 subject_id text NOT NULL,
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0));
CREATE TABLE control.run_guards(source_run_id text PRIMARY KEY,
 subject_id text NOT NULL,
 conversation_id text NOT NULL,
 origin_session_id text NOT NULL REFERENCES control.session_guards(origin_session_id),
 deadline_at timestamptz NOT NULL,
 active boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 budget_limits jsonb NOT NULL CHECK(jsonb_typeof(budget_limits)='object'));
CREATE TABLE control.context_guards(subject_id text NOT NULL,
 conversation_id text NOT NULL,
 context_epoch bigint NOT NULL CHECK(context_epoch>0),
 active boolean NOT NULL,
 PRIMARY KEY(subject_id,conversation_id));
CREATE TABLE control.execution_scopes(
 scope_id text PRIMARY KEY,
 issuer_service text NOT NULL,
 owner_service text NOT NULL,
 subject_id text NOT NULL,
 source_run_id text REFERENCES control.run_guards(source_run_id),
 conversation_id text NOT NULL,

 delegation_id text,
 operation_id text,
 capabilities jsonb NOT NULL CHECK(jsonb_typeof(capabilities)='array'),
 targets jsonb NOT NULL CHECK(jsonb_typeof(targets)='array'),
 source_scope jsonb NOT NULL CHECK(jsonb_typeof(source_scope)='object'),
 context_epoch bigint NOT NULL CHECK(context_epoch>0),

 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 scope_hash text NOT NULL,
 budget_limits jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(budget_limits)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((source_run_id IS NULL)<>(operation_id IS NULL))
);
CREATE TABLE control.resource_limits(resource_key text PRIMARY KEY,
 max_concurrency integer NOT NULL CHECK(max_concurrency>0),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 redis_generation bigint NOT NULL DEFAULT 1 CHECK(redis_generation>0));
CREATE TABLE control.outbound_calls(
 attempt_id text PRIMARY KEY,
 scope_id text NOT NULL REFERENCES control.execution_scopes(scope_id),
 logical_call_key text NOT NULL,
 attempt_no integer NOT NULL CHECK(attempt_no BETWEEN 1 AND 4),
 owner_service text NOT NULL,

 owner_job_id text,
 owner_operation_id text,
 lease_epoch bigint NOT NULL CHECK(lease_epoch>0),
 resource_keys jsonb NOT NULL CHECK(jsonb_typeof(resource_keys)='array'),
 reservation jsonb NOT NULL CHECK(jsonb_typeof(reservation)='object'),
 usage jsonb,

 status text NOT NULL CHECK(status IN('reserved','running','succeeded','failed','unknown','not_sent')),
 hold_until timestamptz NOT NULL,
 response_ref jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 settled_at timestamptz,

 UNIQUE(scope_id,logical_call_key,attempt_no),
 CHECK((owner_job_id IS NULL)<>(owner_operation_id IS NULL)),
 CHECK(usage IS NULL OR jsonb_typeof(usage)='object')
);
CREATE INDEX outbound_holds ON control.outbound_calls(hold_until) WHERE status IN('reserved',
 'running',
 'unknown');
CREATE INDEX outbound_budget ON control.outbound_calls(scope_id,
 status);
ALTER TABLE control.session_guards ADD CONSTRAINT session_guard_subject UNIQUE(origin_session_id,subject_id);
ALTER TABLE control.run_guards ADD CONSTRAINT run_guard_session_subject FOREIGN KEY(origin_session_id,subject_id) REFERENCES control.session_guards(origin_session_id,subject_id);
ALTER TABLE control.run_guards ADD CONSTRAINT run_guard_subject UNIQUE(source_run_id,subject_id,conversation_id);
ALTER TABLE control.execution_scopes ADD CONSTRAINT scope_run_subject FOREIGN KEY(source_run_id,subject_id,conversation_id) REFERENCES control.run_guards(source_run_id,subject_id,conversation_id);
ALTER TABLE control.execution_scopes ADD CONSTRAINT scope_owner UNIQUE(scope_id,owner_service);
ALTER TABLE control.outbound_calls ADD CONSTRAINT attempt_scope_owner FOREIGN KEY(scope_id,owner_service) REFERENCES control.execution_scopes(scope_id,owner_service);
