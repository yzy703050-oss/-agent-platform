package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
	pg "agent-platform/backend/internal/repository/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	pool         *pgxpool.Pool
	repo         *pg.Repository
	scope        model.Scope
	conversation string
}

func TestFinalizePauseRequiresCheckpoint(t *testing.T) {
	f := setup(t)
	r := f.admit(t)
	ctx := context.Background()
	if _, e := f.pool.Exec(ctx, "UPDATE agent_runs SET status='pausing' WHERE id=$1", r.ID); e != nil {
		t.Fatal(e)
	}
	in := model.FinalizePauseInput{Scope: f.scope, RunID: r.ID, Attempt: r.Attempt}
	if _, e := f.repo.FinalizePause(ctx, in); !errors.Is(e, model.ErrConflict) {
		t.Fatalf("without checkpoint: %v", e)
	}
	if e := f.repo.SaveCheckpoint(ctx, model.CheckpointWrite{Scope: f.scope, Checkpoint: model.Checkpoint{RunID: r.ID, Attempt: r.Attempt, Payload: []byte("checkpoint"), Engine: "eino", EngineVersion: "v0.9.0-beta.1", FormatVersion: 1, Fingerprint: "test"}}); e != nil {
		t.Fatal(e)
	}
	paused, e := f.repo.FinalizePause(ctx, in)
	if e != nil || paused.Status != "paused" {
		t.Fatalf("pause: %+v %v", paused, e)
	}
}
func TestSupersedeRollbackPreservesOldTask(t *testing.T) {
	f := setup(t)
	r := f.admit(t)
	ctx := context.Background()
	if _, e := f.pool.Exec(ctx, "UPDATE agent_runs SET status='paused' WHERE id=$1", r.ID); e != nil {
		t.Fatal(e)
	}
	_, e := f.pool.Exec(ctx, `CREATE FUNCTION reject_new_message() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced admission rollback'; END $$;
 CREATE TRIGGER reject_new_message BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION reject_new_message();`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.SupersedePausedRun(ctx, f.input("new-key", "question")); e == nil {
		t.Fatal("expected admission failure")
	}
	var status string
	var eligible bool
	if e = f.pool.QueryRow(ctx, "SELECT status,context_eligible FROM agent_runs WHERE id=$1", r.ID).Scan(&status, &eligible); e != nil {
		t.Fatal(e)
	}
	if status != "paused" || !eligible {
		t.Fatalf("old task changed despite rollback: %s %v", status, eligible)
	}
}
func TestMigrationIdempotent(t *testing.T) {
	f := setup(t)
	if e := pg.Migrate(context.Background(), f.pool); e != nil {
		t.Fatal(e)
	}
}

func setup(t *testing.T) *fixture {
	return setupMigrations(t, pg.Migrate)
}

func setupMigrations(t *testing.T, migrate func(context.Context, *pgxpool.Pool) error) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_DATABASE_TESTS") == "1" {
			t.Fatal("TEST_DATABASE_URL is required for release verification")
		}
		t.Skip("real PostgreSQL required: set TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	if e = migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	f := &fixture{pool: pool, repo: pg.New(pool), scope: model.Scope{UserID: "user-a", SessionID: "session-a", PageID: "page-a"}, conversation: "conversation-a"}
	_, e = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash) VALUES('user-a','a','test'),('user-b','b','test');
 INSERT INTO conversations(id,user_id,agent_type,title) VALUES('conversation-a','user-a','general','test');
 INSERT INTO sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES('session-a','user-a','token','csrf',now()+interval '1 hour');
 INSERT INTO page_leases(id,user_id,session_id,page_token_hash,expires_at) VALUES('page-a','user-a','session-a','page-token',now()+interval '1 hour');`)
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixture) input(key, content string) model.AdmitRunInput {
	return model.AdmitRunInput{Scope: f.scope, ConversationID: f.conversation, IdempotencyKey: key, Content: content, RequestHash: content}
}
func (f *fixture) admit(t *testing.T) model.Run {
	t.Helper()
	r, e := f.repo.AcceptRun(context.Background(), f.input("key", "old-task-secret"))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestConcurrentIdempotentAdmission(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.repo.AcceptRun(context.Background(), f.input("key", "question"))
			if e != nil {
				t.Errorf("admit: %v", e)
				return
			}
			ids <- r.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first != "" && id != first {
			t.Fatal("duplicate runs")
		}
		first = id
	}
	var n int
	f.pool.QueryRow(context.Background(), "SELECT count(*) FROM messages").Scan(&n)
	if n != 1 {
		t.Fatalf("messages=%d", n)
	}
}
func TestSameKeyDifferentBody(t *testing.T) {
	f := setup(t)
	f.admit(t)
	_, e := f.repo.AcceptRun(context.Background(), f.input("key", "different"))
	if !errors.Is(e, model.ErrIdempotencyConflict) {
		t.Fatal(e)
	}
}
func TestSameKeyDifferentPageReturnsOriginalOwner(t *testing.T) {
	f := setup(t)
	r := f.admit(t)
	in := f.input("key", "old-task-secret")
	in.Scope.PageID = "page-b"
	other, e := f.repo.AcceptRun(context.Background(), in)
	if e != nil || r.ID != other.ID || other.PageID != "page-a" {
		t.Fatalf("owner changed: %+v %v", other, e)
	}
}
func TestSingleNonterminalRun(t *testing.T) {
	f := setup(t)
	f.admit(t)
	_, e := f.repo.AcceptRun(context.Background(), f.input("other", "question"))
	if !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
}
func TestStaleAttemptRejected(t *testing.T) {
	f := setup(t)
	r := f.admit(t)
	f.pool.Exec(context.Background(), "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	cp := model.CheckpointWrite{Scope: f.scope, Checkpoint: model.Checkpoint{RunID: r.ID, Attempt: 0, Payload: []byte("old"), Valid: true}}
	if e := f.repo.SaveCheckpoint(context.Background(), cp); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
}
func TestCompleteTransactionRollback(t *testing.T) {
	f := setup(t)
	r := f.admit(t)
	ctx := context.Background()
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	_, e := f.pool.Exec(ctx, `CREATE FUNCTION reject_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='run.completed' THEN RAISE EXCEPTION 'forced rollback'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_complete BEFORE INSERT ON run_events FOR EACH ROW EXECUTE FUNCTION reject_complete();`)
	if e != nil {
		t.Fatal(e)
	}
	e = f.repo.CompleteRun(ctx, model.FinishRunInput{Scope: f.scope, RunID: r.ID, Attempt: r.Attempt, FinalText: "answer"})
	if e == nil {
		t.Fatal("expected failure")
	}
	var status string
	f.pool.QueryRow(ctx, "SELECT status FROM agent_runs WHERE id=$1", r.ID).Scan(&status)
	var n int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE role='assistant'").Scan(&n)
	if status != "running" || n != 0 {
		t.Fatalf("partial commit: %s %d", status, n)
	}
}
func TestSupersedeAtomicity(t *testing.T) {
	f := setup(t)
	r := f.admit(t)
	ctx := context.Background()
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	e := f.repo.SaveCheckpoint(ctx, model.CheckpointWrite{Scope: f.scope, Checkpoint: model.Checkpoint{RunID: r.ID, Attempt: r.Attempt, Payload: []byte("payload"), Valid: true, Engine: "eino", EngineVersion: "v0.9.0-beta.1", FormatVersion: 1, Fingerprint: "test"}})
	if e != nil {
		t.Fatal(e)
	}
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='paused' WHERE id=$1", r.ID)
	in := f.input("new", "fresh question")
	newRun, e := f.repo.SupersedePausedRun(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	var status string
	var eligible bool
	f.pool.QueryRow(ctx, "SELECT status,context_eligible FROM agent_runs WHERE id=$1", r.ID).Scan(&status, &eligible)
	if status != "cancelled" || eligible {
		t.Fatal("old run still eligible")
	}
	_, ok, e := f.repo.GetCheckpoint(ctx, f.scope, r.ID)
	if e != nil || ok {
		t.Fatalf("old checkpoint active: %v %v", ok, e)
	}
	msgs, e := f.repo.ListContext(ctx, f.scope, f.conversation, newRun.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "old-task-secret") {
			t.Fatal("old task in context")
		}
	}
	var n int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&n)
	if n != 2 {
		t.Fatal("history was removed")
	}
}
func TestCrossUserCheckpointDenied(t *testing.T) {
	f := setup(t)
	r := f.admit(t)
	scope := f.scope
	scope.UserID = "user-b"
	_, _, e := f.repo.GetCheckpoint(context.Background(), scope, r.ID)
	if !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
}
