package general

import (
	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
	"context"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"sync/atomic"
	"testing"
	"time"
)

type loopModel struct {
	calls    atomic.Int32
	toolName string
}

func (m *loopModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }
func (m *loopModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls.Add(1)
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call", Type: "function", Function: schema.FunctionCall{Name: m.toolName, Arguments: "{}"}}}}, nil
}
func (m *loopModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	v, e := m.Generate(ctx, in, o...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), e
}

type readSettings struct{ calls atomic.Int32 }

func (r *readSettings) Get(context.Context, pm.Scope) (pm.UserSettings, error) {
	r.calls.Add(1)
	return pm.UserSettings{Theme: "light"}, nil
}
func TestUnknownToolDenied(t *testing.T) { testAgentLimit(t, "delete_everything", false) }
func TestIterationLimit(t *testing.T)    { testAgentLimit(t, "get_my_settings", true) }
func testAgentLimit(t *testing.T, name string, wantTools bool) {
	m := &loopModel{toolName: name}
	settings := &readSettings{}
	a := NewAgent(m, settings, func(contracts.RunInput) adkStore { return &diskStore{dir: t.TempDir()} }, "test-model", time.Second)
	exec, e := a.NewExecution(context.Background(), contracts.RunInput{Scope: pm.Scope{UserID: "owner"}, Run: pm.Run{ID: "run", Attempt: 1}, Messages: []pm.Message{{Role: "user", Content: "test"}}, Limits: contracts.Limits{Iterations: 2, ExecutionTimeout: 5 * time.Second, ToolTimeout: time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = exec.Start(context.Background(), nil)
	if e == nil {
		t.Fatal("expected bounded rejection")
	}
	if m.calls.Load() > 2 {
		t.Fatalf("iterations unbounded: %d", m.calls.Load())
	}
	if !wantTools && settings.calls.Load() != 0 {
		t.Fatal("unknown tool accessed business data")
	}
	if wantTools && settings.calls.Load() == 0 {
		t.Fatal("known tool was not exercised")
	}
}
