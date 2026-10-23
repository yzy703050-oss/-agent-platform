package general

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agenttools"
	pm "agent-platform/backend/internal/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"sync"
	"time"
)

type adkStore = adk.CheckPointStore
type Agent struct {
	model        model.ToolCallingChatModel
	settings     agenttools.SettingsReader
	store        func(contracts.RunInput) adkStore
	fingerprint  string
	pauseTimeout time.Duration
}

const instruction = "你是平台的通用聊天助手，用中文清晰回答用户。需要了解当前用户的设置时调用 get_my_settings。该工具只读且没有参数。差旅助手尚未接入，不要假装执行差旅操作。"

func NewAgent(m model.ToolCallingChatModel, s agenttools.SettingsReader, store func(contracts.RunInput) adkStore, identity string, pause time.Duration) *Agent {
	sum := sha256.Sum256([]byte(identity + instruction + "eino-v0.9.0-beta.1:settings-v1"))
	return &Agent{m, s, store, hex.EncodeToString(sum[:]), pause}
}
func (a *Agent) Fingerprint() string { return a.fingerprint }
func (a *Agent) NewExecution(ctx context.Context, in contracts.RunInput) (contracts.Execution, error) {
	kernel, e := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "general", Description: "通用聊天", Instruction: instruction, Model: a.model, MaxIterations: in.Limits.Iterations, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{agenttools.NewSettingsTool(a.settings, in.Scope, in.Limits.ToolTimeout)}}}})
	if e != nil {
		return nil, e
	}
	exec := &execution{input: in, done: make(chan struct{})}
	runner, e := NewRunner(ctx, RunnerConfig{Agent: kernel, Store: a.store(in), RunID: in.Run.ID, Fingerprint: a.fingerprint, Resume: in.Resume, PauseTimeout: a.pauseTimeout, Streaming: true, OnDelta: func(text string) error {
		b, _ := json.Marshal(map[string]string{"text": text})
		if exec.emit == nil {
			return nil
		}
		return exec.emit(contracts.Event{Type: "message.delta", Payload: b})
	}})
	if e != nil {
		return nil, e
	}
	exec.runner = runner
	return exec, nil
}

type execution struct {
	runner          *Runner
	input           contracts.RunInput
	done            chan struct{}
	mu              sync.Mutex
	err             error
	cancel          context.CancelFunc
	cancelRequested bool
	emit            func(contracts.Event) error
}

func (e *execution) Start(ctx context.Context, emit func(contracts.Event) error) (contracts.ExecutionResult, error) {
	e.emit = emit
	ctx = agenttools.WithObserver(ctx, emit)
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancel = cancel
	if e.cancelRequested {
		cancel()
	}
	e.mu.Unlock()
	defer cancel()
	defer close(e.done)
	if ctx.Err() != nil {
		return contracts.ExecutionResult{}, ctx.Err()
	}
	messages := make([]*schema.Message, 0, len(e.input.Messages))
	for _, m := range e.input.Messages {
		v := &schema.Message{Role: schema.RoleType(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			v.ToolCalls = append(v.ToolCalls, schema.ToolCall{ID: c.ID, Type: "function", Function: schema.FunctionCall{Name: c.Name, Arguments: c.Arguments}})
		}
		messages = append(messages, v)
	}
	err := e.runner.Run(ctx, messages, func(v *schema.Message) error {
		if emit == nil || (v.Role != schema.Tool && len(v.ToolCalls) == 0) {
			return nil
		}
		m := pm.Message{Role: string(v.Role), Content: v.Content, ToolCallID: v.ToolCallID}
		for _, c := range v.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, pm.ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments})
		}
		b, _ := json.Marshal(m)
		return emit(contracts.Event{Type: "message", Payload: b})
	})
	e.mu.Lock()
	e.err = err
	e.mu.Unlock()
	code := ""
	if errors.Is(err, adk.ErrExceedMaxIterations) {
		code = "iteration_limit"
	}
	if errors.Is(err, agenttools.ErrToolTimeout) {
		code = "tool_timeout"
	}
	return contracts.ExecutionResult{FinalText: e.runner.Result().FinalText, ErrorCode: code}, err
}
func (e *execution) Pause(ctx context.Context) (contracts.PauseResult, error) {
	if !e.runner.RequestPause() {
		return contracts.PauseResult{}, pm.ErrConflict
	}
	select {
	case <-ctx.Done():
		return contracts.PauseResult{}, ctx.Err()
	case <-e.done:
	}
	e.mu.Lock()
	err := e.err
	e.mu.Unlock()
	return contracts.PauseResult{Stopped: true, CheckpointSaved: e.runner.Result().Resumable}, err
}
func (e *execution) Cancel(ctx context.Context) error {
	e.mu.Lock()
	e.cancelRequested = true
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return nil
	}
}
