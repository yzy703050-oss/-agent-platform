package travel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type interactionModel struct {
	inner        em.ToolCallingChatModel
	inputs       [][]*schema.Message
	updateMemory bool
}

func (m *interactionModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *interactionModel) Generate(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, append([]*schema.Message(nil), input...))
	response, err := m.inner.Generate(ctx, input, opts...)
	if err == nil && m.updateMemory && len(m.inputs) == 1 {
		response.ToolCalls = append(response.ToolCalls, scenarioCall("remember", "write_memory", map[string]any{"request_kind": "planning", "updates": []any{map[string]any{"op": "add", "text": "UPDATED_SEAT_PREFERENCE"}}}).ToolCalls...)
	}
	return response, err
}
func (m *interactionModel) Stream(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{out}), err
}

type interactionMemory struct {
	rows    []pm.TravelPreference
	updates int
}

func (m *interactionMemory) Update(_ context.Context, _ pm.TravelExecution, _ string, cmd pm.TravelMemoryCommand) (pm.TravelMemoryResult, error) {
	m.updates++
	for _, update := range cmd.Updates {
		if update.Text != nil {
			m.rows = append(m.rows, pm.TravelPreference{Key: "memory_updated", Value: *update.Text, Origin: "explicit"})
		}
	}
	return pm.TravelMemoryResult{Status: "ok", Applied: []pm.TravelMemoryApplied{}, Rejected: []pm.TravelMemoryUpdate{}}, nil
}

func runInteractionScenario(t *testing.T, mode string, memory *interactionMemory, update, omitReply bool) (*interactionModel, FlowState, *sourceFlowTrips, string) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	trips := &sourceFlowTrips{now: now}
	ref := pm.TravelExecution{Scope: pm.Scope{UserID: "u", SessionID: "s"}, RunID: "run", ConversationID: "conv", Attempt: 1}
	var state FlowState
	flow := newSourceFlow(flowConfig{Trips: trips, Execution: ref, OriginalQuery: "安排上海到杭州的单程行程", Now: func() time.Time { return now }, ReadPreferences: func(context.Context) ([]pm.TravelPreference, error) { return memory.rows, nil }, Snapshot: func(s FlowState) { state = s }})
	registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips, Execution: ref}, Context: flow.decision, Memory: memory, OnNativeTrip: flow.recordTrip})
	if err != nil {
		t.Fatal(err)
	}
	flow.registry = registry
	base := &sourceScenarioModel{mode: mode, fixture: trips, base: sourceFlowModel{t: t}}
	m := &interactionModel{inner: base, updateMemory: update}
	if omitReply {
		m.inner = &omitReplyModel{inner: base}
	}
	kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 8, NativeBeforeModel: flow.beforeModel, NativeModelObserved: flow.modelObserved, NativeFinalize: flow.finalize, NativeReserveBatch: flow.reserve, NativeProfile: flow.profile, NativeAfterBatch: flow.afterBatch, Handlers: []adk.ChatModelAgentMiddleware{&sourceGateIdentity{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}}})
	if err != nil {
		t.Fatal(err)
	}
	it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "安排上海到杭州的单程行程")
	final := ""
	for {
		event, ok := it.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			msg, e := event.Output.MessageOutput.GetMessage()
			if e == nil && msg != nil && msg.Role == schema.Assistant && len(msg.ToolCalls) == 0 {
				final = msg.Content
			}
		}
	}
	return m, state, trips, final
}

// Used by the next task to exercise a model that forgets the optional reply.
type omitReplyModel struct{ inner em.ToolCallingChatModel }

func (m *omitReplyModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *omitReplyModel) Generate(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	out, err := m.inner.Generate(ctx, input, opts...)
	if err == nil {
		for i := range out.ToolCalls {
			var args map[string]any
			if json.Unmarshal([]byte(out.ToolCalls[i].Function.Arguments), &args) == nil {
				delete(args, "reply")
				if plan, ok := args["tripplan"].(map[string]any); ok && plan["operation"] == "select" {
					plan["allow_repair"] = false
				}
				raw, _ := json.Marshal(args)
				out.ToolCalls[i].Function.Arguments = string(raw)
			}
		}
	}
	return out, err
}
func (m *omitReplyModel) Stream(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{out}), err
}

func TestTravelInteractionInstructions(t *testing.T) {
	for _, mode := range []string{"normal", "lookup"} {
		t.Run(mode, func(t *testing.T) {
			m, _, _, _ := runInteractionScenario(t, mode, &interactionMemory{}, false, false)
			for _, input := range m.inputs {
				count := 0
				for _, msg := range input {
					if msg.Role == schema.System && strings.Contains(msg.Content, "差旅交互与执行约束（interaction-v1）") {
						count++
					}
				}
				if count != 1 {
					t.Errorf("role %q received %d interaction instructions, want 1", strings.SplitN(input[0].Content, "\n", 2)[0], count)
				}
			}
		})
	}
}

func TestTravelInjectedMemoryAcrossRoles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rows   []pm.TravelPreference
		update bool
	}{
		{name: "empty"},
		{name: "effective_and_retracted", rows: []pm.TravelPreference{{Key: "seat", Value: "EXPLICIT_SEAT", Origin: "explicit"}, {Key: "memory_hotel", Value: "ACTIVE_HOTEL", Origin: "explicit"}, {Key: "memory_removed", Value: "RETRACTED_SECRET", Retracted: true}, {Key: "$analysis", Value: `{"summary":"VALID_SUMMARY"}`}}},
		{name: "explicit_update", update: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, trips, final := runInteractionScenario(t, "normal", &interactionMemory{rows: tc.rows}, tc.update, false)
			if len(m.inputs) != 2 || trips.writes != 2 || !strings.Contains(final, "G1") {
				t.Fatal("normal planning did not finish in two model calls")
			}
			for i, input := range m.inputs {
				var data struct {
					Memories []any `json:"memories"`
					Complete bool  `json:"memory_context_complete"`
				}
				contextText := ""
				for _, msg := range input {
					if msg.Role == schema.System && strings.HasPrefix(msg.Content, "当前上下文（数据）：") {
						contextText = strings.TrimPrefix(msg.Content, "当前上下文（数据）：")
						if err := json.Unmarshal([]byte(contextText), &data); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !data.Complete || data.Memories == nil {
					t.Errorf("role %d memory context is not explicitly complete (including empty memory): %s", i, contextText)
				}
				if strings.Contains(contextText, "RETRACTED_SECRET") {
					t.Fatal("retracted memory leaked")
				}
				if tc.name == "effective_and_retracted" {
					for _, want := range []string{"EXPLICIT_SEAT", "ACTIVE_HOTEL", "VALID_SUMMARY"} {
						if !strings.Contains(contextText, want) {
							t.Errorf("role %d missing %s", i, want)
						}
					}
				}
				if tc.update && i == 1 && !strings.Contains(contextText, "UPDATED_SEAT_PREFERENCE") {
					t.Fatal("Verifier did not see the memory write from Main's batch")
				}
			}
		})
	}
}
