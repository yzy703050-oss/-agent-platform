// Package manager owns platform run lifecycle; the Agent owns its execution kernel.
package manager

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/ports"
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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
	ListRunEvents(context.Context, model.Scope, string, int64) ([]contracts.Event, error)
	AppendRunEvent(context.Context, model.Scope, contracts.Event) error
	SavePartialOutput(context.Context, model.Scope, string, int64, string) error
}
type ContextBuilder interface {
	Build(context.Context, model.Scope, string, string) ([]model.Message, error)
}
type activeRun struct {
	run       model.Run
	scope     model.Scope
	execution contracts.Execution
	cancel    context.CancelFunc
	done      chan struct{}
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
	hub      *EventHub
	metrics  Metrics
}

func New(s Store, b ContextBuilder, r *Registry, l contracts.Limits) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{store: s, memory: b, registry: r, limits: l, active: map[string]*activeRun{}, ctx: ctx, cancel: cancel, hub: NewEventHub(64)}
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
		if x.run.ConversationID == in.ConversationID {
			return model.Run{}, model.ErrRunConflict
		}
		if x.run.UserID == in.Scope.UserID {
			count++
		}
	}
	if count >= 3 || len(m.active) >= 8 {
		return model.Run{}, model.ErrQuota
	}
	run, e := m.store.SupersedePausedRun(ctx, in)
	if e != nil {
		if errors.Is(e, model.ErrConflict) {
			return model.Run{}, model.ErrRunConflict
		}
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
	run, e = m.transitionRun(ctx, model.TransitionRunInput{Scope: in.Scope, RunID: run.ID, Attempt: run.Attempt, From: "accepted", To: "running"})
	if e != nil {
		return model.Run{}, e
	}
	m.launch(run, in.Scope, execution)
	return run, nil
}
func (m *Manager) launch(run model.Run, s model.Scope, exec contracts.Execution) {
	ctx, cancel := context.WithTimeout(m.ctx, m.limits.ExecutionTimeout)
	x := &activeRun{run: run, scope: s, execution: exec, cancel: cancel, done: make(chan struct{})}
	m.active[run.ID] = x
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(x.done)
		defer cancel()
		defer func() { m.mu.Lock(); delete(m.active, run.ID); m.mu.Unlock() }()
		m.metrics.started.Add(1)
		started := time.Now()
		slog.Info("run_started", "run_id", run.ID, "attempt", run.Attempt)
		defer func() {
			m.metrics.durationMS.Add(time.Since(started).Milliseconds())
			slog.Info("execution_stopped", "run_id", run.ID, "attempt", run.Attempt, "duration_ms", time.Since(started).Milliseconds())
		}()
		var partial strings.Builder
		partial.WriteString(run.PartialOutput)
		lastSave := time.Time{}
		savedBytes := partial.Len()
		flush := func(ctx context.Context) error {
			if partial.Len() == savedBytes {
				return nil
			}
			if err := m.store.SavePartialOutput(ctx, s, run.ID, run.Attempt, partial.String()); err != nil {
				return err
			}
			savedBytes = partial.Len()
			lastSave = time.Now()
			return nil
		}
		result, err := exec.Start(ctx, func(e contracts.Event) error {
			if e.Type == "message" {
				var msg model.Message
				if err := json.Unmarshal(e.Payload, &msg); err != nil {
					return err
				}
				return m.store.AppendRunMessage(ctx, s, run.ID, run.Attempt, msg)
			}
			e.RunID = run.ID
			e.Attempt = run.Attempt
			e.Seq = 0
			if e.Type == "message.delta" {
				var delta struct {
					Text string `json:"text"`
				}
				if len(e.Payload) > 16000 || json.Unmarshal(e.Payload, &delta) != nil || partial.Len()+len(delta.Text) > 256000 {
					return model.ErrInvalidInput
				}
				partial.WriteString(delta.Text)
				if time.Since(lastSave) >= 250*time.Millisecond || partial.Len()-savedBytes >= 4096 {
					if err := flush(ctx); err != nil {
						return err
					}
				}
				m.hub.Publish(e)
				return nil
			}
			if e.Type == "tool.started" || e.Type == "tool.completed" || e.Type == "tool.failed" {
				if err := m.store.AppendRunEvent(ctx, s, e); err != nil {
					return err
				}
				m.notify(run)
				if e.Type != "tool.started" {
					var info struct {
						DurationMS int64 `json:"duration_ms"`
					}
					_ = json.Unmarshal(e.Payload, &info)
					slog.Info("tool_finished", "run_id", run.ID, "attempt", run.Attempt, "status", e.Type, "duration_ms", info.DurationMS)
				}
				return nil
			}
			return nil
		})
		finishCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if flushErr := flush(finishCtx); flushErr != nil && err == nil {
			err = flushErr
		}
		if err != nil {
			code := "execution_failed"
			if result.ErrorCode == "iteration_limit" || result.ErrorCode == "tool_timeout" {
				code = result.ErrorCode
			}
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
		} else {
			m.metrics.succeeded.Add(1)
			m.notify(run)
		}
	}()
}
func (m *Manager) fail(run model.Run, s model.Scope, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status := "failed"
	if code == "execution_timeout" {
		status = "timed_out"
	}
	if _, err := m.transitionRun(ctx, model.TransitionRunInput{Scope: s, RunID: run.ID, Attempt: run.Attempt, From: run.Status, To: status, ErrorCode: code}); err != nil && !errors.Is(err, model.ErrConflict) {
		slog.Error("run_state_persist_failed", "run_id", run.ID, "attempt", run.Attempt, "error_code", "storage_unavailable")
	}
}
