package integration

import (
	"agent-platform/backend/internal/agent/agents/general"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/agenttools"
	pm "agent-platform/backend/internal/model"
	"context"
	"errors"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryModel struct {
	boundary, release chan struct{}
	signaled          atomic.Bool
	calls             atomic.Int32
}

func (m *recoveryModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) { return m, nil }
func (m *recoveryModel) Generate(ctx context.Context, in []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	m.calls.Add(1)
	n := 0
	for _, v := range in {
		if v.Role == schema.Tool {
			n++
		}
	}
	if n >= 2 {
		return schema.AssistantMessage("resumed-complete", nil), nil
	}
	if n == 1 && m.boundary != nil && m.signaled.CompareAndSwap(false, true) {
		close(m.boundary)
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	id := "first"
	if n == 1 {
		id = "second"
	}
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: "get_my_settings", Arguments: "{}"}}}}, nil
}
func (m *recoveryModel) Stream(ctx context.Context, in []*schema.Message, o ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, e := m.Generate(ctx, in, o...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), e
}

type countedSettings struct{ calls atomic.Int32 }

func (s *countedSettings) Get(context.Context, pm.Scope) (pm.UserSettings, error) {
	s.calls.Add(1)
	return pm.UserSettings{Theme: "light"}, nil
}
func TestRecoveryEinoAcrossPostgresInstances(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := f.admit(t)
	r, e := f.repo.TransitionRun(ctx, pm.TransitionRunInput{Scope: f.scope, RunID: r.ID, Attempt: 1, From: "accepted", To: "running"})
	if e != nil {
		t.Fatal(e)
	}
	reads := &countedSettings{}
	m := &recoveryModel{boundary: make(chan struct{}), release: make(chan struct{})}
	kernel := func(m *recoveryModel) adk.Agent {
		a, e := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "general", Model: m, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{agenttools.NewSettingsTool(reads, f.scope, time.Second)}}}})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	store := func(run pm.Run) *general.RepositoryStore {
		return &general.RepositoryStore{Store: f.repo, Input: contracts.RunInput{Scope: f.scope, Run: run}, Fingerprint: "test"}
	}
	runner, e := general.NewRunner(ctx, general.RunnerConfig{Agent: kernel(m), Store: store(r), RunID: r.ID, Fingerprint: "test"})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, []*schema.Message{schema.UserMessage("both")}, nil) }()
	select {
	case <-m.boundary:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, e = f.repo.TransitionRun(ctx, pm.TransitionRunInput{Scope: f.scope, RunID: r.ID, Attempt: 1, From: "running", To: "pausing"}); e != nil {
		t.Fatal(e)
	}
	if !runner.RequestPause() {
		t.Fatal("pause not accepted")
	}
	close(m.release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.FinalizePause(ctx, pm.FinalizePauseInput{Scope: f.scope, RunID: r.ID, Attempt: 1}); e != nil {
		t.Fatal(e)
	}
	// Destroy all Eino objects; the new execution receives only PostgreSQL checkpoint bytes.
	runner = nil
	m = nil
	r, e = f.repo.BeginResume(ctx, f.scope, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	r, e = f.repo.TransitionRun(ctx, pm.TransitionRunInput{Scope: f.scope, RunID: r.ID, Attempt: 2, From: "resuming", To: "running"})
	if e != nil {
		t.Fatal(e)
	}
	resumed, e := general.NewRunner(ctx, general.RunnerConfig{Agent: kernel(&recoveryModel{}), Store: store(r), RunID: r.ID, Fingerprint: "test", Resume: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = resumed.Run(ctx, nil, nil); e != nil {
		t.Fatal(e)
	}
	if reads.calls.Load() != 2 || resumed.Result().FinalText != "resumed-complete" {
		t.Fatalf("replayed tool or lost context: reads=%d result=%+v", reads.calls.Load(), resumed.Result())
	}
}
func TestIncompatibleCheckpointDoesNotExecute(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	r := f.admit(t)
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='pausing' WHERE id=$1", r.ID)
	if e := f.repo.SaveCheckpoint(ctx, pm.CheckpointWrite{Scope: f.scope, Checkpoint: pm.Checkpoint{RunID: r.ID, Attempt: 1, Engine: "eino", EngineVersion: "v0.9.0-beta.1", FormatVersion: 1, Fingerprint: "old-model", Payload: []byte("old")}}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.repo.FinalizePause(ctx, pm.FinalizePauseInput{Scope: f.scope, RunID: r.ID, Attempt: 1}); e != nil {
		t.Fatal(e)
	}
	model := &recoveryModel{}
	var a *general.Agent
	a = general.NewAgent(model, &countedSettings{}, func(in contracts.RunInput) adk.CheckPointStore {
		return &general.RepositoryStore{Store: f.repo, Input: in, Fingerprint: a.Fingerprint()}
	}, "new-model", time.Second)
	reg := manager.NewRegistry()
	reg.Register("general", a)
	mgr := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{ExecutionTimeout: time.Minute})
	defer mgr.Close()
	if _, e := mgr.Resume(ctx, f.scope, r.ID); !errors.Is(e, pm.ErrCheckpointIncompatible) {
		t.Fatalf("incompatible: %v", e)
	}
	if model.calls.Load() != 0 {
		t.Fatal("model executed incompatible state")
	}
	got, _ := f.repo.GetRun(ctx, f.scope, r.ID)
	if got.Status != "interrupted" {
		t.Fatalf("got %+v", got)
	}
}
func TestEinoChatPersistsToolsAndCompletedOutput(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var a *general.Agent
	a = general.NewAgent(&recoveryModel{}, &countedSettings{}, func(in contracts.RunInput) adk.CheckPointStore {
		return &general.RepositoryStore{Store: f.repo, Input: in, Fingerprint: a.Fingerprint()}
	}, "test", time.Second)
	reg := manager.NewRegistry()
	reg.Register("general", a)
	mgr := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{Iterations: 8, ExecutionTimeout: time.Second * 10, ToolTimeout: time.Second})
	defer mgr.Close()
	r, e := mgr.Submit(ctx, f.input("eino", "read settings twice"), "general")
	if e != nil {
		t.Fatal(e)
	}
	mgr.Wait()
	got, e := f.repo.GetRun(ctx, f.scope, r.ID)
	if e != nil || got.Status != "succeeded" || got.PartialOutput != "resumed-complete" {
		t.Fatalf("completion: %+v %v", got, e)
	}
	messages, e := f.repo.ListMessages(ctx, f.scope, f.conversation, 0, 100)
	if e != nil || len(messages) != 6 {
		t.Fatalf("missing tool history: %+v %v", messages, e)
	}
	events, e := mgr.Events(ctx, f.scope, r.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	tools := 0
	for _, event := range events {
		if event.Type == "tool.completed" {
			tools++
		}
	}
	if tools != 2 {
		t.Fatalf("tool events=%d", tools)
	}
}
