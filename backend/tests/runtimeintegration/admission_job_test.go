package runtimeintegration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/platformrun"
	"agent-platform/backend/internal/repository/platformpg"
	"github.com/jackc/pgx/v5/pgxpool"
)

func entryConfig() platformrun.Config {
	return platformrun.Config{KernelVersion: "entry-v1", ConfigFingerprint: "fixture-entry-v1", MaxModelInputTokens: 20000, MaxModelOutputTokens: 8000,
		Budget: platformrun.Budget{ModelAttempts: 32, ToolAttempts: 64, TokenUpperBound: 500000, CostUpperMicros: 1000000}, ScopeTargets: []string{"weather", "model:fixture"}}
}

func entryFixture(t *testing.T, cfg platformrun.Config) (*runtimeFixture, *platformpg.Repository, *platformrun.Service, model.Scope) {
	t.Helper()
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	auth := platformpg.New(f.Platform)
	if err := auth.CreateUser(ctx, model.User{ID: "user", Username: "alice"}, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := auth.CreateSession(ctx, model.Session{ID: "session", UserID: "user", TokenHash: "token", CSRFHash: "csrf", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Admin.Exec(ctx, `INSERT INTO platform.conversations(id,user_id,agent_type,title) VALUES('conversation','user','general','test'),('second','user','travel','test'),('third','user','general','test');INSERT INTO control.resource_limits(resource_key,max_concurrency) VALUES('run:active_per_user',2)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"conversation", "second", "third"} {
		if _, err := f.Platform.Exec(ctx, "SELECT control.set_context_guard('user',$1,0,1,true)", id); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := platformpg.NewRuntime(f.Platform, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f, repo, platformrun.New(repo), model.Scope{UserID: "user", SessionID: "session"}
}

func admitEntry(t *testing.T, api *platformrun.Service, scope model.Scope) runtimev1.RunView {
	t.Helper()
	run, err := api.Admit(context.Background(), scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "request", Content: "查询北京天气"})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func claimEntry(t *testing.T, repo *platformpg.Repository, worker string) runtimev1.JobLease {
	t.Helper()
	lease, ok, err := repo.ClaimEntry(context.Background(), worker)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	return lease
}

func finishEntry(lease runtimev1.JobLease, text string) platformrun.FinishInput {
	return platformrun.FinishInput{Status: "succeeded", FinalText: text, ExpectedRunVersion: lease.RunVersion}
}

func TestAdmitIdempotencyAndConflict(t *testing.T) {
	f, repo, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan runtimev1.RunView, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run, err := api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "request", Content: "查询北京天气"})
			results <- run
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for run := range results {
		if id == "" {
			id = run.ID
		}
		if run.ID != id {
			t.Fatal("duplicate Run admitted")
		}
	}
	var messages, jobs, guards int
	if err := f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM platform.messages),(SELECT count(*) FROM platform.agent_jobs),(SELECT count(*) FROM control.run_guards)").Scan(&messages, &jobs, &guards); err != nil || messages != 1 || jobs != 1 || guards != 1 {
		t.Fatalf("duplicates: messages=%d jobs=%d guards=%d err=%v", messages, jobs, guards, err)
	}
	if _, err := api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "request", Content: "改查上海"}); !errors.Is(err, model.ErrIdempotencyConflict) {
		t.Fatalf("same key changed content: %v", err)
	}
	if _, err := api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "other", Content: "新问题"}); !errors.Is(err, model.ErrRunConflict) {
		t.Fatalf("active Run conflict: %v", err)
	}
	if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_jobs SET status='waiting';UPDATE platform.agent_runs SET status='waiting'"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "new", Content: "等待时的新问题"}); !errors.Is(err, model.ErrRunConflict) {
		t.Fatalf("waiting lost exclusion: %v", err)
	}
	if _, err := api.Admit(ctx, model.Scope{UserID: "other", SessionID: "session"}, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "x", Content: "越权"}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("cross-user admission: %v", err)
	}
	if _, ok, err := repo.ClaimEntry(ctx, "worker"); err != nil || ok {
		t.Fatalf("waiting Job claimed: %v %v", ok, err)
	}
}

func TestDeadlineStartsAtAdmission(t *testing.T) {
	f, repo, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	run := admitEntry(t, api, scope)
	var admitted, deadline time.Time
	if err := f.Admin.QueryRow(ctx, "SELECT created_at,deadline_at FROM platform.agent_runs WHERE id=$1", run.ID).Scan(&admitted, &deadline); err != nil {
		t.Fatal(err)
	}
	if deadline.Sub(admitted) != 300*time.Second || !deadline.Equal(run.DeadlineAt) {
		t.Fatalf("deadline not based on admission: %s", deadline.Sub(admitted))
	}
	if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_jobs SET not_before=clock_timestamp()+interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.ClaimEntry(ctx, "worker"); err != nil || ok {
		t.Fatalf("future Job claimed: %v %v", ok, err)
	}
	if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_jobs SET not_before=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	changed := entryConfig()
	changed.RunTimeout = 600 * time.Second
	changed.Budget.ModelAttempts = 100
	changed.Budget.TokenUpperBound = 1000000
	other, err := platformpg.NewRuntime(f.Platform, changed)
	if err != nil {
		t.Fatal(err)
	}
	lease := claimEntry(t, other, "worker")
	if !lease.DeadlineAt.Equal(deadline) || lease.Limits.MaxModelAttempts != 32 || lease.Limits.TokenUpperBound != 500000 {
		t.Fatalf("queue/config change reset original limits: %+v", lease)
	}
	renewed, err := other.RenewEntry(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Ref.Lease.Epoch != lease.Ref.Lease.Epoch || renewed.RunVersion != lease.RunVersion || !renewed.DeadlineAt.Equal(deadline) {
		t.Fatal("renew reset epoch, version or deadline")
	}
}

// Only the storage compatibility gate is exercised here. A08 owns actual Eino
// payload decoding/resume; a claimed metadata fixture is never executed.
func seedEntryCheckpoint(t *testing.T, f *runtimeFixture, run runtimev1.RunView, lease runtimev1.JobLease) {
	t.Helper()
	payload := []byte(`{"a04_metadata_fixture":true}`)
	sum := sha256.Sum256(payload)
	ctx := context.Background()
	if _, err := f.Admin.Exec(ctx, `INSERT INTO platform.checkpoints(job_id,checkpoint_version,run_id,user_id,conversation_id,thread_id,lease_epoch,engine,engine_version,format_version,fingerprint,payload,payload_hash,interrupts,context_epoch) VALUES($1,1,$2,'user','conversation','conversation',$3,'eino','v0.9.0-beta.1',1,'fixture-entry-v1',$4,$5,'{}',1)`, lease.Ref.OwnerID, run.ID, lease.Ref.Lease.Epoch, payload, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_jobs SET checkpoint_version=1,lease_expires_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredEpochCannotRenewOrCommit(t *testing.T) {
	f, repo, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	run := admitEntry(t, api, scope)
	old := claimEntry(t, repo, "first")
	seedEntryCheckpoint(t, f, run, old)
	if _, err := repo.RenewEntry(ctx, old); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expired lease revived without successor: %v", err)
	}
	if err := repo.FinishRun(ctx, old.Ref, finishEntry(old, "过期输出")); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expired writer committed: %v", err)
	}
	fresh := claimEntry(t, repo, "second")
	if fresh.Ref.OwnerID != old.Ref.OwnerID || fresh.Ref.Lease.Epoch != old.Ref.Lease.Epoch+1 || fresh.CheckpointVersion != 1 || !fresh.DeadlineAt.Equal(old.DeadlineAt) {
		t.Fatal("recovery replaced identity or reset deadline")
	}
	stale := finishEntry(fresh, "旧实例输出")
	if err := repo.FinishRun(ctx, old.Ref, stale); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("old epoch committed: %v", err)
	}
	stale.ExpectedRunVersion = old.RunVersion
	if err := repo.FinishRun(ctx, fresh.Ref, stale); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("old Run version committed: %v", err)
	}
	if err := repo.FinishRun(ctx, fresh.Ref, finishEntry(fresh, "真实恢复输出")); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalMessageEventAtomicity(t *testing.T) {
	for _, fault := range []string{"message", "event"} {
		t.Run(fault, func(t *testing.T) {
			f, repo, api, scope := entryFixture(t, entryConfig())
			ctx := context.Background()
			run := admitEntry(t, api, scope)
			lease := claimEntry(t, repo, "worker")
			statement := `CREATE FUNCTION platform.fail_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture storage unavailable'; END $$; CREATE TRIGGER fail_finish BEFORE INSERT ON platform.messages FOR EACH ROW WHEN (NEW.role='assistant') EXECUTE FUNCTION platform.fail_finish()`
			if fault == "event" {
				statement = `CREATE FUNCTION platform.fail_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture storage unavailable'; END $$; CREATE TRIGGER fail_finish BEFORE INSERT ON platform.run_events FOR EACH ROW WHEN (NEW.type='run.succeeded') EXECUTE FUNCTION platform.fail_finish()`
			}
			if _, err := f.Admin.Exec(ctx, statement); err != nil {
				t.Fatal(err)
			}
			if err := repo.FinishRun(ctx, lease.Ref, finishEntry(lease, "完整回答")); err == nil {
				t.Fatal("storage failure reported completion")
			}
			var status string
			var active bool
			var assistant, terminal int
			var next int64
			if err := f.Admin.QueryRow(ctx, `SELECT r.status,r.next_event_seq,g.active,(SELECT count(*) FROM platform.messages m WHERE m.run_id=r.id AND role='assistant'),(SELECT count(*) FROM platform.run_events e WHERE e.run_id=r.id AND e.type='run.succeeded') FROM platform.agent_runs r JOIN control.run_guards g ON g.source_run_id=r.id WHERE r.id=$1`, run.ID).Scan(&status, &next, &active, &assistant, &terminal); err != nil || status != "running" || !active || assistant != 0 || terminal != 0 || next != 3 {
				t.Fatalf("partial finalization: %s next=%d active=%v assistant=%d terminal=%d err=%v", status, next, active, assistant, terminal, err)
			}
			table := "platform.messages"
			if fault == "event" {
				table = "platform.run_events"
			}
			if _, err := f.Admin.Exec(ctx, "DROP TRIGGER fail_finish ON "+table); err != nil {
				t.Fatal(err)
			}
			if err := repo.FinishRun(ctx, lease.Ref, finishEntry(lease, "完整回答")); err != nil {
				t.Fatal(err)
			}
			if err := repo.FinishRun(ctx, lease.Ref, finishEntry(lease, "迟到覆盖")); !errors.Is(err, model.ErrConflict) {
				t.Fatalf("terminal overwritten: %v", err)
			}
			duplicate, err := api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "request", Content: "查询北京天气"})
			if err != nil || duplicate.ID != run.ID || duplicate.Status != "succeeded" {
				t.Fatalf("terminal idempotency failed: %+v %v", duplicate, err)
			}
		})
	}
}

func TestTerminalMessageRejectsDeadlineCrossedDuringStorage(t *testing.T) {
	cfg := entryConfig()
	cfg.RunTimeout = time.Second
	cfg.ToolTimeout = 10 * time.Millisecond
	f, repo, api, scope := entryFixture(t, cfg)
	ctx := context.Background()
	run := admitEntry(t, api, scope)
	lease := claimEntry(t, repo, "worker")
	if _, err := f.Admin.Exec(ctx, `CREATE FUNCTION platform.delay_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM pg_sleep(GREATEST(0,extract(epoch FROM deadline_at-clock_timestamp()))+0.02) FROM platform.agent_runs WHERE id=NEW.run_id;
 RETURN NEW; END $$;
 CREATE TRIGGER delay_finish BEFORE INSERT ON platform.messages FOR EACH ROW WHEN(NEW.role='assistant') EXECUTE FUNCTION platform.delay_finish()`); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishRun(ctx, lease.Ref, finishEntry(lease, "超过期限才保存的输出")); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("storage wait crossed deadline: %v", err)
	}
	var messages int
	if err := f.Admin.QueryRow(ctx, "SELECT count(*) FROM platform.messages WHERE run_id=$1 AND role='assistant'", run.ID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("late success saved: %d %v", messages, err)
	}
	if _, err := f.Admin.Exec(ctx, "DROP TRIGGER delay_finish ON platform.messages"); err != nil {
		t.Fatal(err)
	}
	if changed, err := repo.CloseInvalidRun(ctx, run.ID); err != nil || !changed {
		t.Fatalf("expired Run not closed: %v %v", changed, err)
	}
}

func TestTerminalMessageConcurrentFinishers(t *testing.T) {
	f, repo, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	run := admitEntry(t, api, scope)
	lease := claimEntry(t, repo, "worker")
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, input := range []platformrun.FinishInput{finishEntry(lease, "最终回答"), {Status: "failed", Code: "provider_failed", FinalText: "已保存部分结果", ExpectedRunVersion: lease.RunVersion}} {
		go func() { <-start; results <- repo.FinishRun(ctx, lease.Ref, input) }()
	}
	close(start)
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if !errors.Is(err, model.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("terminal winners=%d", success)
	}
	var assistant, events int
	var active bool
	if err := f.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM platform.messages WHERE run_id=$1 AND role='assistant'),(SELECT count(*) FROM platform.run_events WHERE run_id=$1 AND type IN('run.succeeded','run.failed')),(SELECT active FROM control.run_guards WHERE source_run_id=$1)`, run.ID).Scan(&assistant, &events, &active); err != nil || assistant != 1 || events != 1 || active {
		t.Fatalf("non-atomic terminal winner: messages=%d events=%d active=%v err=%v", assistant, events, active, err)
	}
}

func TestAdmitSharedQuotaIncludesWaiting(t *testing.T) {
	f, repo, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	first := admitEntry(t, api, scope)
	if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_runs SET status='waiting';UPDATE platform.agent_jobs SET status='waiting'"); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, f.Platform.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	otherConfig := entryConfig()
	otherConfig.Budget.ModelAttempts = 64
	otherRepo, err := platformpg.NewRuntime(pool, otherConfig)
	if err != nil {
		t.Fatal(err)
	}
	second, err := platformrun.New(otherRepo).Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "second", IdempotencyKey: "second", Content: "另一个入口的问题"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "third", IdempotencyKey: "third", Content: "第三个问题"}); !errors.Is(err, model.ErrQuota) {
		t.Fatalf("waiting refunded active quota: %v", err)
	}
	lease := claimEntry(t, repo, "worker")
	if lease.Ref.ThreadID != "second" {
		t.Fatal("claimed waiting thread")
	}
	if err = repo.FinishRun(ctx, lease.Ref, finishEntry(lease, "第二个问题的答案")); err != nil {
		t.Fatal(err)
	}
	if _, err = api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "third", IdempotencyKey: "third", Content: "第三个问题"}); err != nil {
		t.Fatalf("terminal did not release active quota: %v", err)
	}
	var count int
	if err = f.Admin.QueryRow(ctx, "SELECT count(*) FROM platform.agent_runs WHERE id IN($1,$2)", first.ID, second.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("quota cleanup removed history")
	}
}

func TestExpiredEpochSystemClosure(t *testing.T) {
	for _, reason := range []string{"deadline", "session", "context"} {
		t.Run(reason, func(t *testing.T) {
			cfg := entryConfig()
			if reason == "deadline" {
				cfg.RunTimeout = 250 * time.Millisecond
				cfg.ToolTimeout = 10 * time.Millisecond
			}
			f, repo, api, scope := entryFixture(t, cfg)
			ctx := context.Background()
			run := admitEntry(t, api, scope)
			lease := claimEntry(t, repo, "worker")
			maintenance := platformpg.New(f.Maintenance)
			if changed, err := maintenance.CloseInvalidRun(ctx, run.ID); err != nil || changed {
				t.Fatalf("valid Run closed by cleanup: %v %v", changed, err)
			}
			if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_runs SET partial_output='已有真实部分结果' WHERE id=$1", run.ID); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "deadline":
				if _, err := f.Admin.Exec(ctx, "SELECT pg_sleep(GREATEST(0,extract(epoch FROM deadline_at-clock_timestamp()))) FROM platform.agent_runs WHERE id=$1", run.ID); err != nil {
					t.Fatal(err)
				}
			case "session":
				if err := platformpg.New(f.Platform).RevokeSession(ctx, "token"); err != nil {
					t.Fatal(err)
				}
			case "context":
				if _, err := f.Platform.Exec(ctx, "SELECT control.set_context_guard('user','conversation',1,2,true)"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := repo.RenewEntry(ctx, lease); err == nil {
				t.Fatal("invalid authority renewed")
			}
			if err := repo.FinishRun(ctx, lease.Ref, finishEntry(lease, "迟到正常输出")); err == nil {
				t.Fatal("invalid authority committed success")
			}
			if changed, err := maintenance.CloseInvalidRun(ctx, run.ID); err != nil || !changed {
				t.Fatalf("invalid Run left active: %v %v", changed, err)
			}
			if changed, err := maintenance.CloseInvalidRun(ctx, run.ID); err != nil || changed {
				t.Fatalf("cleanup not idempotent: %v %v", changed, err)
			}
			var status, code, text string
			var running int
			if err := f.Admin.QueryRow(ctx, `SELECT r.status,r.error_code,(SELECT content FROM platform.messages WHERE run_id=r.id AND role='assistant'),(SELECT count(*) FROM platform.agent_jobs WHERE run_id=r.id AND (worker_id IS NOT NULL OR lease_expires_at IS NOT NULL)) FROM platform.agent_runs r WHERE id=$1`, run.ID).Scan(&status, &code, &text, &running); err != nil || running != 0 || text != "已有真实部分结果" {
				t.Fatalf("invalid cleanup: %s/%s %q leases=%d err=%v", status, code, text, running, err)
			}
			want := map[string]string{"deadline": "deadline_exceeded", "session": "auth_revoked", "context": "context_invalidated"}[reason]
			if code != want || (reason == "deadline" && status != "timed_out") || (reason != "deadline" && status != "failed") {
				t.Fatalf("wrong system terminal: %s/%s", status, code)
			}
		})
	}
}

func TestAdmitStoreRejectsForgedHash(t *testing.T) {
	f, repo, _, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	_, err := repo.AdmitRun(ctx, scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "request", Content: "真实用户原文", RequestHash: strings.Repeat("0", 64)})
	if !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("direct store accepted forged immutable hash: %v", err)
	}
	var count int
	if err = f.Admin.QueryRow(ctx, "SELECT count(*) FROM platform.agent_runs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid admission left state: %d %v", count, err)
	}
}

func TestExpiredEpochMissingAndIncompatibleRecovery(t *testing.T) {
	for _, scenario := range []string{"missing", "hash", "format", "config"} {
		t.Run(scenario, func(t *testing.T) {
			f, repo, api, scope := entryFixture(t, entryConfig())
			ctx := context.Background()
			run := admitEntry(t, api, scope)
			lease := claimEntry(t, repo, "old")
			if scenario != "missing" {
				payload := []byte("a04 metadata fixture; never executed")
				sum := sha256.Sum256(payload)
				hash := hex.EncodeToString(sum[:])
				format := 1
				if scenario == "hash" {
					hash = strings.Repeat("0", 64)
				}
				if scenario == "format" {
					format = 2
				}
				if _, err := f.Admin.Exec(ctx, `INSERT INTO platform.checkpoints(job_id,checkpoint_version,run_id,user_id,conversation_id,thread_id,lease_epoch,engine,engine_version,format_version,fingerprint,payload,payload_hash,interrupts,context_epoch) VALUES($1,1,$2,'user','conversation','conversation',1,'eino','v0.9.0-beta.1',$3,'fixture-entry-v1',$4,$5,'{}',1)`, lease.Ref.OwnerID, run.ID, format, payload, hash); err != nil {
					t.Fatal(err)
				}
				if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_jobs SET checkpoint_version=1"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_jobs SET lease_expires_at=clock_timestamp()-interval '1 second'"); err != nil {
				t.Fatal(err)
			}
			if scenario == "config" {
				cfg := entryConfig()
				cfg.ConfigFingerprint = "different-entry"
				var err error
				repo, err = platformpg.NewRuntime(f.Platform, cfg)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, ok, err := repo.ClaimEntry(ctx, "new"); err != nil || ok {
				t.Fatalf("unsafe recovery claimed: %v %v", ok, err)
			}
			var status, code string
			var active bool
			if err := f.Admin.QueryRow(ctx, "SELECT r.status,r.error_code,g.active FROM platform.agent_runs r JOIN control.run_guards g ON g.source_run_id=r.id WHERE r.id=$1", run.ID).Scan(&status, &code, &active); err != nil {
				t.Fatal(err)
			}
			want := "checkpoint_incompatible"
			if scenario == "missing" {
				want = "checkpoint_missing"
			}
			if status != "interrupted" || code != want || active {
				t.Fatalf("false recoverability: %s/%s active=%v", status, code, active)
			}
		})
	}
}

func TestExpiredEpochRecoveryRetriesDoNotReset(t *testing.T) {
	f, repo, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	run := admitEntry(t, api, scope)
	lease := claimEntry(t, repo, "worker")
	seedEntryCheckpoint(t, f, run, lease)
	for retry := 1; retry <= 3; retry++ {
		lease = claimEntry(t, repo, "replacement")
		var count int
		if err := f.Admin.QueryRow(ctx, "SELECT retry_count FROM platform.agent_jobs WHERE id=$1", lease.Ref.OwnerID).Scan(&count); err != nil || count != retry {
			t.Fatalf("recovery reset retry counter: %d want %d err=%v", count, retry, err)
		}
		if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_jobs SET lease_expires_at=clock_timestamp()-interval '1 second'"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := repo.ClaimEntry(ctx, "fifth-attempt"); err != nil || ok {
		t.Fatalf("unbounded Job recovery: %v %v", ok, err)
	}
	var status, code string
	if err := f.Admin.QueryRow(ctx, "SELECT status,error_code FROM platform.agent_runs WHERE id=$1", run.ID).Scan(&status, &code); err != nil || status != "failed" || code != "retry_exhausted" {
		t.Fatalf("retry terminal missing: %s/%s %v", status, code, err)
	}
}

func TestExpiredEpochRejectsInvalidLimitSnapshot(t *testing.T) {
	f, repo, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	run := admitEntry(t, api, scope)
	if _, err := f.Admin.Exec(ctx, "UPDATE platform.agent_runs SET limit_snapshot='{}' WHERE id=$1", run.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.ClaimEntry(ctx, "worker"); err != nil || ok {
		t.Fatalf("invalid saved limits not reliably closed: claimed=%v err=%v", ok, err)
	}
	var status, code string
	if err := f.Admin.QueryRow(ctx, "SELECT status,error_code FROM platform.agent_runs WHERE id=$1", run.ID).Scan(&status, &code); err != nil || status != "interrupted" || code != "limit_snapshot_invalid" {
		t.Fatalf("invalid limits left active: %s/%s %v", status, code, err)
	}
}

func TestAdmitStorageFailureRollsBackAllState(t *testing.T) {
	f, _, api, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	if _, err := f.Admin.Exec(ctx, `CREATE FUNCTION platform.fail_admit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture accepted event failure'; END $$; CREATE TRIGGER fail_admit BEFORE INSERT ON platform.run_events FOR EACH ROW WHEN(NEW.type='run.accepted') EXECUTE FUNCTION platform.fail_admit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: "conversation", IdempotencyKey: "request", Content: "查询北京天气"}); err == nil {
		t.Fatal("storage failure reported accepted")
	}
	for _, table := range []string{"platform.agent_runs", "platform.agent_jobs", "platform.agent_threads", "platform.messages", "control.run_guards", "control.execution_scopes"} {
		var count int
		if err := f.Admin.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("orphan admission in %s: %d %v", table, count, err)
		}
	}
	if _, err := f.Admin.Exec(ctx, "DROP TRIGGER fail_admit ON platform.run_events"); err != nil {
		t.Fatal(err)
	}
	admitEntry(t, api, scope)
}

func TestAdmitQuotaIgnoresConnectionDefaultIsolation(t *testing.T) {
	f, _, _, scope := entryFixture(t, entryConfig())
	ctx := context.Background()
	if _, err := f.Admin.Exec(ctx, "UPDATE control.resource_limits SET max_concurrency=1 WHERE resource_key='run:active_per_user'"); err != nil {
		t.Fatal(err)
	}
	cfg := f.Platform.Config()
	name := f.Principals.Prefix + "-isolation"
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo, err := platformpg.NewRuntime(pool, entryConfig())
	if err != nil {
		t.Fatal(err)
	}
	api := platformrun.New(repo)
	blocker, err := f.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, "SELECT id FROM platform.users WHERE id='user' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, conversation := range []string{"conversation", "second"} {
		go func() {
			_, e := api.Admit(ctx, scope, platformrun.SubmitInput{ConversationID: conversation, IdempotencyKey: "request", Content: "并发问题"})
			results <- e
		}()
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	blocked := 0
	for wait.Err() == nil {
		if err = f.Admin.QueryRow(wait, "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND cardinality(pg_blocking_pids(pid))>0", name).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked == 2 {
			break
		}
	}
	if blocked != 2 {
		t.Fatal("did not observe both admission transactions at quota barrier")
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	accepted, rejected := 0, 0
	for range 2 {
		err = <-results
		if err == nil {
			accepted++
		} else if errors.Is(err, model.ErrQuota) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("connection snapshot bypassed hard quota: accepted=%d rejected=%d", accepted, rejected)
	}
}
