package integration

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/model"
	"context"
	"errors"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pausableAgent struct {
	f         *fixture
	inputs    chan contracts.RunInput
	fail      bool
	claimOnly bool
	starts    atomic.Int32
	events    []contracts.Event
}
type pausableExecution struct {
	a    *pausableAgent
	in   contracts.RunInput
	stop chan struct{}
	once sync.Once
}

func (a *pausableAgent) NewExecution(_ context.Context, in contracts.RunInput) (contracts.Execution, error) {
	return &pausableExecution{a: a, in: in, stop: make(chan struct{})}, nil
}
func (e *pausableExecution) Start(ctx context.Context, emit func(contracts.Event) error) (contracts.ExecutionResult, error) {
	e.a.starts.Add(1)
	for _, event := range e.a.events {
		if err := emit(event); err != nil {
			return contracts.ExecutionResult{}, err
		}
	}
	e.a.inputs <- e.in
	select {
	case <-e.stop:
		return contracts.ExecutionResult{}, nil
	case <-ctx.Done():
		return contracts.ExecutionResult{}, ctx.Err()
	}
}
func (e *pausableExecution) Cancel(context.Context) error {
	e.once.Do(func() { close(e.stop) })
	return nil
}
func (e *pausableExecution) Pause(ctx context.Context) (contracts.PauseResult, error) {
	defer e.Cancel(ctx)
	if e.a.claimOnly {
		return contracts.PauseResult{Stopped: true, CheckpointSaved: true}, nil
	}
	if e.a.fail {
		return contracts.PauseResult{Stopped: true}, errors.New("forced save failure")
	}
	err := e.a.f.repo.SaveCheckpoint(ctx, model.CheckpointWrite{Scope: e.in.Scope, Checkpoint: model.Checkpoint{RunID: e.in.Run.ID, Attempt: e.in.Run.Attempt, Engine: "test", EngineVersion: "1", FormatVersion: 1, Fingerprint: "test", Payload: []byte("state")}})
	return contracts.PauseResult{Stopped: true, CheckpointSaved: err == nil}, err
}
func TestPauseMissingSavedCheckpointInterrupted(t *testing.T) {
	f, m, _, a, _ := recoveryFixture(t)
	a.claimOnly = true
	r := recoveryRun(t, f, m, a)
	got, e := m.Pause(context.Background(), f.scope, r.ID)
	if e != nil || got.Status != "interrupted" {
		t.Fatalf("claimed checkpoint is not proof: %+v %v", got, e)
	}
}
func TestIntermediateMessagesIdempotent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	r := f.admit(t)
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	msg := model.Message{Role: "tool", Content: "stored result", ToolCallID: "call-1"}
	for i := 0; i < 2; i++ {
		if e := f.repo.AppendRunMessage(ctx, f.scope, r.ID, 1, msg); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE run_id=$1 AND role='tool'", r.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate tool result: %d", n)
	}
}
func TestDistinctToolCallGroupsDoNotCollide(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	r := f.admit(t)
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	for _, ids := range [][2]string{{"a/b", "c"}, {"a", "b/c"}} {
		m := model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: ids[0], Name: "get_my_settings", Arguments: "{}"}, {ID: ids[1], Name: "get_my_settings", Arguments: "{}"}}}
		if e := f.repo.AppendRunMessage(ctx, f.scope, r.ID, 1, m); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE run_id=$1 AND role='assistant'", r.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("distinct call groups collided: %d", n)
	}
}
func recoveryFixture(t *testing.T) (*fixture, *manager.Manager, *manager.LeaseManager, *pausableAgent, *time.Time) {
	f := setup(t)
	a := &pausableAgent{f: f, inputs: make(chan contracts.RunInput, 10)}
	reg := manager.NewRegistry()
	reg.Register("general", a)
	m := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{Iterations: 8, ExecutionTimeout: time.Minute, ToolTimeout: time.Second})
	t.Cleanup(m.Close)
	now := time.Now()
	leases := manager.NewLeaseManager(f.repo, m, func() time.Time { return now }, 20*time.Second)
	return f, m, leases, a, &now
}
func recoveryRun(t *testing.T, f *fixture, m *manager.Manager, a *pausableAgent) model.Run {
	t.Helper()
	r, e := m.Submit(context.Background(), f.input(uuid.NewString(), "question"), "general")
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-a.inputs:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	return r
}
func TestPageSwitchKeepsRun(t *testing.T) {
	f, m, l, a, _ := recoveryFixture(t)
	ctx := context.Background()
	p, e := l.Register(ctx, f.scope)
	if e != nil {
		t.Fatal(e)
	}
	f.scope.PageID = p.PageID
	r := recoveryRun(t, f, m, a)
	if _, e = f.repo.CreateConversation(ctx, f.scope, "travel", "another entry"); e != nil {
		t.Fatal(e)
	}
	if e = l.Heartbeat(ctx, f.scope, p.PageID, p.Token); e != nil {
		t.Fatal(e)
	}
	if e = l.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	got, _ := f.repo.GetRun(ctx, f.scope, r.ID)
	if got.Status != "running" {
		t.Fatalf("switched: %+v", got)
	}
}
func TestOwnerPageExpiryPauses(t *testing.T) {
	f, m, l, a, now := recoveryFixture(t)
	ctx := context.Background()
	owner, e := l.Register(ctx, f.scope)
	if e != nil {
		t.Fatal(e)
	}
	f.scope.PageID = owner.PageID
	r := recoveryRun(t, f, m, a)
	other, e := l.Register(ctx, f.scope)
	if e != nil {
		t.Fatal(e)
	}
	*now = now.Add(21 * time.Second)
	if e = l.Heartbeat(ctx, f.scope, other.PageID, other.Token); e == nil {
		t.Fatal("expired lease revived")
	}
	if e = l.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	got, _ := f.repo.GetRun(ctx, f.scope, r.ID)
	if got.Status != "paused" {
		t.Fatalf("owner expired: %+v", got)
	}
}
func TestOtherPageCannotHeartbeatOwner(t *testing.T) {
	f, _, l, _, _ := recoveryFixture(t)
	ctx := context.Background()
	one, _ := l.Register(ctx, f.scope)
	two, _ := l.Register(ctx, f.scope)
	if e := l.Heartbeat(ctx, f.scope, one.PageID, two.Token); !errors.Is(e, model.ErrNotFound) {
		t.Fatalf("wrong token: %v", e)
	}
}
func TestNewDocumentGetsDistinctPageIdentity(t *testing.T) {
	f, _, l, _, _ := recoveryFixture(t)
	one, _ := l.Register(context.Background(), f.scope)
	two, _ := l.Register(context.Background(), f.scope)
	if one.PageID == two.PageID || one.Token == two.Token {
		t.Fatal("reused document identity")
	}
}
func TestPageLeaseReturnsConfiguredHeartbeat(t *testing.T) {
	f, m, _, _, now := recoveryFixture(t)
	lease := manager.NewLeaseManager(f.repo, m, func() time.Time { return *now }, 20*time.Second, 2*time.Second)
	p, e := lease.Register(context.Background(), f.scope)
	if e != nil || p.HeartbeatMS != 2000 {
		t.Fatalf("heartbeat config ignored: %+v %v", p, e)
	}
}
func TestPauseCheckpointFailureInterrupted(t *testing.T) {
	f, m, _, a, _ := recoveryFixture(t)
	a.fail = true
	r := recoveryRun(t, f, m, a)
	got, e := m.Pause(context.Background(), f.scope, r.ID)
	if e != nil || got.Status != "interrupted" {
		t.Fatalf("failure must not be resumable: %+v %v", got, e)
	}
}
func TestResumeCASOnlyStartsOnce(t *testing.T) {
	f, m, _, a, _ := recoveryFixture(t)
	ctx := context.Background()
	r := recoveryRun(t, f, m, a)
	if _, e := m.Pause(ctx, f.scope, r.ID); e != nil {
		t.Fatal(e)
	}
	m.Wait()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := m.Resume(ctx, f.scope, r.ID)
			if e != nil && !errors.Is(e, model.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	select {
	case in := <-a.inputs:
		if !in.Resume || in.Run.Attempt != 2 {
			t.Fatalf("not resume: %+v", in)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no resume")
	}
	if a.starts.Load() != 2 {
		t.Fatalf("duplicate resumes: %d", a.starts.Load())
	}
}
func TestResumeTransfersPageOwner(t *testing.T) {
	f, m, l, a, _ := recoveryFixture(t)
	ctx := context.Background()
	r := recoveryRun(t, f, m, a)
	if _, e := m.Pause(ctx, f.scope, r.ID); e != nil {
		t.Fatal(e)
	}
	m.Wait()
	p, _ := l.Register(ctx, f.scope)
	s := f.scope
	s.PageID = p.PageID
	got, e := m.Resume(ctx, s, r.ID)
	if e != nil || got.PageID != p.PageID {
		t.Fatalf("owner transfer: %+v %v", got, e)
	}
}
func TestCrossUserLifecycleDenied(t *testing.T) {
	f, m, _, a, _ := recoveryFixture(t)
	r := recoveryRun(t, f, m, a)
	other := model.Scope{UserID: "user-b"}
	for name, action := range map[string]func(context.Context, model.Scope, string) (model.Run, error){"pause": m.Pause, "resume": m.Resume, "cancel": m.Cancel} {
		if _, e := action(context.Background(), other, r.ID); !errors.Is(e, model.ErrNotFound) {
			t.Fatalf("%s leaked task state: %v", name, e)
		}
	}
}
func TestLogoutPausesOwnedTasks(t *testing.T) {
	f, m, l, a, _ := recoveryFixture(t)
	r := recoveryRun(t, f, m, a)
	if e := l.EndSession(context.Background(), f.scope); e != nil {
		t.Fatal(e)
	}
	got, _ := f.repo.GetRun(context.Background(), f.scope, r.ID)
	if got.Status != "paused" {
		t.Fatalf("logout: %+v", got)
	}
}
func TestRestartWithCheckpointWaitsForUser(t *testing.T) {
	f, m, _, a, _ := recoveryFixture(t)
	ctx := context.Background()
	r := recoveryRun(t, f, m, a)
	if _, e := m.Pause(ctx, f.scope, r.ID); e != nil {
		t.Fatal(e)
	}
	m.Wait()
	m.Close()
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	reg := manager.NewRegistry()
	reg.Register("general", a)
	fresh := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{ExecutionTimeout: time.Minute})
	defer fresh.Close()
	if e := fresh.ReconcileInterrupted(ctx); e != nil {
		t.Fatal(e)
	}
	got, _ := f.repo.GetRun(ctx, f.scope, r.ID)
	if got.Status != "paused" || a.starts.Load() != 1 {
		t.Fatalf("unexpected restart: %+v starts=%d", got, a.starts.Load())
	}
}
func TestRestartWithoutCheckpointInterrupted(t *testing.T) {
	f, m, _, _, _ := recoveryFixture(t)
	ctx := context.Background()
	r := f.admit(t)
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	if e := m.ReconcileInterrupted(ctx); e != nil {
		t.Fatal(e)
	}
	got, _ := f.repo.GetRun(ctx, f.scope, r.ID)
	if got.Status != "interrupted" {
		t.Fatalf("restart: %+v", got)
	}
}
func TestOldAttemptCheckpointWriteRejected(t *testing.T) {
	f, m, _, a, _ := recoveryFixture(t)
	ctx := context.Background()
	r := recoveryRun(t, f, m, a)
	if _, e := m.Pause(ctx, f.scope, r.ID); e != nil {
		t.Fatal(e)
	}
	m.Wait()
	if _, e := m.Resume(ctx, f.scope, r.ID); e != nil {
		t.Fatal(e)
	}
	e := f.repo.SaveCheckpoint(ctx, model.CheckpointWrite{Scope: f.scope, Checkpoint: model.Checkpoint{RunID: r.ID, Attempt: 1, Payload: []byte("late"), Engine: "test", EngineVersion: "1", FormatVersion: 1, Fingerprint: "test"}})
	if !errors.Is(e, model.ErrConflict) {
		t.Fatalf("old attempt accepted: %v", e)
	}
}
