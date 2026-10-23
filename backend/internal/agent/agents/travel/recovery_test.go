package travel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"agent-platform/backend/internal/agent/einoruntime"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// A filesystem fixture proves that fresh objects share durable bytes only.
type probeStore struct {
	dir  string
	fail bool
}

func (s *probeStore) Get(_ context.Context, _ string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "checkpoint"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}
func (s *probeStore) Set(_ context.Context, _ string, b []byte) error {
	if s.fail {
		return errors.New("injected checkpoint failure")
	}
	return os.WriteFile(filepath.Join(s.dir, "checkpoint"), b, 0600)
}
func (s *probeStore) Delete(_ context.Context, _ string) error {
	err := os.Remove(filepath.Join(s.dir, "checkpoint"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type probeModel struct {
	entered, release chan struct{}
	stages           []State
}

func (m *probeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }
func (m *probeModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	state, err := readState(ctx)
	if err != nil {
		return nil, err
	}
	m.stages = append(m.stages, state)
	results := 0
	for _, msg := range in {
		if msg.Role == schema.Tool {
			results++
		}
	}
	if results == 1 && m.entered != nil {
		close(m.entered)
		// Wait until the test has requested a real TurnLoop stop.
		<-m.release
	}
	if results == 0 {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "create", Type: "function", Function: schema.FunctionCall{Name: "write_trip", Arguments: `{"operation":"propose"}`}}}}, nil
	}
	if results == 1 {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "query", Type: "function", Function: schema.FunctionCall{Name: "query_travel", Arguments: `{}`}}}}, nil
	}
	return schema.AssistantMessage("已根据保存结果完成差旅查询。", nil), nil
}
func (m *probeModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

type probeTool struct {
	name     string
	count    *atomic.Int32
	observed *State
}

func (p *probeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: p.name, Desc: "travel boundary probe"}, nil
}
func (p *probeTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if p.observed != nil {
		s, err := readState(ctx)
		if err != nil {
			return "", err
		}
		*p.observed = s
	}
	p.count.Add(1)
	return `{"status":"ok"}`, nil
}

func TestTravelCheckpointResume(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "durable_state"
		if fail {
			name = "save_failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			store := &probeStore{dir: t.TempDir(), fail: fail}
			var writes, queries atomic.Int32
			var queryState State
			tools := []tool.BaseTool{&probeTool{name: "write_trip", count: &writes}, &probeTool{name: "query_travel", count: &queries, observed: &queryState}}
			m := &probeModel{entered: make(chan struct{}), release: make(chan struct{})}
			defer func() {
				select {
				case <-m.release:
				default:
					close(m.release)
				}
			}()
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, Tools: tools, MaxIterations: 8})
			if err != nil {
				t.Fatal(err)
			}
			runner, err := einoruntime.NewRunner(ctx, einoruntime.RunnerConfig{Agent: kernel, Store: store, RunID: "travel-run", Fingerprint: "travel-v1"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- runner.Run(ctx, []*schema.Message{schema.UserMessage("安排一次差旅")}, nil) }()
			select {
			case <-m.entered:
			case <-ctx.Done():
				t.Fatal("did not reach query boundary")
			}
			if !runner.RequestPause() {
				t.Fatal("pause was not acknowledged")
			}
			close(m.release)
			err = <-done
			if fail {
				if err == nil || runner.Result().Resumable {
					t.Fatalf("failed save reported resumable: %v", err)
				}
				return
			}
			if err != nil || !runner.Result().Resumable {
				t.Fatalf("pause: %v %+v", err, runner.Result())
			}
			if writes.Load() != 1 || queries.Load() != 0 {
				t.Fatalf("boundary writes=%d queries=%d", writes.Load(), queries.Load())
			}
			_, err = einoruntime.NewRunner(ctx, einoruntime.RunnerConfig{Agent: kernel, Store: store, RunID: "travel-run", Fingerprint: "different", Resume: true})
			if !errors.Is(err, einoruntime.ErrCheckpointIncompatible) {
				t.Fatalf("fingerprint: %v", err)
			}
			freshModel := &probeModel{}
			freshKernel, err := NewKernel(ctx, KernelConfig{Model: freshModel, Tools: tools, MaxIterations: 8})
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := einoruntime.NewRunner(ctx, einoruntime.RunnerConfig{Agent: freshKernel, Store: &probeStore{dir: store.dir}, RunID: "travel-run", Fingerprint: "travel-v1", Resume: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(freshModel.stages) != 0 {
				t.Fatal("construction invoked model")
			}
			if err = fresh.Run(ctx, nil, nil); err != nil {
				t.Fatal(err)
			}
			if writes.Load() != 1 || queries.Load() != 1 {
				t.Fatalf("replayed tools writes=%d queries=%d", writes.Load(), queries.Load())
			}
			// The accepted query tool call resumes before another model invocation.
			if queryState.Phase != PhaseQuerying || queryState.ModelCalls != 2 {
				t.Fatalf("pending tool lost state: %+v", queryState)
			}
			if len(freshModel.stages) == 0 || freshModel.stages[0].Phase != PhaseValidating || freshModel.stages[0].ModelCalls != 2 {
				b, _ := json.Marshal(freshModel.stages)
				t.Fatalf("stage/counter lost: %s", b)
			}
			if fresh.Result().FinalText == "" {
				t.Fatal("missing final answer")
			}
		})
	}
}
