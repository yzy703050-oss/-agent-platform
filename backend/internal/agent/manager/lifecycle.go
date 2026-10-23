package manager

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/model"
	"context"
	"time"
)

func (m *Manager) Pause(ctx context.Context, s model.Scope, id string) (model.Run, error) {
	m.mu.Lock()
	run, e := m.store.GetRun(ctx, s, id)
	if e != nil {
		m.mu.Unlock()
		return run, e
	}
	if run.Status == "paused" || run.Status == "pausing" {
		m.mu.Unlock()
		return run, nil
	}
	x := m.active[id]
	if x == nil || run.Status != "running" {
		m.mu.Unlock()
		return model.Run{}, model.ErrConflict
	}
	run, e = m.transitionRun(ctx, model.TransitionRunInput{Scope: s, RunID: id, Attempt: run.Attempt, From: run.Status, To: "pausing"})
	m.mu.Unlock()
	if e != nil {
		return run, e
	}
	bounded, cancel := context.WithTimeout(context.Background(), m.stopBudget())
	defer cancel()
	result, pauseErr := x.execution.Pause(bounded)
	if pauseErr == nil {
		select {
		case <-x.done:
		case <-bounded.Done():
			pauseErr = bounded.Err()
		}
	}
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if pauseErr == nil && result.Stopped && result.CheckpointSaved {
		if paused, err := m.store.FinalizePause(finish, model.FinalizePauseInput{Scope: s, RunID: id, Attempt: run.Attempt}); err == nil {
			m.metrics.paused.Add(1)
			m.notify(paused)
			return paused, nil
		}
	}
	x.cancel()
	return m.transitionRun(finish, model.TransitionRunInput{Scope: s, RunID: id, Attempt: run.Attempt, From: "pausing", To: "interrupted", ErrorCode: "pause_checkpoint_unavailable"})
}
func (m *Manager) Resume(ctx context.Context, s model.Scope, id string) (model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.active[id] != nil {
		return model.Run{}, model.ErrConflict
	}
	count := 0
	for _, x := range m.active {
		if x.run.UserID == s.UserID {
			count++
		}
	}
	if count >= 3 || len(m.active) >= 8 {
		return model.Run{}, model.ErrQuota
	}
	old, e := m.store.GetRun(ctx, s, id)
	if e != nil {
		return old, e
	}
	conv, e := m.store.FindConversation(ctx, s, old.ConversationID)
	if e != nil {
		return model.Run{}, e
	}
	a, ok := m.registry.Get(conv.AgentType)
	if !ok {
		return model.Run{}, model.ErrModelUnavailable
	}
	run, e := m.store.BeginResume(ctx, s, id)
	if e != nil {
		return run, e
	}
	execution, e := a.NewExecution(ctx, contracts.RunInput{Scope: s, Run: run, Resume: true, CheckpointID: id, Limits: m.limits})
	if e != nil {
		_, _ = m.transitionRun(ctx, model.TransitionRunInput{Scope: s, RunID: id, Attempt: run.Attempt, From: "resuming", To: "interrupted", ErrorCode: "checkpoint_incompatible"})
		return model.Run{}, model.ErrCheckpointIncompatible
	}
	run, e = m.transitionRun(ctx, model.TransitionRunInput{Scope: s, RunID: id, Attempt: run.Attempt, From: "resuming", To: "running"})
	if e != nil {
		return run, e
	}
	m.launch(run, s, execution)
	return run, nil
}
func (m *Manager) Cancel(ctx context.Context, s model.Scope, id string) (model.Run, error) {
	m.mu.Lock()
	run, e := m.store.GetRun(ctx, s, id)
	if e != nil {
		m.mu.Unlock()
		return run, e
	}
	if terminal(run.Status) {
		m.mu.Unlock()
		return run, nil
	}
	x := m.active[id]
	run, e = m.transitionRun(ctx, model.TransitionRunInput{Scope: s, RunID: id, Attempt: run.Attempt, From: run.Status, To: "cancelling"})
	m.mu.Unlock()
	if e != nil {
		return run, e
	}
	status, code := "cancelled", "user_cancelled"
	if x != nil {
		bounded, cancel := context.WithTimeout(context.Background(), m.stopBudget())
		e = x.execution.Cancel(bounded)
		cancel()
		x.cancel()
		if e != nil {
			status, code = "interrupted", "cancel_stop_unconfirmed"
		}
	}
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	return m.transitionRun(finish, model.TransitionRunInput{Scope: s, RunID: id, Attempt: run.Attempt, From: "cancelling", To: status, ErrorCode: code})
}
func (m *Manager) stopBudget() time.Duration {
	d := m.limits.PauseTimeout
	if d <= 0 {
		d = 15 * time.Second
	}
	return d + time.Second
}
func terminal(status string) bool {
	switch status {
	case "succeeded", "failed", "timed_out", "cancelled", "interrupted":
		return true
	}
	return false
}
func (m *Manager) ReconcileInterrupted(ctx context.Context) error {
	runs, e := m.store.ListRecoverableRuns(ctx)
	if e != nil {
		return e
	}
	for _, x := range runs {
		status, code := "interrupted", "restart_checkpoint_unavailable"
		cp, ok, e := m.store.GetCheckpoint(ctx, x.Scope, x.Run.ID)
		if e != nil {
			return e
		}
		if x.Run.Status == "cancelling" {
			status, code = "cancelled", "user_cancelled"
		} else if ok && cp.Attempt == x.Run.Attempt {
			conv, e := m.store.FindConversation(ctx, x.Scope, x.Run.ConversationID)
			if e != nil {
				return e
			}
			if a, ok := m.registry.Get(conv.AgentType); ok {
				if _, e = a.NewExecution(ctx, contracts.RunInput{Scope: x.Scope, Run: x.Run, Resume: true, CheckpointID: x.Run.ID, Limits: m.limits}); e == nil {
					status, code = "paused", ""
				} else {
					code = "checkpoint_incompatible"
				}
			}
		}
		if _, e = m.store.ReconcileRun(ctx, x.Scope, x.Run, status, code); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) GetRun(ctx context.Context, s model.Scope, id string) (model.Run, error) {
	return m.store.GetRun(ctx, s, id)
}
