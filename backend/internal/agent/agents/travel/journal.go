package travel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/agent/ports"
	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type journalStore interface {
	ports.TravelDecisionStore
	ports.TravelActionStore
}
type journal struct {
	flow          *sourceFlow
	contextTokens int
	*adk.BaseChatModelAgentMiddleware
	store  journalStore
	ref    pm.TravelExecution
	native bool
}

func decisionKey(n int) string { return fmt.Sprintf("model-%06d", n) }
func (h *journal) WrapModel(_ context.Context, m model.BaseChatModel, mc *adk.ModelContext) (model.BaseChatModel, error) {
	return &journalModel{inner: m, h: h, tools: mc.Tools}, nil
}

type journalModel struct {
	inner model.BaseChatModel
	h     *journal
	tools []*schema.ToolInfo
}

func (m *journalModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	s, err := readState(ctx)
	if err != nil {
		return nil, err
	}
	// Ignore ephemeral Eino message IDs. Only semantic input and visible tools
	// define this accepted decision's request identity.
	hash, err := decisionRequestHash(input, m.tools)
	if err != nil {
		return nil, err
	}
	key := decisionKey(s.ModelCalls)
	d, ok, err := m.h.store.FindTravelDecision(ctx, m.h.ref, key)
	if err != nil {
		return nil, err
	}
	if ok {
		if d.RequestHash != hash {
			return nil, pm.ErrIdempotencyConflict
		}
		return toEino(d.Response), nil
	}
	var response *schema.Message
	if m.h.native {
		response, input, err = generateSourceTurn(ctx, m.inner, input, m.h.flow, opts...)
	} else {
		response, err = m.inner.Generate(ctx, input, opts...)
	}
	if err != nil {
		return nil, err
	}
	if m.h.native {
		batch, readErr := loadNativeBatch(ctx)
		if readErr != nil {
			return nil, readErr
		}
		if batch.Final != "" {
			return response, nil
		}
		hash, err = decisionRequestHash(input, m.tools)
		if err != nil {
			return nil, err
		}
	}
	if response == nil {
		return nil, pm.ErrInvalidInput
	}
	if m.h.native && m.h.contextTokens > 0 && sourceTokenEstimate(append(append([]*schema.Message(nil), input...), response), m.tools) > m.h.contextTokens {
		return nil, memory.ErrTravelContextBudget
	}
	encoded, encodeErr := json.Marshal(fromEino(response))
	if encodeErr != nil || len(encoded) > 256000 {
		return nil, pm.ErrInvalidInput
	}
	writes := 0
	for _, c := range response.ToolCalls {
		if c.Function.Name == "write_trip" || c.Function.Name == "write_travel_preference" {
			writes++
		}
	}
	if writes > 1 && !m.h.native {
		return nil, pm.ErrInvalidInput
	}
	stage := string(s.Phase)
	if m.h.native {
		stage = modelStage(ctx)
	}
	decision := pm.TravelDecision{RunID: m.h.ref.RunID, DecisionKey: key, Stage: stage, RequestHash: hash, Response: fromEino(response)}
	if m.h.native {
		for _, msg := range input {
			decision.InputMessages = append(decision.InputMessages, fromEino(msg))
		}
		if value, ok := adk.GetSessionValue(ctx, flowKey); ok {
			if text, ok := value.(string); ok {
				decision.FlowSnapshot = json.RawMessage(text)
			}
		}
	}
	d, err = m.h.store.AcceptTravelDecision(ctx, m.h.ref, decision)
	if err != nil {
		return nil, err
	}
	return toEino(d.Response), nil
}

func decisionRequestHash(input []*schema.Message, tools []*schema.ToolInfo) (string, error) {
	normal := make([]pm.Message, 0, len(input))
	for _, msg := range input {
		normal = append(normal, fromEino(msg))
	}
	raw, err := json.Marshal(struct {
		Messages []pm.Message
		Tools    []*schema.ToolInfo
	}{normal, tools})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (h *journal) recordRejected(ctx context.Context, call traveltools.NativeCall, reason *pm.TravelRuleError) (string, error) {
	s, err := readState(ctx)
	if err != nil {
		return "", err
	}
	d, ok, err := h.store.FindTravelDecision(ctx, h.ref, decisionKey(s.ModelCalls-1))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", pm.ErrConflict
	}
	id := d.OperationIDs[call.ID]
	if id == "" {
		return "", pm.ErrConflict
	}
	matched := false
	for _, accepted := range d.Response.ToolCalls {
		if accepted.ID == call.ID && accepted.Name == call.Name && traveltools.SameNativeArguments(accepted.Arguments, string(call.Arguments)) {
			matched = true
		}
	}
	if !matched {
		return "", pm.ErrIdempotencyConflict
	}
	if out, ok, err := h.store.FindTravelToolResult(ctx, h.ref, id); err != nil || ok {
		return out, err
	}
	data, err := json.Marshal(pm.TravelToolResult{CallID: call.ID, Name: call.Name, Status: "rejected", Reason: reason.Message, Error: reason})
	if err != nil {
		return "", err
	}
	if err = h.store.SaveTravelToolResult(ctx, h.ref, id, string(data)); err != nil {
		return "", err
	}
	return string(data), nil
}
func (m *journalModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{v}), nil
}
func (h *journal) WrapInvokableToolCall(_ context.Context, next adk.InvokableToolCallEndpoint, t *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		s, err := readState(ctx)
		if err != nil {
			return "", err
		}
		d, ok, err := h.store.FindTravelDecision(ctx, h.ref, decisionKey(s.ModelCalls-1))
		if err != nil {
			return "", err
		}
		if !ok {
			return "", pm.ErrConflict
		}
		id := d.OperationIDs[t.CallID]
		if id == "" {
			return "", pm.ErrConflict
		}
		matched := false
		for _, call := range d.Response.ToolCalls {
			if call.ID == t.CallID && call.Name == t.Name && traveltools.SameNativeArguments(call.Arguments, args) {
				matched = true
			}
		}
		if !matched {
			return "", pm.ErrIdempotencyConflict
		}
		if out, ok, err := h.store.FindTravelToolResult(ctx, h.ref, id); err != nil || ok {
			return out, err
		}
		out, err := next(traveltools.WithOperationID(ctx, id), args, opts...)
		if err != nil {
			return out, err
		}
		if err = h.store.SaveTravelToolResult(ctx, h.ref, id, out); err != nil {
			return "", err
		}
		return out, nil
	}, nil
}
func fromEino(v *schema.Message) pm.Message {
	m := pm.Message{Role: string(v.Role), Content: v.Content, ToolCallID: v.ToolCallID}
	for _, c := range v.ToolCalls {
		m.ToolCalls = append(m.ToolCalls, pm.ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments})
	}
	return m
}
func toEino(m pm.Message) *schema.Message {
	v := &schema.Message{Role: schema.RoleType(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
	for _, c := range m.ToolCalls {
		v.ToolCalls = append(v.ToolCalls, schema.ToolCall{ID: c.ID, Type: "function", Function: schema.FunctionCall{Name: c.Name, Arguments: c.Arguments}})
	}
	return v
}
