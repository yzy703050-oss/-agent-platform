// Package manager owns platform run lifecycle; the Agent owns its execution kernel.
package manager

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/ports"
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type Store interface {
	ports.RunStore
	FindIdempotentRun(context.Context, model.AdmitRunInput) (model.Run, error)
	GetRun(context.Context, model.Scope, string) (model.Run, error)
	TransitionRun(context.Context, model.TransitionRunInput) (model.Run, error)
	AppendRunMessage(context.Context, model.Scope, string, int64, model.Message) error
	FindConversation(context.Context, model.Scope, string) (model.Conversation, error)
	BeginResume(context.Context, model.Scope, string) (model.Run, error)
	ListRecoverableRuns(context.Context) ([]model.ScopedRun, error)
	ReconcileRun(context.Context, model.Scope, model.Run, string, string) (model.Run, error)
	ports.CheckpointStore
}
type ContextBuilder interface {
	Build(context.Context, model.Scope, string, string) ([]model.Message, error)
}
type activeRun struct {
	run       model.Run
	scope     model.Scope
	execution contracts.Execution
	cancel    context.CancelFunc
}
type Manager struct {
	mu       sync.Mutex
	store    Store
	memory   ContextBuilder
	registry *Registry
	limits   contracts.Limits
	active   map[string]*activeRun
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
}

func New(s Store, b ContextBuilder, r *Registry, l contracts.Limits) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{store: s, memory: b, registry: r, limits: l, active: map[string]*activeRun{}, ctx: ctx, cancel: cancel}
}
func (m *Manager) Available(agent string) bool { _, ok := m.registry.Get(agent); return ok }
func (m *Manager) Wait()                       { m.wg.Wait() }
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	active := make([]*activeRun, 0, len(m.active))
	for _, x := range m.active {
		active = append(active, x)
	}
	m.mu.Unlock()
	var stops sync.WaitGroup
	for _, x := range active {
		stops.Add(1)
		go func(x *activeRun) { defer stops.Done(); _, _ = m.Pause(context.Background(), x.scope, x.run.ID) }(x)
	}
	stops.Wait()
	m.cancel()
	m.wg.Wait()
}
func (m *Manager) Submit(ctx context.Context, in model.AdmitRunInput, agent string) (model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return model.Run{}, model.ErrConflict
	}
	existing, e := m.store.FindIdempotentRun(ctx, in)
	if e == nil {
		return existing, nil
	}
	if !errors.Is(e, model.ErrNotFound) {
		return model.Run{}, e
	}
	a, ok := m.registry.Get(agent)
	if !ok {
		if agent == "general" {
			return model.Run{}, model.ErrModelUnavailable
		}
		return model.Run{}, model.ErrAgentUnavailable
	}
	count := 0
	for _, x := range m.active {
		if x.run.UserID == in.Scope.UserID {
			count++
		}
	}
	if count >= 3 || len(m.active) >= 8 {
		return model.Run{}, model.ErrQuota
	}
	run, e := m.store.SupersedePausedRun(ctx, in)
	if e != nil {
		return model.Run{}, e
	}
	messages, e := m.memory.Build(ctx, in.Scope, run.ConversationID, run.ID)
	if e != nil {
		m.fail(run, in.Scope, "context_limit")
		return run, e
	}
	execution, e := a.NewExecution(ctx, contracts.RunInput{Scope: in.Scope, Run: run, Messages: messages, Limits: m.limits})
	if e != nil {
		m.fail(run, in.Scope, "agent_initialization_failed")
		return run, model.ErrModelUnavailable
	}
	run, e = m.store.TransitionRun(ctx, model.TransitionRunInput{Scope: in.Scope, RunID: run.ID, Attempt: run.Attempt, From: "accepted", To: "running"})
	if e != nil {
		return model.Run{}, e
	}
	m.launch(run, in.Scope, execution)
	return run, nil
}
func (m *Manager) launch(run model.Run, s model.Scope, exec contracts.Execution) {
	ctx, cancel := context.WithTimeout(m.ctx, m.limits.ExecutionTimeout)
	x := &activeRun{run, s, exec, cancel}
	m.active[run.ID] = x
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer func() { m.mu.Lock(); delete(m.active, run.ID); m.mu.Unlock() }()
		result, err := exec.Start(ctx, func(e contracts.Event) error {
			if e.Type == "message" {
				var msg model.Message
				if err := json.Unmarshal(e.Payload, &msg); err != nil {
					return err
				}
				return m.store.AppendRunMessage(ctx, s, run.ID, run.Attempt, msg)
			}
			return nil
		})
		finishCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err != nil {
			code := "execution_failed"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code = "execution_timeout"
			}
			m.fail(run, s, code)
			return
		}
		current, e := m.store.GetRun(finishCtx, s, run.ID)
		if e != nil || current.Attempt != run.Attempt || current.Status != "running" {
			return
		}
		if e = m.store.CompleteRun(finishCtx, model.FinishRunInput{Scope: s, RunID: run.ID, Attempt: run.Attempt, FinalText: result.FinalText}); e != nil {
			m.fail(run, s, "persistence_failed")
		}
	}()
}
func (m *Manager) fail(run model.Run, s model.Scope, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = m.store.TransitionRun(ctx, model.TransitionRunInput{Scope: s, RunID: run.ID, Attempt: run.Attempt, From: run.Status, To: "failed", ErrorCode: code})
}
