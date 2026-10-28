package travel

import (
	"context"
	"strings"
	"testing"
	"time"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type summaryRecoveryModel struct {
	t      *testing.T
	mode   string
	inputs [][]*schema.Message
	stages []string
	bound  []*schema.ToolInfo
	tools  [][]*schema.ToolInfo
}

func (m *summaryRecoveryModel) WithTools(tools []*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	m.bound = tools
	return m, nil
}
func (m *summaryRecoveryModel) Generate(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, input)
	m.stages = append(m.stages, modelStage(ctx))
	tools := em.GetCommonOptions(&em.Options{}, opts...).Tools
	if tools == nil {
		tools = m.bound
	}
	m.tools = append(m.tools, tools)
	if len(m.inputs) == 1 {
		if m.mode == "question" {
			return schema.AssistantMessage("请告诉我出发地。", nil), nil
		}
		return scenarioCall("bad", "write_trip", map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "propose", "tasks": []any{map[string]any{"origin": "上海", "destination": "杭州", "purpose": "INVALID_PURPOSE"}}}}), nil
	}
	if len(m.inputs) == 2 {
		return scenarioCall("recover", "write_trip", map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "propose", "default_round_trip": false, "tasks": []any{map[string]any{"origin": "上海", "destination": "杭州", "conditions": map[string]any{"departure_date": "2026-10-26"}, "items": []any{map[string]any{"domain": "train"}}}}}}), nil
	}
	base := sourceFlowModel{t: m.t, calls: 1}
	return base.Generate(ctx, input, opts...)
}
func (m *summaryRecoveryModel) Stream(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{out}), err
}

func TestMainRecoveryHandsToSummary(t *testing.T) {
	for _, mode := range []string{"question", "parameter"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
			trips := &sourceFlowTrips{now: now}
			flow := newSourceFlow(flowConfig{Trips: trips, OriginalQuery: "安排上海到杭州的单程行程", Now: func() time.Time { return now }})
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips}, Context: flow.decision, OnNativeTrip: flow.recordTrip})
			if err != nil {
				t.Fatal(err)
			}
			flow.registry = registry
			store := &sourceJournalStore{decisions: map[string]pm.TravelDecision{}, results: map[string]string{}}
			j := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, native: true, flow: flow}
			m := &summaryRecoveryModel{t: t, mode: mode}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 8, NativeBeforeModel: flow.beforeModel, NativeModelObserved: flow.modelObserved, NativeReserveBatch: flow.reserve, NativeProfile: flow.profile, NativeAfterBatch: flow.afterBatch, NativeFinalize: flow.finalize, Handlers: []adk.ChatModelAgentMiddleware{j}})
			if err != nil {
				t.Fatal(err)
			}
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "安排上海到杭州的单程行程")
			for {
				event, ok := it.Next()
				if !ok {
					break
				}
				if event.Err != nil {
					t.Fatal(event.Err)
				}
			}
			if len(m.stages) != 3 || m.stages[0] != "agent:main" || m.stages[1] != "agent:trip_summary" || m.stages[2] != "agent:trip_summary" {
				t.Fatalf("Main reentered instead of summary recovery: %v", m.stages)
			}
			if m.inputs[1][0].Content != roleInstruction("planning_summary") {
				t.Fatal("recovery was only relabeled; still using Main prompt")
			}
			assertRuntimeLayers(t, m.inputs[1], "planning_summary")
			found := false
			for _, msg := range m.inputs[1] {
				if strings.Contains(msg.Content, runtimePlanningSummary) {
					found = true
				}
			}
			if !found {
				t.Fatal("missing actual summary role instructions")
			}
			for _, tool := range m.tools[1] {
				if tool.Name == "write_memory" || tool.Name == "request_replan" || tool.Name == "read" {
					t.Fatalf("old Main tool leaked into summary: %s", tool.Name)
				}
			}
			for _, decision := range store.decisions {
				matched := false
				for i, input := range m.inputs {
					hash, _ := decisionRequestHash(input, m.tools[i])
					if hash == decision.RequestHash {
						matched = true
					}
				}
				if !matched {
					t.Fatal("accepted decision hash does not match any actual model input/tool set")
				}
			}
		})
	}
}
