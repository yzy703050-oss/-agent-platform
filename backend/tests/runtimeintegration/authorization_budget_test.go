package runtimeintegration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests catch a missing transaction-held guard lock, a forged service
// identity, and budget refunds caused by losing usage. All calls use real logins.
func seedAuthorization(t *testing.T, f *runtimeFixture) {
	t.Helper()
	_, err := f.Admin.Exec(context.Background(), `
INSERT INTO control.session_guards(origin_session_id,subject_id,expires_at) VALUES('session','user',clock_timestamp()+interval '1 hour');
INSERT INTO control.run_guards(source_run_id,subject_id,conversation_id,origin_session_id,deadline_at,budget_limits)
VALUES('run','user','conversation','session',clock_timestamp()+interval '5 minutes','{"model_attempts":4,"tool_attempts":4,"token_upper_bound":100,"cost_upper_micros":1000}');
INSERT INTO control.context_guards VALUES('user','conversation',1,true);
INSERT INTO control.resource_limits(resource_key,max_concurrency) VALUES('account',2),('endpoint',2);`)
	if err != nil {
		t.Fatal(err)
	}
}

func issueTestScope(t *testing.T, f *runtimeFixture, id, owner string) map[string]any {
	return issueTestScopeForRun(t, f, id, owner, "run")
}

func issueTestScopeForRun(t *testing.T, f *runtimeFixture, id, owner, run string) map[string]any {
	t.Helper()
	input := map[string]any{"scope_id": id, "owner_service": owner, "subject_id": "user", "source_run_id": run, "conversation_id": "conversation", "context_epoch": 1, "expires_at": time.Now().UTC().Add(time.Minute), "capabilities": []string{"commit", "model", "tool", "control_rpc"}, "targets": []string{"plan", "account", "endpoint"}, "source_scope": map[string]any{}}
	b, _ := json.Marshal(input)
	var result []byte
	if err := f.Platform.QueryRow(context.Background(), "SELECT control.issue_scope($1::jsonb)", b).Scan(&result); err != nil {
		t.Fatal(err)
	}
	var scope map[string]any
	if err := json.Unmarshal(result, &scope); err != nil {
		t.Fatal(err)
	}
	return scope
}

func assertTestScope(ctx context.Context, tx pgx.Tx, scope map[string]any) error {
	_, err := tx.Exec(ctx, "SELECT control.assert_scope($1,$2,'commit','plan','commit')", scope["scope_id"], scope["scope_hash"])
	return err
}

// Wait on the actual PostgreSQL lock graph, not a sleep-based race guess.
func awaitBlocked(t *testing.T, admin *pgxpool.Pool, pid int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var blocked bool
		if err := admin.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1))>0", pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
	}
	t.Fatal("expected guard lock contention was not observed")
}

func TestAuthorizationCommitRevocationRace(t *testing.T) {
	for _, commitFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "commit_first", false: "revoke_first"}[commitFirst], func(t *testing.T) {
			f := newRuntimeFixture(t)
			f.apply(t)
			seedAuthorization(t, f)
			scope := issueTestScope(t, f, "scope", "travel")
			ctx := context.Background()
			commit, err := f.Travel.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer commit.Rollback(ctx)
			revoke, err := f.Platform.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer revoke.Rollback(ctx)
			writeBusiness := func() error {
				if e := assertTestScope(ctx, commit, scope); e != nil {
					return e
				}
				_, e := commit.Exec(ctx, `INSERT INTO travel.threads(id,user_id,conversation_id,origin_thread_id,last_main_context_epoch) VALUES('thread','user','conversation','conversation',1)`)
				return e
			}
			if commitFirst {
				if err = writeBusiness(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, e := revoke.Exec(ctx, "SELECT control.revoke_session_guard('session')"); done <- e }()
				awaitBlocked(t, f.Admin, int32(revoke.Conn().PgConn().PID()))
				if err = commit.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err = <-done; err != nil {
					t.Fatal(err)
				}
				if err = revoke.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = revoke.Exec(ctx, "SELECT control.revoke_session_guard('session')"); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- writeBusiness() }()
				awaitBlocked(t, f.Admin, int32(commit.Conn().PgConn().PID()))
				if err = revoke.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err = <-done; err == nil {
					t.Fatal("revoked scope committed")
				}
			}
			var businessRows int
			if err = f.Admin.QueryRow(ctx, "SELECT count(*) FROM travel.threads WHERE id='thread'").Scan(&businessRows); err != nil || businessRows != map[bool]int{true: 1, false: 0}[commitFirst] {
				t.Fatalf("business commit rows=%d %v", businessRows, err)
			}
			tx, err := f.Travel.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err = assertTestScope(ctx, tx, scope); err == nil {
				t.Fatal("revocation not enforced on another connection")
			}
		})
	}
}

func TestServiceRoleScopeIsolation(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	scope := issueTestScope(t, f, "scope", "travel")
	ctx := context.Background()
	for _, pool := range []*pgxpool.Pool{f.Platform, f.Maintenance} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = assertTestScope(ctx, tx, scope); err == nil {
			t.Fatal("another service used travel scope")
		}
		tx.Rollback(ctx)
	}
	if _, err := f.Travel.Exec(ctx, `SELECT control.issue_scope('{"owner_service":"travel"}')`); err == nil {
		t.Fatal("travel issued scope")
	}
	tx, err := f.Travel.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT control.assert_scope($1,'wrong','commit','plan','commit')", scope["scope_id"]); err == nil {
		t.Fatal("forged hash accepted")
	}
	for _, args := range [][]any{{scope["scope_id"], scope["scope_hash"], "delete", "plan", "commit"}, {scope["scope_id"], scope["scope_hash"], "commit", "other", "commit"}} {
		tx, err := f.Travel.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "SELECT control.assert_scope($1,$2,$3,$4,$5)", args...); err == nil {
			t.Fatal("scope enlarged")
		}
		tx.Rollback(ctx)
	}
}

func reserveTestAttempt(ctx context.Context, pool *pgxpool.Pool, scope map[string]any, id string, resources []string) (map[string]any, error) {
	input := map[string]any{"attempt_id": id, "scope_id": scope["scope_id"], "scope_hash": scope["scope_hash"], "capability": "model", "target": "account", "logical_call_key": id, "attempt_no": 1, "owner_job_id": "job", "owner_epoch": 1, "resource_keys": resources, "reservation": map[string]int{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": 60, "cost_upper_micros": 100}, "hold_until": time.Now().UTC().Add(30 * time.Second)}
	b, _ := json.Marshal(input)
	var result []byte
	err := pool.QueryRow(ctx, "SELECT control.reserve_attempt($1::jsonb)", b).Scan(&result)
	var a map[string]any
	if err == nil {
		err = json.Unmarshal(result, &a)
	}
	return a, err
}

func settleTestAttempt(ctx context.Context, pool *pgxpool.Pool, id, status string, usage any) (bool, error) {
	b, _ := json.Marshal(map[string]any{"attempt_id": id, "status": status, "usage": usage, "response_ref": map[string]any{"id": "response"}})
	var changed bool
	err := pool.QueryRow(ctx, "SELECT control.settle_attempt($1::jsonb)", b).Scan(&changed)
	return changed, err
}

func TestBudgetReservationSurvivesUnknownUsage(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	parent := issueTestScope(t, f, "parent", "platform")
	child := issueTestScope(t, f, "child", "travel")
	if _, err := reserveTestAttempt(ctx, f.Platform, parent, "first", []string{"account", "endpoint"}); err != nil {
		t.Fatal(err)
	}
	if _, err := settleTestAttempt(ctx, f.Platform, "first", "unknown", nil); err != nil {
		t.Fatal(err)
	}
	var reserved int
	if err := f.Admin.QueryRow(ctx, "SELECT sum((reservation->>'token_upper_bound')::int) FROM control.outbound_calls").Scan(&reserved); err != nil || reserved != 60 {
		t.Fatalf("unknown refunded: %d %v", reserved, err)
	}
	if _, err := reserveTestAttempt(ctx, f.Travel, child, "second", []string{"endpoint", "account"}); err == nil {
		t.Fatal("child spent a separate budget")
	}
	usage := map[string]int{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": 20, "cost_upper_micros": 40}
	if changed, err := settleTestAttempt(ctx, f.Platform, "first", "succeeded", usage); err != nil || !changed {
		t.Fatalf("late real usage not settled: %v %v", changed, err)
	}
	if changed, err := settleTestAttempt(ctx, f.Platform, "first", "succeeded", usage); err != nil || changed {
		t.Fatalf("usage settled twice: %v %v", changed, err)
	}
	if _, err := settleTestAttempt(ctx, f.Travel, "first", "not_sent", nil); err == nil {
		t.Fatal("another service settled attempt")
	}
	if _, err := reserveTestAttempt(ctx, f.Travel, child, "second", []string{"endpoint", "account"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Platform.Exec(ctx, "SELECT control.revoke_session_guard('session')"); err != nil {
		t.Fatal(err)
	}
	if _, err := settleTestAttempt(ctx, f.Travel, "second", "succeeded", usage); err != nil {
		t.Fatalf("revocation blocked original settlement: %v", err)
	}
	if _, err := reserveTestAttempt(ctx, f.Travel, child, "third", []string{"account"}); err == nil {
		t.Fatal("revoked scope reserved")
	}
}

func TestBudgetAdmissionConcurrentResourceOrder(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	if _, err := f.Admin.Exec(ctx, `UPDATE control.run_guards SET budget_limits='{"model_attempts":20,"tool_attempts":20,"token_upper_bound":2000,"cost_upper_micros":10000}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Admin.Exec(ctx, `INSERT INTO control.run_guards SELECT 'run2',subject_id,conversation_id,origin_session_id,deadline_at,active,version,budget_limits FROM control.run_guards WHERE source_run_id='run'`); err != nil {
		t.Fatal(err)
	}
	parent := issueTestScope(t, f, "parent", "platform")
	child := issueTestScopeForRun(t, f, "child", "travel", "run2")
	results := make(chan error, 4)
	for i := range 4 {
		go func() {
			pool, scope := f.Platform, parent
			keys := []string{"account", "endpoint"}
			if i%2 == 1 {
				pool, scope = f.Travel, child
				keys = []string{"endpoint", "account"}
			}
			_, err := reserveTestAttempt(ctx, pool, scope, string(rune('a'+i)), keys)
			results <- err
		}()
	}
	accepted := 0
	for range 4 {
		if err := <-results; err == nil {
			accepted++
		}
	}
	if accepted != 2 {
		t.Fatalf("hard shared concurrency: accepted=%d want=2", accepted)
	}
	var n int
	if err := f.Admin.QueryRow(ctx, "SELECT count(*) FROM control.outbound_calls").Scan(&n); err != nil || n != 2 {
		t.Fatalf("ledger=%d %v", n, err)
	}
}

func TestServiceRoleRejectsNullCapabilityAndTarget(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	scope := issueTestScope(t, f, "scope", "travel")
	for _, args := range [][]any{{scope["scope_id"], scope["scope_hash"], nil, "plan", "commit"}, {scope["scope_id"], scope["scope_hash"], "commit", nil, "commit"}} {
		if _, err := f.Travel.Exec(context.Background(), "SELECT control.assert_scope($1,$2,$3,$4,$5)", args...); err == nil {
			t.Fatal("null bypassed capability/target restriction")
		}
	}
}

func TestServiceRolePrivateHelpersNotCallable(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	for _, pool := range []*pgxpool.Pool{f.Platform, f.Travel, f.Maintenance} {
		denied(t, pool, "SELECT control.valid_budget('{}'::jsonb)")
		denied(t, pool, "SELECT control.lock_authority(NULL,'user','conversation',1,false)")
		denied(t, pool, "SELECT * FROM control.service_principals")
	}
	var invalid int
	if err := f.Admin.QueryRow(context.Background(), `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='control' AND (r.rolcanlogin OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog'])`).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatalf("unsafe function ownership or path: %d %v", invalid, err)
	}
}

func TestBudgetAdmissionRechecksExpiryAfterResourceWait(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	scope := issueTestScope(t, f, "scope", "platform")
	if _, err := f.Admin.Exec(ctx, "UPDATE control.execution_scopes SET expires_at=clock_timestamp()+interval '500 milliseconds'"); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, "SELECT 1 FROM control.resource_limits WHERE resource_key='account' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	conn, err := f.Platform.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	b, _ := json.Marshal(map[string]any{"attempt_id": "late", "scope_id": scope["scope_id"], "scope_hash": scope["scope_hash"], "capability": "model", "target": "account", "logical_call_key": "late", "attempt_no": 1, "owner_job_id": "job", "owner_epoch": 1, "resource_keys": []string{"account"}, "reservation": map[string]int{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": 10, "cost_upper_micros": 0}, "hold_until": time.Now().Add(time.Minute)})
	done := make(chan error, 1)
	go func() { _, e := conn.Exec(ctx, "SELECT control.reserve_attempt($1::jsonb)", b); done <- e }()
	awaitBlocked(t, f.Admin, int32(conn.Conn().PgConn().PID()))
	// Wait for the database's expiry event after observing lock contention.
	if _, err = f.Admin.Exec(ctx, "SELECT pg_sleep(GREATEST(0,extract(epoch FROM expires_at-clock_timestamp()))) FROM control.execution_scopes WHERE scope_id='scope'"); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("expired scope admitted after resource wait")
	}
}

func TestBudgetRequiresFiniteBoundsAndCountsEveryAttempt(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	scope := issueTestScope(t, f, "scope", "platform")
	input := map[string]any{"attempt_id": "a", "scope_id": scope["scope_id"], "scope_hash": scope["scope_hash"], "capability": "model", "target": "account", "logical_call_key": "call", "attempt_no": 1, "owner_job_id": "job", "owner_epoch": 1, "resource_keys": []string{"account"}, "hold_until": time.Now().Add(time.Minute)}
	for name, reservation := range map[string]any{
		"missing":      map[string]any{"model_attempts": 1},
		"negative":     map[string]any{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": -1, "cost_upper_micros": 0},
		"fractional":   map[string]any{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": 1.5, "cost_upper_micros": 0},
		"overflow":     map[string]any{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": json.Number("9223372036854775808"), "cost_upper_micros": 0},
		"free_attempt": map[string]any{"model_attempts": 0, "tool_attempts": 0, "token_upper_bound": 10, "cost_upper_micros": 0},
	} {
		t.Run(name, func(t *testing.T) {
			input["reservation"] = reservation
			b, _ := json.Marshal(input)
			if _, err := f.Platform.Exec(ctx, "SELECT control.reserve_attempt($1::jsonb)", b); err == nil {
				t.Fatal("invalid reservation admitted")
			}
		})
	}
	input["reservation"] = map[string]int{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": 20, "cost_upper_micros": 10}
	for n := 1; n <= 4; n++ {
		id := string(rune('a' + n - 1))
		input["attempt_id"], input["attempt_no"] = id, n
		b, _ := json.Marshal(input)
		var raw []byte
		if err := f.Platform.QueryRow(ctx, "SELECT control.reserve_attempt($1::jsonb)", b).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := f.Platform.QueryRow(ctx, "SELECT control.reserve_attempt($1::jsonb)", b).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var replay map[string]any
		json.Unmarshal(raw, &replay)
		if replay["replayed"] != true {
			t.Fatal("same attempt reserved twice")
		}
		if _, err := settleTestAttempt(ctx, f.Platform, id, "succeeded", map[string]int{"model_attempts": 1, "tool_attempts": 0, "token_upper_bound": 0, "cost_upper_micros": 0}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reserveTestAttempt(ctx, f.Platform, scope, "fifth", []string{"account"}); err == nil {
		t.Fatal("known zero usage refunded attempt count")
	}
	input["attempt_id"], input["attempt_no"] = "fifth", 5
	b, _ := json.Marshal(input)
	if _, err := f.Platform.Exec(ctx, "SELECT control.reserve_attempt($1::jsonb)", b); err == nil {
		t.Fatal("fifth physical attempt allowed")
	}
}

func TestBudgetControlRPCUsesResourceLedgerWithoutProviderBudget(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	if _, err := f.Admin.Exec(ctx, `UPDATE control.run_guards SET budget_limits='{"model_attempts":0,"tool_attempts":0,"token_upper_bound":0,"cost_upper_micros":0}'`); err != nil {
		t.Fatal(err)
	}
	scope := issueTestScope(t, f, "scope", "platform")
	call := func(id string) error {
		b, _ := json.Marshal(map[string]any{"attempt_id": id, "scope_id": scope["scope_id"], "scope_hash": scope["scope_hash"], "capability": "control_rpc", "target": "account", "logical_call_key": id, "attempt_no": 1, "owner_job_id": "dispatch", "owner_epoch": 1, "resource_keys": []string{"account"}, "reservation": map[string]int{"model_attempts": 0, "tool_attempts": 0, "token_upper_bound": 0, "cost_upper_micros": 0}, "hold_until": time.Now().Add(time.Minute)})
		_, err := f.Platform.Exec(ctx, "SELECT control.reserve_attempt($1::jsonb)", b)
		return err
	}
	if err := call("poll-1"); err != nil {
		t.Fatalf("control RPC counted as a provider attempt: %v", err)
	}
	if err := call("poll-2"); err != nil {
		t.Fatal(err)
	}
	if err := call("poll-3"); err == nil {
		t.Fatal("control RPC bypassed resource concurrency")
	}
	if _, err := settleTestAttempt(ctx, f.Platform, "poll-1", "succeeded", nil); err != nil {
		t.Fatal(err)
	}
	if err := call("poll-3"); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := f.Admin.QueryRow(ctx, "SELECT sum((reservation->>'model_attempts')::int+(reservation->>'tool_attempts')::int) FROM control.outbound_calls").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("RPC consumed provider budget: %d %v", attempts, err)
	}
}
