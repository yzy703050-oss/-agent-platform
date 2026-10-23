package manager

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/model"
	"context"
	"log/slog"
	"sync"
)

type EventHub struct {
	mu          sync.Mutex
	capacity    int
	count       int
	subscribers map[string]map[chan contracts.Event]struct{}
}

func NewEventHub(capacity int) *EventHub {
	if capacity < 1 {
		capacity = 64
	}
	return &EventHub{capacity: capacity, subscribers: map[string]map[chan contracts.Event]struct{}{}}
}
func (h *EventHub) Subscribe(id string) (<-chan contracts.Event, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count >= 128 || len(h.subscribers[id]) >= 8 {
		return nil, nil, model.ErrQuota
	}
	if h.subscribers[id] == nil {
		h.subscribers[id] = map[chan contracts.Event]struct{}{}
	}
	ch := make(chan contracts.Event, h.capacity)
	h.subscribers[id][ch] = struct{}{}
	h.count++
	return ch, func() { h.mu.Lock(); defer h.mu.Unlock(); h.remove(id, ch) }, nil
}
func (h *EventHub) remove(id string, ch chan contracts.Event) {
	if _, ok := h.subscribers[id][ch]; !ok {
		return
	}
	delete(h.subscribers[id], ch)
	h.count--
	close(ch)
	if len(h.subscribers[id]) == 0 {
		delete(h.subscribers, id)
	}
}
func (h *EventHub) Publish(e contracts.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers[e.RunID] {
		select {
		case ch <- e:
		default:
			h.remove(e.RunID, ch)
		}
	}
}

type EventCursor struct{ seq int64 }

func NewEventCursor(after int64) *EventCursor { return &EventCursor{seq: after} }
func (c *EventCursor) Accept(e contracts.Event) bool {
	if e.Seq == 0 {
		return true
	}
	if e.Seq <= c.seq {
		return false
	}
	c.seq = e.Seq
	return true
}
func (c *EventCursor) Seq() int64 { return c.seq }
func (m *Manager) Subscribe(ctx context.Context, s model.Scope, id string) (<-chan contracts.Event, func(), error) {
	if _, e := m.store.GetRun(ctx, s, id); e != nil {
		return nil, nil, e
	}
	return m.hub.Subscribe(id)
}
func (m *Manager) Events(ctx context.Context, s model.Scope, id string, after int64) ([]contracts.Event, error) {
	return m.store.ListRunEvents(ctx, s, id, after)
}

// A change notification carries no authority: subscribers replay committed PostgreSQL events.
func (m *Manager) notify(run model.Run) {
	m.hub.Publish(contracts.Event{RunID: run.ID, Attempt: run.Attempt, Type: "sync"})
}
func (m *Manager) transitionRun(ctx context.Context, in model.TransitionRunInput) (model.Run, error) {
	run, e := m.store.TransitionRun(ctx, in)
	if e == nil {
		slog.Info("run_state_changed", "run_id", run.ID, "attempt", run.Attempt, "status", run.Status, "error_code", run.ErrorCode)
		m.notify(run)
		switch run.Status {
		case "failed", "timed_out", "interrupted":
			m.metrics.failed.Add(1)
		case "cancelled":
			m.metrics.cancelled.Add(1)
		}
	}
	return run, e
}
