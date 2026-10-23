package general

import (
	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"testing"
	"time"
)

type streamPauseModel struct{ started, release chan struct{} }

func (m *streamPauseModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *streamPauseModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, v := range in {
		if v.Role == schema.Tool {
			return schema.AssistantMessage("resumed-stream-complete", nil), nil
		}
	}
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "settings-once", Type: "function", Function: schema.FunctionCall{Name: "get_my_settings", Arguments: "{}"}}}}, nil
}
func (m *streamPauseModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.started == nil {
		msg, e := m.Generate(ctx, in, o...)
		return schema.StreamReaderFromArray([]*schema.Message{msg}), e
	}
	for _, v := range in {
		if v.Role == schema.Tool {
			reader, writer := schema.Pipe[*schema.Message](1)
			go func() {
				defer writer.Close()
				writer.Send(schema.AssistantMessage("partial", nil), nil)
				close(m.started)
				select {
				case <-ctx.Done():
				case <-m.release:
					writer.Send(&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "settings-second", Type: "function", Function: schema.FunctionCall{Name: "get_my_settings", Arguments: "{}"}}}}, nil)
				}
			}()
			return reader, nil
		}
	}
	msg, e := m.Generate(ctx, in, o...)
	return schema.StreamReaderFromArray([]*schema.Message{msg}), e
}
func TestStreamingEscalationNotResumable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dir := t.TempDir()
	reads := &readSettings{}
	m := &streamPauseModel{started: make(chan struct{})}
	factory := func(contracts.RunInput) adkStore { return &diskStore{dir: dir} }
	a := NewAgent(m, reads, factory, "stream-pause", 30*time.Millisecond)
	input := contracts.RunInput{Run: pm.Run{ID: "stream-pause", Attempt: 1}, Scope: pm.Scope{UserID: "owner"}, Limits: contracts.Limits{Iterations: 8, ToolTimeout: time.Second}, Messages: []pm.Message{{Role: "user", Content: "read settings then reply"}}}
	exec, e := a.NewExecution(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := exec.Start(ctx, nil); done <- e }()
	await(t, m.started)
	paused, e := exec.Pause(ctx)
	if e == nil || !paused.Stopped || paused.CheckpointSaved {
		t.Fatalf("forced stop must not expose an unsafe checkpoint: %+v %v", paused, e)
	}
	if e = <-done; e == nil {
		t.Fatal("expected unsafe checkpoint rejection")
	}
	if _, ok, e := factory(input).Get(ctx, input.Run.ID); e != nil || ok {
		t.Fatalf("unsafe checkpoint persisted: %v %v", ok, e)
	}
}
func TestStreamingSafeCheckpointResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dir := t.TempDir()
	reads := &readSettings{}
	m := &streamPauseModel{started: make(chan struct{}), release: make(chan struct{})}
	factory := func(contracts.RunInput) adkStore { return &diskStore{dir: dir} }
	a := NewAgent(m, reads, factory, "stream-pause", time.Second)
	input := contracts.RunInput{Run: pm.Run{ID: "stream-pause", Attempt: 1}, Scope: pm.Scope{UserID: "owner"}, Limits: contracts.Limits{Iterations: 8, ToolTimeout: time.Second}, Messages: []pm.Message{{Role: "user", Content: "read settings then reply"}}}
	exec, e := a.NewExecution(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := exec.Start(ctx, nil); done <- e }()
	await(t, m.started)
	if !exec.(*execution).runner.RequestPause() {
		t.Fatal("pause not acknowledged")
	}
	close(m.release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if !exec.(*execution).runner.Result().Resumable {
		t.Fatal("safe stop not resumable")
	}
	if reads.calls.Load() != 1 {
		t.Fatal("tool ran after acknowledged safe pause")
	}
	fresh := NewAgent(&streamPauseModel{}, reads, factory, "stream-pause", 30*time.Millisecond)
	input.Resume = true
	input.Messages = nil
	input.Run.Attempt = 2
	resumed, e := fresh.NewExecution(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]int{}
	result, e := resumed.Start(ctx, func(event contracts.Event) error {
		if event.Type == "message" {
			var message pm.Message
			if e := json.Unmarshal(event.Payload, &message); e != nil {
				return e
			}
			if message.Role == "tool" {
				seen[message.ToolCallID]++
			}
		}
		return nil
	})
	if e != nil || result.FinalText == "" || reads.calls.Load() != 2 || seen["settings-once"] != 0 || seen["settings-second"] != 1 {
		t.Fatalf("resumed=%+v err=%v tools=%d", result, e, reads.calls.Load())
	}
}
