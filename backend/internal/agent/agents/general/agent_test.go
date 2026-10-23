package general

import (
	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
	"context"
	"encoding/json"
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

type chunkModel struct{ loopModel }

func (m *chunkModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }
func (m *chunkModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("第一块", nil), schema.AssistantMessage("第二块", nil)}), nil
}
func TestExecutionEmitsActualStreamChunks(t *testing.T) {
	a := NewAgent(&chunkModel{}, &readSettings{}, func(contracts.RunInput) adkStore { return &diskStore{dir: t.TempDir()} }, "stream", time.Second)
	exec, e := a.NewExecution(context.Background(), contracts.RunInput{Run: pm.Run{ID: "stream"}, Limits: contracts.Limits{Iterations: 2, ToolTimeout: time.Second}, Messages: []pm.Message{{Role: "user", Content: "hi"}}})
	if e != nil {
		t.Fatal(e)
	}
	chunks := []string{}
	result, e := exec.Start(context.Background(), func(event contracts.Event) error {
		if event.Type == "message.delta" {
			var delta struct {
				Text string `json:"text"`
			}
			if e := json.Unmarshal(event.Payload, &delta); e != nil {
				return e
			}
			chunks = append(chunks, delta.Text)
		}
		return nil
	})
	if e != nil || len(chunks) != 2 || chunks[0] != "第一块" || result.FinalText != "第一块第二块" {
		t.Fatalf("chunks=%v result=%+v err=%v", chunks, result, e)
	}
}
func TestCancelBeforeStartNeverCallsModel(t *testing.T) {
	m := &loopModel{toolName: "get_my_settings"}
	a := NewAgent(m, &readSettings{}, func(contracts.RunInput) adkStore { return &diskStore{dir: t.TempDir()} }, "test", time.Second)
	exec, e := a.NewExecution(context.Background(), contracts.RunInput{Run: pm.Run{ID: "run"}, Limits: contracts.Limits{Iterations: 2, ToolTimeout: time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- exec.Cancel(ctx) }()
	// Cancellation starts before execution; a started cancellation must be remembered.
	concrete := exec.(*execution)
	deadline := time.Now().Add(time.Second)
	for {
		concrete.mu.Lock()
		requested := concrete.cancelRequested
		concrete.mu.Unlock()
		if requested {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel not registered")
		}
		time.Sleep(time.Millisecond)
	}
	_, _ = exec.Start(ctx, nil)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if m.calls.Load() != 0 {
		t.Fatal("cancelled execution called model")
	}
}
func testAgentLimit(t *testing.T, name string, wantTools bool) {
	m := &loopModel{toolName: name}
	settings := &readSettings{}
	a := NewAgent(m, settings, func(contracts.RunInput) adkStore { return &diskStore{dir: t.TempDir()} }, "test-model", time.Second)
	exec, e := a.NewExecution(context.Background(), contracts.RunInput{Scope: pm.Scope{UserID: "owner"}, Run: pm.Run{ID: "run", Attempt: 1}, Messages: []pm.Message{{Role: "user", Content: "test"}}, Limits: contracts.Limits{Iterations: 2, ExecutionTimeout: 5 * time.Second, ToolTimeout: time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	result, e := exec.Start(context.Background(), nil)
	if e == nil {
		t.Fatal("expected bounded rejection")
	}
	if wantTools && result.ErrorCode != "iteration_limit" {
		t.Fatalf("limit reason missing: %q %v", result.ErrorCode, e)
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
