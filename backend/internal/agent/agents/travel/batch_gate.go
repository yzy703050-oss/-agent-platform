package travel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const nativeBatchKey = "platform.travel.native.batch.v1"

type nativeBatchState struct {
	RawResults map[string]json.RawMessage     `json:"raw_results,omitempty"`
	Sequence   int                            `json:"sequence"`
	Batch      traveltools.PreparedBatch      `json:"batch"`
	Calls      []traveltools.NativeCall       `json:"calls"`
	Rejected   *pm.TravelRuleError            `json:"rejected,omitempty"`
	Results    map[string]pm.TravelToolResult `json:"results"`
	Final      string                         `json:"final,omitempty"`
	AfterDone  bool                           `json:"after_done"`
}
type batchRuntime struct {
	sequence int
	done     map[string]chan struct{}
	claimed  map[string]bool
}
type nativeBatchGate struct {
	*adk.BaseChatModelAgentMiddleware
	cfg     KernelConfig
	mu      sync.Mutex
	runtime *batchRuntime
}

func newNativeKernel(ctx context.Context, cfg KernelConfig) (adk.ResumableAgent, error) {
	tools, err := cfg.Registry.Tools()
	if err != nil {
		return nil, err
	}
	h := &nativeBatchGate{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, cfg: cfg}
	kernel, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "travel", Description: "差旅行程规划", Model: cfg.Model, MaxIterations: cfg.MaxIterations + 1, Instruction: instruction,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, UnknownToolsHandler: h.unknownTool}},
		Handlers:    append([]adk.ChatModelAgentMiddleware{h}, cfg.Handlers...),
		GenModelInput: func(_ context.Context, _ string, input *adk.AgentInput) ([]*schema.Message, error) {
			return append([]*schema.Message{schema.SystemMessage(instruction), schema.SystemMessage(replyPolicy)}, input.Messages...), nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &nativeHookAgent{ResumableAgent: kernel, after: h.afterTools}, nil
}

type nativeHookAgent struct {
	adk.ResumableAgent
	after func(context.Context) error
}

func (a *nativeHookAgent) Run(ctx context.Context, in *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.ResumableAgent.Run(ctx, in, append(opts, adk.WithAfterToolCallsHook(a.after))...)
}
func (a *nativeHookAgent) Resume(ctx context.Context, in *adk.ResumeInfo, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.ResumableAgent.Resume(ctx, in, append(opts, adk.WithAfterToolCallsHook(a.after))...)
}

func loadNativeBatch(ctx context.Context) (nativeBatchState, error) {
	value, ok := adk.GetSessionValue(ctx, nativeBatchKey)
	if !ok {
		return nativeBatchState{}, nil
	}
	text, ok := value.(string)
	if !ok {
		return nativeBatchState{}, errors.New("invalid native batch state")
	}
	var state nativeBatchState
	err := json.Unmarshal([]byte(text), &state)
	return state, err
}
func saveNativeBatch(ctx context.Context, state nativeBatchState) error {
	data, err := json.Marshal(state)
	if err == nil {
		adk.AddSessionValue(ctx, nativeBatchKey, string(data))
	}
	return err
}
func (h *nativeBatchGate) profile(ctx context.Context) (traveltools.ProfileRequest, error) {
	if h.cfg.NativeProfile != nil {
		return h.cfg.NativeProfile(ctx)
	}
	return traveltools.ProfileRequest{Role: "main"}, nil
}

func (h *nativeBatchGate) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, err := loadNativeBatch(ctx)
	if err != nil {
		return ctx, state, err
	}
	s, err := readState(ctx)
	if err != nil {
		return ctx, state, err
	}
	if b.Final == "" && s.ModelCalls >= h.cfg.MaxIterations {
		return ctx, state, adk.ErrExceedMaxIterations
	}
	if b.Final == "" && h.cfg.NativeBeforeModel != nil {
		var final string
		state.Messages, final, err = h.cfg.NativeBeforeModel(ctx, state.Messages)
		if err != nil {
			return ctx, state, err
		}
		if final != "" {
			b.Final = final
			if err = saveNativeBatch(ctx, b); err != nil {
				return ctx, state, err
			}
		}
		s, err = readState(ctx)
		if err != nil {
			return ctx, state, err
		}
	}
	profile, err := h.profile(ctx)
	if err != nil {
		return ctx, state, err
	}
	state.ToolInfos, err = h.cfg.Registry.Profile(profile)
	if err != nil {
		return ctx, state, err
	}
	if b.Final != "" {
		state.ToolInfos = nil
	}
	if h.cfg.OnState != nil {
		if err = h.cfg.OnState(ctx, s); err != nil {
			return ctx, state, err
		}
	}
	return ctx, state, nil
}

func (h *nativeBatchGate) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	old, err := loadNativeBatch(ctx)
	if err != nil {
		return ctx, state, err
	}
	if old.Final != "" {
		return ctx, state, nil
	}
	s, err := readState(ctx)
	if err != nil {
		return ctx, state, err
	}
	s.ModelCalls++
	if err = saveState(ctx, s); err != nil {
		return ctx, state, err
	}
	if len(state.Messages) == 0 {
		return ctx, state, errors.New("empty source model response")
	}
	msg := state.Messages[len(state.Messages)-1]
	if h.cfg.NativeModelObserved != nil {
		if err = h.cfg.NativeModelObserved(ctx, msg); err != nil {
			return ctx, state, err
		}
	}
	if len(msg.ToolCalls) == 0 {
		if h.cfg.NativeFinalize != nil {
			msg.Content, err = h.cfg.NativeFinalize(ctx, msg.Content)
			if err != nil {
				return ctx, state, err
			}
		}
		return ctx, state, nil
	}
	profile, err := h.profile(ctx)
	if err != nil {
		return ctx, state, err
	}
	calls := make([]traveltools.NativeCall, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		calls = append(calls, traveltools.NativeCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	var preflightErr error
	if h.cfg.NativeReserveBatch != nil {
		preflightErr = h.cfg.NativeReserveBatch(ctx, calls)
	}
	batch := traveltools.PreparedBatch{Request: profile}
	if preflightErr == nil {
		batch, preflightErr = h.cfg.Registry.Preflight(ctx, profile, calls)
	}
	b := nativeBatchState{Sequence: s.ModelCalls, Batch: batch, Calls: calls, Results: map[string]pm.TravelToolResult{}}
	if preflightErr != nil {
		b.Rejected = correctableBatchError(preflightErr)
	}
	if err = saveNativeBatch(ctx, b); err != nil {
		return ctx, state, err
	}
	h.runtime = nil
	return ctx, state, nil
}

func correctableBatchError(err error) *pm.TravelRuleError {
	var rule *pm.TravelRuleError
	if errors.As(err, &rule) {
		return rule
	}
	if errors.Is(err, pm.ErrConflict) {
		return &pm.TravelRuleError{Code: "INVALID_ARGUMENTS", Message: "行程或任务修订已变化，读取当前结果后再修改。"}
	}
	if errors.Is(err, pm.ErrNotFound) {
		return &pm.TravelRuleError{Code: "INVALID_ARGUMENTS", Message: "trip_not_found"}
	}
	return &pm.TravelRuleError{Code: "TOOL_ERROR", Message: "工具预检失败，未执行本批操作。"}
}
func (h *nativeBatchGate) runtimeFor(b nativeBatchState) *batchRuntime {
	if h.runtime == nil || h.runtime.sequence != b.Sequence {
		h.runtime = &batchRuntime{sequence: b.Sequence, done: map[string]chan struct{}{}, claimed: map[string]bool{}}
		for _, call := range b.Calls {
			c := make(chan struct{})
			if _, ok := b.Results[call.ID]; ok {
				close(c)
			}
			h.runtime.done[call.ID] = c
		}
	}
	return h.runtime
}
func sourcePrecedes(a, b traveltools.PreparedCall, ai, bi int) bool {
	if a.Category == "simple_write" {
		return b.Category != "simple_write" || ai < bi
	}
	if b.Category == "complex_write" {
		return a.Category == "read" || a.Category == "policy" || a.Category == "complex_write" && ai < bi
	}
	if b.Category == "query" {
		return a.Category == "complex_write"
	}
	return false
}

func (h *nativeBatchGate) WrapInvokableToolCall(_ context.Context, next adk.InvokableToolCallEndpoint, t *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		h.mu.Lock()
		b, err := loadNativeBatch(ctx)
		if err != nil {
			h.mu.Unlock()
			return "", err
		}
		if b.Rejected != nil {
			prepared := traveltools.PreparedCall{NativeCall: traveltools.NativeCall{ID: t.CallID, Name: t.Name, Arguments: json.RawMessage(args)}, Rejected: b.Rejected}
			h.mu.Unlock()
			out, err := next(traveltools.WithPreparedCall(ctx, prepared), args, opts...)
			if err != nil {
				return out, err
			}
			var result pm.TravelToolResult
			if err = json.Unmarshal([]byte(out), &result); err != nil {
				return "", err
			}
			return h.complete(ctx, t.CallID, result, out)
		}
		index := -1
		var prepared traveltools.PreparedCall
		for i, call := range b.Batch.Calls {
			if call.ID == t.CallID && call.Name == t.Name && string(call.Arguments) == args {
				index = i
				prepared = call
				break
			}
		}
		if index < 0 {
			h.mu.Unlock()
			return "", pm.ErrIdempotencyConflict
		}
		runtime := h.runtimeFor(b)
		wait := []<-chan struct{}{}
		for i, other := range b.Batch.Calls {
			if i != index && sourcePrecedes(other, prepared, i, index) {
				wait = append(wait, runtime.done[other.ID])
			}
		}
		h.mu.Unlock()
		for _, done := range wait {
			select {
			case <-done:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		h.mu.Lock()
		latest, err := loadNativeBatch(ctx)
		h.mu.Unlock()
		if err != nil {
			return "", err
		}
		if prepared.Category == "complex_write" {
			for _, other := range b.Batch.Calls {
				if other.Category == "simple_write" {
					prior := latest.Results[other.ID]
					if prior.Status != "ok" {
						prepared.Rejected = &pm.TravelRuleError{Code: "PREFERENCE_WRITE_FAILED", Message: "preference_write_failed"}
					}
				}
			}
		}
		out, err := next(traveltools.WithPreparedCall(ctx, prepared), args, opts...)
		if err != nil {
			return out, err
		}
		var result pm.TravelToolResult
		if err = json.Unmarshal([]byte(out), &result); err != nil {
			return "", err
		}
		result.CallID = t.CallID
		result.Name = t.Name
		return h.complete(ctx, t.CallID, result, out)
	}, nil
}

func (h *nativeBatchGate) complete(ctx context.Context, id string, result pm.TravelToolResult, raw string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, err := loadNativeBatch(ctx)
	if err != nil {
		return "", err
	}
	if b.Results == nil {
		b.Results = map[string]pm.TravelToolResult{}
	}
	if raw == "" {
		data, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		raw = string(data)
	}
	result.Raw = json.RawMessage(raw)
	if b.RawResults == nil {
		b.RawResults = map[string]json.RawMessage{}
	}
	b.RawResults[id] = json.RawMessage(raw)
	b.Results[id] = result
	if err = saveNativeBatch(ctx, b); err != nil {
		return "", err
	}
	runtime := h.runtimeFor(b)
	if done, ok := runtime.done[id]; ok {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	return raw, nil
}
func (h *nativeBatchGate) unknownTool(ctx context.Context, name, args string) (string, error) {
	h.mu.Lock()
	b, err := loadNativeBatch(ctx)
	if err != nil {
		h.mu.Unlock()
		return "", err
	}
	runtime := h.runtimeFor(b)
	for _, call := range b.Calls {
		if call.Name == name && string(call.Arguments) == args && !runtime.claimed[call.ID] {
			runtime.claimed[call.ID] = true
			h.mu.Unlock()
			result := pm.TravelToolResult{CallID: call.ID, Name: name, Status: "rejected", Error: &pm.TravelRuleError{Code: "UNKNOWN_TOOL", Message: "unknown_tool:" + name}}
			raw := ""
			if h.cfg.NativeRejected != nil {
				raw, err = h.cfg.NativeRejected(ctx, call, result.Error)
				if err != nil {
					return "", err
				}
			}
			return h.complete(ctx, call.ID, result, raw)
		}
	}
	h.mu.Unlock()
	return "", pm.ErrIdempotencyConflict
}
func (h *nativeBatchGate) afterTools(ctx context.Context) error {
	h.mu.Lock()
	b, err := loadNativeBatch(ctx)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if b.AfterDone {
		return nil
	}
	results := make([]pm.TravelToolResult, 0, len(b.Calls))
	for _, call := range b.Calls {
		result, ok := b.Results[call.ID]
		if !ok {
			return fmt.Errorf("native tool result missing: %s", call.ID)
		}
		result.Raw = b.RawResults[call.ID]
		results = append(results, result)
	}
	final := ""
	if h.cfg.NativeAfterBatch != nil {
		final, err = h.cfg.NativeAfterBatch(ctx, b.Batch, results)
		if err != nil {
			return err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b, err = loadNativeBatch(ctx)
	if err != nil {
		return err
	}
	b.Final = final
	b.AfterDone = true
	return saveNativeBatch(ctx, b)
}

func (h *nativeBatchGate) WrapModel(_ context.Context, inner model.BaseChatModel, _ *adk.ModelContext) (model.BaseChatModel, error) {
	return &nativeFinalModel{inner: inner, finalize: h.cfg.NativeFinalize}, nil
}

type nativeFinalModel struct {
	inner    model.BaseChatModel
	finalize func(context.Context, string) (string, error)
}

func (m *nativeFinalModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	b, err := loadNativeBatch(ctx)
	if err != nil {
		return nil, err
	}
	if b.Final != "" {
		final := b.Final
		if m.finalize != nil {
			final, err = m.finalize(ctx, final)
			if err != nil {
				return nil, err
			}
		}
		return schema.AssistantMessage(final, nil), nil
	}
	response, err := m.inner.Generate(withModelStage(ctx, modelStage(ctx)), messages, opts...)
	if err == nil && response != nil && len(response.ToolCalls) == 0 && m.finalize != nil {
		copy := *response
		copy.Content, err = m.finalize(ctx, response.Content)
		response = &copy
	}
	return response, err
}
func (m *nativeFinalModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{v}), nil
}
