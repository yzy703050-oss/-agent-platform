package general

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type diskStore struct {
	dir  string
	fail bool
}

func TestResumeMissingCheckpoint(t *testing.T) {
	_, err := NewRunner(context.Background(), RunnerConfig{Agent: makeAgent(t, &probeModel{}, &atomic.Int32{}, &atomic.Int32{}), Store: &diskStore{dir: t.TempDir()}, RunID: "missing", Fingerprint: "v1", Resume: true})
	if !errors.Is(err, ErrCheckpointMissing) {
		t.Fatalf("expected missing checkpoint: %v", err)
	}
}
func TestCheckpointRemovedBeforeExecutionCannotStartFresh(t *testing.T) {
	ctx := context.Background()
	s := &diskStore{dir: t.TempDir()}
	cp := &checkpointStore{inner: s, runID: "run", fingerprint: "v1"}
	if err := cp.Set(ctx, "run", []byte("checkpoint")); err != nil {
		t.Fatal(err)
	}
	a, b := &atomic.Int32{}, &atomic.Int32{}
	r, err := NewRunner(ctx, RunnerConfig{Agent: makeAgent(t, &probeModel{}, a, b), Store: s, RunID: "run", Fingerprint: "v1", Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Delete(ctx, "run")
	err = r.Run(ctx, nil, nil)
	if !errors.Is(err, ErrCheckpointMissing) {
		t.Fatalf("must not silently start fresh: %v", err)
	}
	if a.Load() != 0 || b.Load() != 0 {
		t.Fatal("executed a fresh task")
	}
}

func (s *diskStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	b, e := os.ReadFile(filepath.Join(s.dir, "checkpoint"))
	if errors.Is(e, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, e == nil, e
}
func (s *diskStore) Set(ctx context.Context, id string, b []byte) error {
	if s.fail {
		return errors.New("disk full")
	}
	return os.WriteFile(filepath.Join(s.dir, "checkpoint"), b, 0600)
}
func (s *diskStore) Delete(ctx context.Context, id string) error {
	e := os.Remove(filepath.Join(s.dir, "checkpoint"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}

type probeModel struct {
	second   chan struct{}
	release  chan struct{}
	signaled atomic.Bool
}

func (m *probeModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *probeModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	n := 0
	for _, v := range in {
		if v.Role == schema.Tool {
			n++
		}
	}
	if n >= 2 {
		return schema.AssistantMessage("complete", nil), nil
	}
	if n == 1 && m.second != nil && m.signaled.CompareAndSwap(false, true) {
		close(m.second)
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	name, id := "first", "call-first"
	if n == 1 {
		name, id = "second", "call-second"
	}
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: "{}"}}}}, nil
}
func (m *probeModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, e := m.Generate(ctx, in, o...)
	if e != nil {
		return nil, e
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

type countedTool struct {
	name  string
	count *atomic.Int32
}

func (t *countedTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "read only probe"}, nil
}
func (t *countedTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	t.count.Add(1)
	return "ok", nil
}

func makeAgent(t *testing.T, m *probeModel, a, b *atomic.Int32) adk.Agent {
	t.Helper()
	agent, e := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name: "general", Description: "checkpoint probe", Model: m, MaxIterations: 8,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{&countedTool{"first", a}, &countedTool{"second", b}}}},
	})
	if e != nil {
		t.Fatal(e)
	}
	return agent
}
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("execution did not reach expected boundary")
	}
}

func TestCheckpointResumeAcrossExecutionInstances(t *testing.T) {
	testCheckpointResume(t)
}
func TestResumeWithoutApprovalPayload(t *testing.T) {
	testCheckpointResume(t)
}
func testCheckpointResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	s := &diskStore{dir: dir}
	a, b := &atomic.Int32{}, &atomic.Int32{}
	m := &probeModel{second: make(chan struct{}), release: make(chan struct{})}
	r, e := NewRunner(ctx, RunnerConfig{Agent: makeAgent(t, m, a, b), Store: s, RunID: "run-1", Fingerprint: "model-v1"})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = r.Run(ctx, []*schema.Message{schema.UserMessage("use both tools")}, nil); close(done) }()
	await(t, m.second)
	if !r.RequestPause() {
		t.Fatal("pause not accepted")
	}
	close(m.release)
	await(t, done)
	if runErr != nil {
		t.Fatalf("pause: %v", runErr)
	}
	if !r.Result().Resumable {
		t.Fatalf("not resumable: %+v", r.Result())
	}
	if a.Load() != 1 || b.Load() != 0 {
		t.Fatalf("before resume first=%d second=%d", a.Load(), b.Load())
	}
	// A new store and new model/agent/runner share only durable bytes, never execution memory.
	resumed, e := NewRunner(ctx, RunnerConfig{Agent: makeAgent(t, &probeModel{}, a, b), Store: &diskStore{dir: dir}, RunID: "run-1", Fingerprint: "model-v1", Resume: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = resumed.Run(ctx, nil, nil); e != nil {
		t.Fatal(e)
	}
	if resumed.Result().FinalText != "complete" {
		t.Fatalf("result: %+v", resumed.Result())
	}
	if a.Load() != 1 || b.Load() != 1 {
		t.Fatalf("replayed tool: first=%d second=%d", a.Load(), b.Load())
	}
	if _, ok, e := s.Get(ctx, ""); e != nil || ok {
		t.Fatalf("completed checkpoint not deleted: %v %v", ok, e)
	}
}
func TestCheckpointSaveFailureNotResumable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m := &probeModel{second: make(chan struct{}), release: make(chan struct{})}
	r, e := NewRunner(ctx, RunnerConfig{Agent: makeAgent(t, m, &atomic.Int32{}, &atomic.Int32{}), Store: &diskStore{dir: t.TempDir(), fail: true}, RunID: "failed-save", Fingerprint: "v1"})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = r.Run(ctx, []*schema.Message{schema.UserMessage("probe")}, nil); close(done) }()
	await(t, m.second)
	r.RequestPause()
	close(m.release)
	await(t, done)
	if runErr == nil || r.Result().Resumable {
		t.Fatalf("save failure reported resumable: err=%v result=%+v", runErr, r.Result())
	}
}
func TestCheckpointConfigurationMismatch(t *testing.T) {
	ctx := context.Background()
	s := &diskStore{dir: t.TempDir()}
	if e := s.Set(ctx, "", []byte("{\"version\":1,\"run_id\":\"run\",\"fingerprint\":\"old\",\"payload\":\"Ynl0ZXM=\"}")); e != nil {
		t.Fatal(e)
	}
	_, e := NewRunner(ctx, RunnerConfig{Agent: makeAgent(t, &probeModel{}, &atomic.Int32{}, &atomic.Int32{}), Store: s, RunID: "run", Fingerprint: "new", Resume: true})
	if !errors.Is(e, ErrCheckpointIncompatible) {
		t.Fatalf("expected incompatible, got %v", e)
	}
}
