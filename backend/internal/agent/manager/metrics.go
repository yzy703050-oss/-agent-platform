package manager

import "sync/atomic"

type Metrics struct{ started, succeeded, failed, paused, cancelled, durationMS atomic.Int64 }

func (m *Manager) Metrics() map[string]int64 {
	m.mu.Lock()
	active := int64(len(m.active))
	m.mu.Unlock()
	return map[string]int64{"active_runs": active, "executions_started_total": m.metrics.started.Load(), "runs_succeeded_total": m.metrics.succeeded.Load(), "runs_failed_total": m.metrics.failed.Load(), "pauses_total": m.metrics.paused.Load(), "cancellations_total": m.metrics.cancelled.Load(), "execution_duration_ms_total": m.metrics.durationMS.Load()}
}
