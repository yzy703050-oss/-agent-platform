package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	chatapp "agent-platform/backend/internal/application/chat"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/router"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
	"github.com/google/uuid"
)

type heldAgent struct {
	starts  atomic.Int32
	inputs  chan contracts.RunInput
	release chan struct{}
}
type heldExecution struct {
	owner *heldAgent
	input contracts.RunInput
}

func (a *heldAgent) NewExecution(_ context.Context, in contracts.RunInput) (contracts.Execution, error) {
	return &heldExecution{a, in}, nil
}
func (e *heldExecution) Start(ctx context.Context, _ func(contracts.Event) error) (contracts.ExecutionResult, error) {
	e.owner.starts.Add(1)
	e.owner.inputs <- e.input
	select {
	case <-e.owner.release:
		return contracts.ExecutionResult{FinalText: "answer"}, nil
	case <-ctx.Done():
		return contracts.ExecutionResult{}, ctx.Err()
	}
}
func (e *heldExecution) Pause(context.Context) (contracts.PauseResult, error) {
	return contracts.PauseResult{}, errors.New("not used")
}
func (e *heldExecution) Cancel(context.Context) error { return nil }
func chatFixture(t *testing.T) (*fixture, *manager.Manager, *chatapp.Application, *heldAgent) {
	f := setup(t)
	a := &heldAgent{inputs: make(chan contracts.RunInput, 20), release: make(chan struct{})}
	reg := manager.NewRegistry()
	reg.Register("general", a)
	m := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{Iterations: 8, ExecutionTimeout: time.Minute, ToolTimeout: time.Second})
	t.Cleanup(func() { close(a.release); m.Close() })
	return f, m, chatapp.New(convsvc.New(f.repo), m), a
}
func submit(t *testing.T, f *fixture, app *chatapp.Application, key string) (model.Run, error) {
	t.Helper()
	return app.Submit(context.Background(), f.scope, chatapp.SubmitInput{ConversationID: f.conversation, PageID: f.scope.PageID, Content: "new question", IdempotencyKey: key})
}
func takeInput(t *testing.T, a *heldAgent) contracts.RunInput {
	t.Helper()
	select {
	case in := <-a.inputs:
		return in
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not start")
		return contracts.RunInput{}
	}
}
func TestSubmitIdempotentStartsOnce(t *testing.T) {
	f, _, app, a := chatFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := submit(t, f, app, "same")
			if e != nil {
				t.Error(e)
				return
			}
			ids <- r.ID
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("duplicate run")
		}
		id = got
	}
	takeInput(t, a)
	if a.starts.Load() != 1 {
		t.Fatalf("starts: %d", a.starts.Load())
	}
}

func TestChatAgentReadiness(t *testing.T) {
	for _, ready := range []string{"general", "travel"} {
		t.Run(ready, func(t *testing.T) {
			ctx := context.Background()
			f := setup(t)
			a := &heldAgent{inputs: make(chan contracts.RunInput, 2), release: make(chan struct{})}
			reg := manager.NewRegistry()
			reg.Register(ready, a)
			m := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{Iterations: 8, ExecutionTimeout: time.Minute})
			defer m.Close()
			defer close(a.release)
			convs := convsvc.New(f.repo)
			app := chatapp.New(convs, m)
			for _, kind := range []string{"general", "travel"} {
				c, err := convs.Create(ctx, f.scope, kind, "entry")
				if err != nil {
					t.Fatal(err)
				}
				_, err = app.Submit(ctx, f.scope, chatapp.SubmitInput{ConversationID: c.ID, Content: "new task", PageID: f.scope.PageID, IdempotencyKey: kind})
				if kind == ready {
					if err != nil {
						t.Fatalf("ready %s rejected: %v", kind, err)
					}
					takeInput(t, a)
				} else if err == nil {
					t.Fatalf("unavailable %s executed", kind)
				}
			}
			auth := authsvc.New(f.repo)
			if _, err := auth.Register(ctx, "web-user", "password-123"); err != nil {
				t.Fatal(err)
			}
			h := router.New(router.Dependencies{Auth: auth, Users: usersvc.New(f.repo), Conversations: convs, Settings: settingssvc.New(f.repo), Manager: m, Chat: app})
			cookie, _ := webLogin(t, h)
			w := request(h, "GET", "/api/v1/agents", "", cookie, "")
			var out struct {
				Items []struct {
					ID        string `json:"id"`
					Available bool   `json:"available"`
				}
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
				t.Fatal(w.Body.String())
			}
			for _, item := range out.Items {
				if item.Available != (item.ID == ready) {
					t.Fatalf("wrong per-agent readiness: %s", w.Body.String())
				}
			}
		})
	}
}
func TestConcurrentConversationConflict(t *testing.T) {
	f, _, app, a := chatFixture(t)
	if _, e := submit(t, f, app, "one"); e != nil {
		t.Fatal(e)
	}
	takeInput(t, a)
	if _, e := submit(t, f, app, "two"); !errors.Is(e, model.ErrConflict) {
		t.Fatalf("expected conflict: %v", e)
	}
}
func TestUserRunQuota(t *testing.T) {
	f, _, app, a := chatFixture(t)
	for i := 0; i < 4; i++ {
		c, e := convsvc.New(f.repo).Create(context.Background(), f.scope, "general", "quota")
		if e != nil {
			t.Fatal(e)
		}
		f.conversation = c.ID
		_, e = submit(t, f, app, uuid.NewString())
		if i == 3 {
			if !errors.Is(e, model.ErrQuota) {
				t.Fatalf("quota: %v", e)
			}
		} else {
			if e != nil {
				t.Fatal(e)
			}
			takeInput(t, a)
		}
	}
}
func TestFinalPersistFailureNotSuccess(t *testing.T) {
	f, m, app, a := chatFixture(t)
	r, e := submit(t, f, app, "one")
	if e != nil {
		t.Fatal(e)
	}
	takeInput(t, a)
	_, e = f.pool.Exec(context.Background(), `CREATE FUNCTION reject_answer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.role='assistant' THEN RAISE EXCEPTION 'disk failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_answer BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION reject_answer();`)
	if e != nil {
		t.Fatal(e)
	}
	a.release <- struct{}{}
	m.Wait()
	got, e := f.repo.GetRun(context.Background(), f.scope, r.ID)
	if e != nil || got.Status != "failed" {
		t.Fatalf("got %+v %v", got, e)
	}
	var n int
	if e = f.pool.QueryRow(context.Background(), "SELECT count(*) FROM run_events WHERE run_id=$1 AND type='run.completed'", r.ID).Scan(&n); e != nil || n != 0 {
		t.Fatalf("published success: %d %v", n, e)
	}
}
func TestSupersededRunVisibleButExcluded(t *testing.T) {
	f, _, app, a := chatFixture(t)
	ctx := context.Background()
	early := f.admit(t)
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", early.ID)
	if e := f.repo.CompleteRun(ctx, model.FinishRunInput{Scope: f.scope, RunID: early.ID, Attempt: 1, FinalText: "earlier-valid"}); e != nil {
		t.Fatal(e)
	}
	old, e := f.repo.AcceptRun(ctx, f.input("old", "replace-me-secret"))
	if e != nil {
		t.Fatal(e)
	}
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='paused' WHERE id=$1", old.ID)
	if _, e = submit(t, f, app, "new"); e != nil {
		t.Fatal(e)
	}
	in := takeInput(t, a)
	joined := ""
	for _, v := range in.Messages {
		joined += v.Content
	}
	if strings.Contains(joined, "replace-me-secret") || !strings.Contains(joined, "earlier-valid") {
		t.Fatalf("bad context: %s", joined)
	}
	history, e := f.repo.ListMessages(ctx, f.scope, f.conversation, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, v := range history {
		found = found || v.Content == "replace-me-secret"
	}
	if !found {
		t.Fatal("history lost")
	}
}
func TestLateOldRunCannotPolluteNew(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	old := f.admit(t)
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='paused' WHERE id=$1", old.ID)
	newer, e := f.repo.SupersedePausedRun(ctx, f.input("new", "new question"))
	if e != nil {
		t.Fatal(e)
	}
	e = f.repo.AppendRunMessage(ctx, f.scope, old.ID, old.Attempt, model.Message{Role: "tool", Content: "late-secret", ToolCallID: "old"})
	if !errors.Is(e, model.ErrConflict) {
		t.Fatalf("late callback: %v", e)
	}
	msgs, e := f.repo.ListContext(ctx, f.scope, f.conversation, newer.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range msgs {
		if strings.Contains(v.Content, "late-secret") {
			t.Fatal("context contaminated")
		}
	}
}
func TestCompletionCancelRaceHasOneTerminalOutcome(t *testing.T) {
	f, m, app, a := chatFixture(t)
	ctx := context.Background()
	r, e := submit(t, f, app, "race")
	if e != nil {
		t.Fatal(e)
	}
	takeInput(t, a)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		results <- f.repo.CompleteRun(ctx, model.FinishRunInput{Scope: f.scope, RunID: r.ID, Attempt: 1, FinalText: "committed answer"})
	}()
	go func() { <-start; _, e := m.Cancel(ctx, f.scope, r.ID); results <- e }()
	close(start)
	for i := 0; i < 2; i++ {
		if e := <-results; e != nil && !errors.Is(e, model.ErrConflict) {
			t.Fatal(e)
		}
	}
	got, e := f.repo.GetRun(ctx, f.scope, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	var messages, completed int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE run_id=$1 AND role='assistant'", r.ID).Scan(&messages)
	f.pool.QueryRow(ctx, "SELECT count(*) FROM run_events WHERE run_id=$1 AND type='run.completed'", r.ID).Scan(&completed)
	if got.Status == "succeeded" {
		if messages != 1 || completed != 1 {
			t.Fatal("incomplete success transaction")
		}
	} else if got.Status == "cancelled" {
		if messages != 0 || completed != 0 {
			t.Fatal("late success polluted cancellation")
		}
	} else {
		t.Fatalf("nonterminal race outcome: %+v", got)
	}
}
