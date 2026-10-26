package travel

import (
	"context"
	"strings"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type questioningModel struct {
	calls, invalid int
	tools          bool
}

func (m *questioningModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *questioningModel) Generate(context.Context, []*schema.Message, ...em.Option) (*schema.Message, error) {
	m.calls++
	if m.calls <= m.invalid {
		out := schema.AssistantMessage("请告诉我出发地，确认后再为您规划。", nil)
		if m.tools {
			out.ToolCalls = scenarioCall("must-not-run", "write_trip", map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "propose", "default_round_trip": false, "tasks": []any{map[string]any{"origin": "上海", "destination": "杭州", "conditions": map[string]any{"departure_date": "2026-10-26"}}}}}).ToolCalls
		}
		return out, nil
	}
	return schema.AssistantMessage("上海可安排外滩和豫园游览，出发地未明确，跨城交通尚未核实。", nil), nil
}
func (m *questioningModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, in, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{out}), err
}

func TestTravelQuestioningResponseIsNotAccepted(t *testing.T) {
	for _, invalid := range []int{1, 3} {
		t.Run(map[int]string{1: "corrected", 3: "bounded_failure"}[invalid], func(t *testing.T) {
			ctx := context.Background()
			trips := &sourceGateTrips{}
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips}})
			if err != nil {
				t.Fatal(err)
			}
			store := &sourceJournalStore{decisions: map[string]pm.TravelDecision{}, results: map[string]string{}}
			journal := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, native: true}
			m := &questioningModel{invalid: invalid, tools: true}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 8, Handlers: []adk.ChatModelAgentMiddleware{journal}})
			if err != nil {
				t.Fatal(err)
			}
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "帮我规划明天去上海的旅途")
			failed := false
			final := ""
			for {
				event, ok := it.Next()
				if !ok {
					break
				}
				failed = failed || event.Err != nil
				if event.Output != nil && event.Output.MessageOutput != nil {
					msg, e := event.Output.MessageOutput.GetMessage()
					if e == nil && msg != nil {
						final = msg.Content
					}
				}
			}
			want := invalid + 1
			if invalid == 3 {
				want = 3
			}
			if m.calls != want || failed != (invalid == 3) || strings.Contains(final, "请告诉我") || trips.writes.Load() != 0 {
				t.Fatalf("question accepted or retry not bounded: calls=%d failed=%v final=%q", m.calls, failed, final)
			}
			if invalid == 3 && len(store.decisions) != 0 {
				t.Fatal("questioning decision was persisted")
			}
		})
	}
}

func TestTravelInteractionRequestBoundaries(t *testing.T) {
	for text, want := range map[string]bool{
		"我需要先确认一下：您是从哪个城市出发去上海？":      true,
		"请补充出发地、人数和住宿日期。":             true,
		"你把这几项告诉我，我再安排。":              true,
		"出发城市：是从北京出发吗？":               true,
		"出发地尚未明确，跨城交通尚未核实。":           false,
		"不需要您补充任何信息。":                 false,
		"您没有提供出发城市，上海本地的安排先按可调整建议规划。": false,
		"您尚未确认出发城市，跨城交通尚未核实。":         false,
		"跨城交通待您补充出发地后再核实。":            true,
		"我先确认一下你的出发地，以及这趟是旅游还是出差。":    true,
		"补充出发地后即可据此安排。":               true,
		"无需您确认，以下为可调整建议。":             false,
		"酒店报价是否包含早餐尚未核实。":             false,
		"上海可安排外滩与豫园，返程日期为软建议。":        false,
	} {
		if got := asksForUserInput(text); got != want {
			t.Errorf("request detection %q=%v want %v", text, got, want)
		}
	}
	response := scenarioCall("select", "write_trip", map[string]any{"reply": map[string]any{"explanation": "请确认是否接受这个方案。"}})
	if sourceInteractionFailure(response) != "user_input_requested" {
		t.Fatal("question in bound explanation escaped validation")
	}
}

func TestTravelReplanFeedbackNarrowsNextReview(t *testing.T) {
	s := FlowState{LastBatchResults: []pm.TravelToolResult{{Name: "write_trip", Status: "rejected", Error: &pm.TravelRuleError{Code: "REPLAN_REQUIRED"}}}}
	if !profileForState(s, "verifier").ReplanRequired {
		t.Fatal("review still permits the rejected repair instead of handoff")
	}
	if profileForState(s, "main").ReplanRequired {
		t.Fatal("Main inherited review restriction")
	}
	s.LastBatchResults = []pm.TravelToolResult{{Name: "request_replan", Status: "ok"}}
	if profileForState(s, "verifier").ReplanRequired {
		t.Fatal("completed handoff did not clear review restriction")
	}
}

func TestTravelReplanMainAlreadyHasFullPlan(t *testing.T) {
	s := FlowState{Planning: true, Plans: map[string]pm.TripResult{"p": {}}}
	if !profileForState(s, "main").PlanContextComplete {
		t.Fatal("Main was allowed to re-read its injected plan")
	}
	if profileForState(s, "verifier").PlanContextComplete {
		t.Fatal("review unexpectedly uses Main restriction")
	}
	s.Planning = false
	if profileForState(s, "main").PlanContextComplete {
		t.Fatal("explicit historical lookup was restricted")
	}
}

func TestTravelFreshPlanningCannotReadHistory(t *testing.T) {
	for query, want := range map[string]bool{
		"帮我规划明天去上海的旅途":                            true,
		"安排上海到杭州往返行程":                             true,
		"安排去上海历史博物馆的行程":                           true,
		"不要读取历史，帮我规划去上海的行程":                       true,
		"不要修改已有行程，请规划明天去上海":                       true,
		"查看我上次去上海的行程":                             false,
		"参考上次行程，规划下周去上海":                          false,
		"帮我调整上海的行程安排":                             false,
		"Plan a trip to Shanghai tomorrow":        true,
		"Plan a trip using my previous itinerary": false,
		"Show my saved travel plan":               false,
	} {
		s := FlowState{OriginalQuery: query}
		if got := profileForState(s, "main").NewPlanningRequest; got != want {
			t.Errorf("fresh planning %q=%v want %v", query, got, want)
		}
	}
}
