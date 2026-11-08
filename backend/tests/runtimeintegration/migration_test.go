package runtimeintegration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"agent-platform/backend/internal/migration"
	"agent-platform/backend/internal/repository/platformpg"
	"agent-platform/backend/internal/repository/travelpg"
	"agent-platform/backend/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimeMigrationPlatformRequiresControlCapabilities(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	// A real v001-only installation, not a corrupted migration receipt.
	tx, err := f.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{f.Principals.Owner("control")}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.Files.ReadFile("control/001_coordination.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(sql)
	if _, err = tx.Exec(ctx, "CREATE TABLE control.schema_migrations(version text PRIMARY KEY,content_hash text NOT NULL,applied_at timestamptz NOT NULL DEFAULT clock_timestamp())"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO control.schema_migrations(version,content_hash) VALUES('001',$1)", hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "GRANT SELECT ON control.schema_migrations TO "+pgx.Identifier{f.Principals.Migrator("platform")}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = migration.Apply(ctx, f.Migrations["platform"], "platform"); err == nil {
		t.Fatal("platform atomic sessions installed before control capabilities")
	}
	if err = migration.Apply(ctx, f.Migrations["control"], "control"); err != nil {
		t.Fatal(err)
	}
	if err = migration.Apply(ctx, f.Migrations["platform"], "platform"); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeMigrationIdempotent(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	if err := migration.Apply(ctx, f.Migrations["travel"], "travel"); err == nil {
		t.Fatal("out-of-order migration accepted")
	}
	f.apply(t)
	if _, err := migration.Provision(ctx, f.Admin, f.Principals.Prefix, nil); err != nil {
		t.Fatalf("idempotent role bootstrap: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- migration.Apply(ctx, f.Migrations["platform"], "platform")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	f.apply(t)
	for _, component := range []string{"control", "platform", "travel"} {
		var count int
		expected := map[string]int{"control": 3, "platform": 2, "travel": 1}[component]
		if err := f.Admin.QueryRow(ctx, "SELECT count(*) FROM "+component+".schema_migrations").Scan(&count); err != nil || count != expected {
			t.Fatalf("%s versions=%d err=%v", component, count, err)
		}
	}
	var crossFK int
	if err := f.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_constraint c JOIN pg_class a ON a.oid=c.conrelid JOIN pg_class b ON b.oid=c.confrelid JOIN pg_namespace x ON x.oid=a.relnamespace JOIN pg_namespace y ON y.oid=b.relnamespace WHERE c.contype='f' AND x.nspname IN ('platform','travel','control') AND x.oid<>y.oid`).Scan(&crossFK); err != nil || crossFK != 0 {
		t.Fatalf("cross-schema business FK=%d err=%v", crossFK, err)
	}
	for _, table := range []string{"platform.agent_runs", "platform.agent_jobs", "platform.agent_threads", "platform.messages", "platform.checkpoints", "platform.agent_delegations", "platform.agent_dispatches", "platform.outbox_messages", "platform.tool_calls", "platform.tool_evidence", "platform.evidence_imports", "platform.memory_operations", "platform.user_memory_revisions", "travel.tasks", "travel.threads", "travel.messages", "travel.checkpoints", "travel.tool_calls", "travel.tool_evidence", "travel.evidence_imports", "travel.plans", "travel.revisions", "travel.execution_steps", "travel.actions", "control.session_guards", "control.run_guards", "control.context_guards", "control.execution_scopes", "control.resource_limits", "control.outbound_calls"} {
		var exists bool
		if err := f.Admin.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
	if platformpg.New(f.Platform) == nil || travelpg.New(f.Travel) == nil {
		t.Fatal("missing schema repository boundary")
	}
	if err := migration.Apply(ctx, f.Admin, "platform"); err == nil {
		t.Fatal("administrator was accepted as component migration identity")
	}
	if err := migration.Apply(ctx, f.Platform, "platform"); err == nil {
		t.Fatal("runtime identity executed DDL")
	}
}

func denied(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
		t.Fatalf("expected denied permission, got %v", err)
	}
}

func TestSchemaRolesDenyCrossBusinessAccess(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	f.apply(t)
	for label, pool := range map[string]*pgxpool.Pool{"platform": f.Platform, "travel": f.Travel, "maintenance": f.Maintenance} {
		var login, current, path string
		var super, createRole, createDB, bypass bool
		if err := pool.QueryRow(ctx, `SELECT session_user,current_user,current_setting('search_path'),rolsuper,rolcreaterole,rolcreatedb,rolbypassrls FROM pg_roles WHERE rolname=session_user`).Scan(&login, &current, &path, &super, &createRole, &createDB, &bypass); err != nil {
			t.Fatal(err)
		}
		if login != f.Principals.Runtime(label) || current != login || super || createRole || createDB || bypass {
			t.Fatalf("invalid real runtime principal %s", label)
		}
		if path == "\"$user\", public" {
			t.Fatal("unfixed search_path")
		}
		denied(t, pool, "CREATE TABLE public.forbidden(id int)")
		denied(t, pool, "CREATE TABLE platform.forbidden(id int)")
		denied(t, pool, "CREATE TABLE travel.forbidden(id int)")
		denied(t, pool, "SELECT * FROM control.execution_scopes")
		denied(t, pool, "UPDATE control.resource_limits SET max_concurrency=99")
		denied(t, pool, "SET ROLE "+f.Principals.Owner("control"))
	}
	denied(t, f.Travel, "SELECT * FROM platform.users")
	denied(t, f.Platform, "UPDATE travel.tasks SET status='queued'")
	denied(t, f.Platform, "DELETE FROM platform.messages")
	denied(t, f.Platform, "INSERT INTO platform.sessions(id) VALUES('unsafe')")
}

func TestRuntimeMigrationConstraints(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	_, err := f.Admin.Exec(ctx, `
 INSERT INTO platform.users(id,username,password_hash) VALUES('u','fixture','hash');
 INSERT INTO platform.sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES('s','u','token','csrf',clock_timestamp()+interval '1 day');
 INSERT INTO platform.conversations(id,user_id,agent_type,title) VALUES('c','u','general','fixture');
 INSERT INTO platform.agent_threads(id,user_id,conversation_id,agent_kind,thread_role) VALUES('c','u','c','general','entry');
 INSERT INTO platform.agent_runs(id,user_id,conversation_id,origin_session_id,root_job_id,idempotency_key,request_hash,status,kernel_version,deadline_at) VALUES('r','u','c','s','j','key','hash','accepted','v1',clock_timestamp()+interval '5 minutes'),('r2','u','c','s','j2','key2','hash2','succeeded','v1',clock_timestamp()+interval '5 minutes');
 INSERT INTO platform.agent_jobs(id,run_id,user_id,conversation_id,thread_id,scope_id,scope_hash,status,deadline_at) VALUES('j','r','u','c','c','scope','scope-hash','ready',clock_timestamp()+interval '5 minutes'),('j2','r2','u','c','c','scope2','scope-hash2','ready',clock_timestamp()+interval '5 minutes');
 INSERT INTO travel.threads(id,user_id,conversation_id,origin_thread_id,last_main_context_epoch) VALUES('t','u','c','c',1),('t2','u','c','c',1);
 INSERT INTO travel.tasks(id,caller_id,subject_id,conversation_id,source_run_id,delegation_id,request_key,input_hash,arguments,scope_id,scope_hash,thread_id,status,deadline_at) VALUES('task','platform','u','c','r','d','request','hash','{}','scope','hash','t','queued',clock_timestamp()+interval '5 minutes');
 INSERT INTO control.session_guards(origin_session_id,subject_id,expires_at) VALUES('s','u',clock_timestamp()+interval '1 day');
 INSERT INTO control.context_guards(subject_id,conversation_id,context_epoch,active) VALUES('u','c',1,true);
 INSERT INTO control.run_guards(source_run_id,subject_id,conversation_id,origin_session_id,deadline_at,budget_limits) VALUES('r','u','c','s',clock_timestamp()+interval '5 minutes','{}');
 INSERT INTO control.execution_scopes(scope_id,issuer_service,owner_service,subject_id,source_run_id,conversation_id,capabilities,targets,source_scope,context_epoch,expires_at,scope_hash) VALUES('scope','platform','platform','u','r','c','[]','[]','{}',1,clock_timestamp()+interval '5 minutes','hash');
 `)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, sql, code string }{
		{"guard_session_subject", "INSERT INTO control.run_guards(source_run_id,subject_id,conversation_id,origin_session_id,deadline_at,budget_limits) VALUES('bad-run','another-user','c','s',clock_timestamp()+interval '5 minutes','{}')", "23503"},
		{"attempt_scope_owner", "INSERT INTO control.outbound_calls(attempt_id,scope_id,logical_call_key,attempt_no,owner_service,owner_job_id,lease_epoch,resource_keys,reservation,status,hold_until) VALUES('bad-attempt','scope','call',1,'travel','task',1,'[]','{}','reserved',clock_timestamp()+interval '10 seconds')", "23503"},
		{"root_job_belongs_to_run", "UPDATE platform.agent_runs SET root_job_id='j2' WHERE id='r'", "23503"},
		{"checkpoint_belongs_to_job_run", "INSERT INTO platform.checkpoints(job_id,checkpoint_version,run_id,user_id,conversation_id,thread_id,lease_epoch,engine,engine_version,format_version,fingerprint,payload,payload_hash,interrupts,context_epoch) VALUES('j',1,'r2','u','c','c',1,'eino','v',1,'f','bytes','h','{}',1)", "23503"},
		{"waiting_has_no_lease", "UPDATE platform.agent_jobs SET status='waiting',worker_id='w',lease_epoch=1,lease_expires_at=clock_timestamp()+interval '10 seconds' WHERE id='j'", "23514"},
		{"retry_count_bounded", "UPDATE travel.tasks SET retry_count=4 WHERE id='task'", "23514"},
		{"same_key_not_new_task", "INSERT INTO travel.tasks SELECT 'duplicate',caller_id,subject_id,conversation_id,source_run_id,delegation_id,request_key,input_hash,arguments,scope_id,scope_hash,thread_id,status,worker_id,lease_epoch,lease_expires_at,retry_count,checkpoint_version,deadline_at,not_before,loop_count,no_progress_count,result_version,result,error,created_at,updated_at,completed_at FROM travel.tasks WHERE id='task'", "23505"},
		{"task_thread_ownership", "INSERT INTO travel.messages(id,task_id,user_id,conversation_id,thread_id,ordinal,role,content,message_kind,context_epoch,source_key) VALUES('message','task','u','c','t2',1,'user','x','delegation_context',1,'key')", "23503"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.Admin.Exec(ctx, c.sql)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != c.code {
				t.Fatalf("expected %s, got %v", c.code, err)
			}
		})
	}
}

func TestRuntimeMigrationRejectsChangedHistory(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	if _, err := f.Admin.Exec(ctx, "UPDATE control.schema_migrations SET content_hash='changed' WHERE version='001'"); err != nil {
		t.Fatal(err)
	}
	if err := migration.Apply(ctx, f.Migrations["control"], "control"); err == nil {
		t.Fatal("changed applied migration checksum accepted")
	}
}

func TestRuntimeMigrationFreshDatabase(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	for _, table := range []string{"platform.users", "platform.sessions", "platform.conversations", "platform.agent_runs", "platform.agent_jobs", "platform.messages", "platform.checkpoints", "platform.user_memory_revisions", "travel.tasks", "travel.plans", "travel.messages", "travel.checkpoints"} {
		var count int
		if err := f.Admin.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("fresh %s unexpectedly contains imported data: count=%d err=%v", table, count, err)
		}
	}
	var publicBusinessTables int
	if err := f.Admin.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname='public'").Scan(&publicBusinessTables); err != nil || publicBusinessTables != 0 {
		t.Fatal("fresh deployment created old public business tables")
	}
}
