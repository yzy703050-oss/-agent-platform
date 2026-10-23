package manager

import (
	"agent-platform/backend/internal/agent/contracts"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	agents map[string]contracts.Agent
}

func NewRegistry() *Registry { return &Registry{agents: map[string]contracts.Agent{}} }
func (r *Registry) Register(name string, a contracts.Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[name] = a
}
func (r *Registry) Get(name string) (contracts.Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[name]
	return a, ok
}
