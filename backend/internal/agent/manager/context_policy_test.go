package manager

import (
	"context"
	"testing"

	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/model"
)

type policyContextProbe struct{ regular, complete int }

func (b *policyContextProbe) Build(context.Context, model.Scope, string, string) ([]model.Message, error) {
	b.regular++
	return nil, nil
}
func (b *policyContextProbe) BuildComplete(context.Context, model.Scope, string, string) ([]model.Message, error) {
	b.complete++
	return []model.Message{{Content: "complete authorized turn"}}, nil
}

type contextPolicyAgent struct{ full bool }

func (a contextPolicyAgent) ContextPolicy() contracts.ContextPolicy {
	return contracts.ContextPolicy{PreserveCompleteHistory: a.full}
}
func (contextPolicyAgent) NewExecution(context.Context, contracts.RunInput) (contracts.Execution, error) {
	return nil, nil
}
func TestTravelContextPolicy(t *testing.T) {
	b := &policyContextProbe{}
	m := &Manager{memory: b}
	for _, full := range []bool{false, true} {
		if _, err := m.loadContext(context.Background(), contextPolicyAgent{full}, model.Scope{}, model.Run{}); err != nil {
			t.Fatal(err)
		}
	}
	if b.regular != 1 || b.complete != 1 {
		t.Fatal("source history truncated before Travel compaction or General changed", b)
	}
}
