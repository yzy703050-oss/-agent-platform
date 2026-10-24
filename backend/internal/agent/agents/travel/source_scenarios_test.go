package travel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type sourceScenarioModel struct {
	fixture *sourceFlowTrips
	base    sourceFlowModel
	mode    string
	calls   int
}

func (m *sourceScenarioModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func scenarioCall(id, name string, args any) *schema.Message {
	raw, _ := json.Marshal(args)
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: string(raw)}}}}
}
func (m *sourceScenarioModel) Generate(ctx context.Context, messages []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	m.calls++
	if m.mode == "lookup" || m.mode == "lookup_followup" {
		if m.calls == 1 {
			return scenarioCall("lookup", "query", map[string]any{"request_kind": "lookup", "query_plan": []any{map[string]any{"domain": "train", "parameters": map[string]any{"origin": "上海", "destination": "杭州", "departure_date": "2026-10-26"}}}}), nil
		}
		if messages[0].Content != summaryInstruction {
			return nil, fmt.Errorf("lookup did not hand to source Summary")
		}
		if m.mode == "lookup_followup" && m.calls == 2 {
			return scenarioCall("lookup-other", "query", map[string]any{"request_kind": "lookup", "query_plan": []any{map[string]any{"domain": "train", "parameters": map[string]any{"origin": "南京", "destination": "苏州", "departure_date": "2026-10-26"}}}}), nil
		}
		return schema.AssistantMessage("已查询到车次 G1。", nil), nil
	}
	if m.mode == "policy" && m.calls == 1 {
		return scenarioCall("policy", "query_policy", map[string]any{"request_kind": "planning", "context": map[string]any{}}), nil
	}
	if m.calls == 1 || m.mode == "policy" && m.calls == 2 {
		reply, err := m.base.Generate(ctx, messages, opts...)
		if m.mode == "multiple" && err == nil {
			second := reply.ToolCalls[0]
			second.ID = "create-other"
			second.Function.Arguments = strings.ReplaceAll(strings.ReplaceAll(second.Function.Arguments, "上海", "南京"), "杭州", "苏州")
			reply.ToolCalls = append(reply.ToolCalls, second)
		}
		return reply, err
	}
	var review struct {
		Tripplan  map[string]any   `json:"tripplan"`
		Tripplans []map[string]any `json:"tripplans"`
		Replan    json.RawMessage  `json:"replan_request"`
	}
	if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &review); err != nil {
		return nil, err
	}
	if m.mode == "no_progress" {
		return scenarioCall(fmt.Sprintf("unchanged-%d", m.calls), "write_trip", map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "amend", "trip_plan_id": review.Tripplan["trip_plan_id"], "expected_revision": review.Tripplan["revision"]}}), nil
	}
	if m.mode == "replan" && m.calls == 2 {
		return scenarioCall("replan", "request_replan", map[string]any{"request_kind": "planning", "trip_plan_ids": []any{review.Tripplan["trip_plan_id"]}, "reason": "缺少目的地活动，需主规划补全。"}), nil
	}
	if m.mode == "replan" && m.calls == 3 {
		if messages[0].Content != instruction || len(review.Replan) == 0 {
			return nil, fmt.Errorf("replan did not hand Main original context")
		}
		task := review.Tripplan["tasks"].([]any)[0].(map[string]any)
		return scenarioCall("add-content", "write_trip", map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "amend", "trip_plan_id": review.Tripplan["trip_plan_id"], "expected_revision": review.Tripplan["revision"], "tasks": []any{map[string]any{"task_id": task["id"], "components": []string{"content"}, "content": []any{map[string]any{"purpose": "会议", "description": "抵达后参加会议", "date": "2026-10-26", "time": "16:00", "source": "proposal", "strength": "soft"}}}}}}), nil
	}
	if m.mode == "multiple" {
		if messages[0].Content != reviewInstruction || len(review.Tripplans) != 2 {
			return nil, fmt.Errorf("Verifier did not receive both plans")
		}
		response := &schema.Message{Role: schema.Assistant}
		for i, p := range review.Tripplans {
			task := p["tasks"].([]any)[0].(map[string]any)
			candidate := p["candidates"].([]any)[0].(map[string]any)
			item := candidate["items"].([]any)[0].(map[string]any)
			args := map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "select", "trip_plan_id": p["trip_plan_id"], "expected_revision": p["revision"], "tasks": []any{map[string]any{"task_id": task["id"], "expected_revision": task["revision"], "selection_refs": map[string]any{"train": map[string]any{"query_id": candidate["query_id"], "result_revision": candidate["result_revision"], "candidate_id": item["id"]}}}}}, "reply": map[string]any{}}
			response.ToolCalls = append(response.ToolCalls, scenarioCall(fmt.Sprintf("select-%d", i), "write_trip", args).ToolCalls...)
		}
		return response, nil
	}
	if m.mode == "protocol_no_progress" {
		return &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{FinishReason: "length"}}, nil
	}
	if (m.mode == "expiry" || m.mode == "expiry_again") && m.calls == 3 {
		m.base.calls = 1
	}
	response, err := m.base.Generate(ctx, messages, opts...)
	if m.mode == "expiry" && m.calls == 2 || m.mode == "expiry_again" && (m.calls == 2 || m.calls == 3) {
		m.fixture.mu.Lock()
		m.fixture.now = m.fixture.now.Add(2 * time.Millisecond)
		m.fixture.mu.Unlock()
	}
	return response, err
}
func (m *sourceScenarioModel) Stream(ctx context.Context, messages []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, messages, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), err
}

func TestTravelSourceFlowScenarios(t *testing.T) {
	for _, tc := range []struct {
		mode                    string
		models, writes, queries int
	}{{"lookup", 2, 0, 1}, {"lookup_followup", 3, 0, 2}, {"policy", 3, 2, 1}, {"replan", 4, 3, 1}, {"multiple", 2, 4, 2}, {"no_progress", 3, 3, 1}, {"expiry", 3, 2, 2}, {"expiry_again", 3, 1, 2}, {"expiry_before_review", 2, 2, 2}, {"protocol_no_progress", 3, 1, 1}} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
			trips := &sourceFlowTrips{now: now}
			if tc.mode == "expiry" || tc.mode == "expiry_again" {
				trips.ttl = time.Millisecond
			}
			if tc.mode == "expiry_before_review" {
				trips.ttl = time.Millisecond
				trips.expireFirst = true
			}
			ref := pm.TravelExecution{Scope: pm.Scope{UserID: "u", SessionID: "s"}, RunID: "run", ConversationID: "conv", Attempt: 1}
			var state FlowState
			flow := newSourceFlow(flowConfig{Trips: trips, Execution: ref, OriginalQuery: "安排上海到杭州的单程行程", Now: func() time.Time { trips.mu.Lock(); defer trips.mu.Unlock(); return trips.now }, Snapshot: func(s FlowState) { state = s }})
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips, Execution: ref}, Context: flow.decision, OnNativeTrip: flow.recordTrip})
			if err != nil {
				t.Fatal(err)
			}
			flow.registry = registry
			m := &sourceScenarioModel{mode: tc.mode, fixture: trips, base: sourceFlowModel{t: t}}
			store := &sourceJournalStore{decisions: map[string]pm.TravelDecision{}, results: map[string]string{}}
			journal := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, ref: ref, native: true, flow: flow}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 8, NativeFinalize: flow.finalize, NativeBeforeModel: flow.beforeModel, NativeModelObserved: flow.modelObserved, NativeReserveBatch: flow.reserve, NativeProfile: flow.profile, NativeAfterBatch: flow.afterBatch, NativeRejected: journal.recordRejected, Handlers: []adk.ChatModelAgentMiddleware{journal}})
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
			if m.calls != tc.models || trips.writes != tc.writes || trips.queries != tc.queries {
				t.Fatalf("models=%d writes=%d queries=%d state=%s", m.calls, trips.writes, trips.queries, state.StopReason)
			}
			if (tc.mode == "no_progress" || tc.mode == "protocol_no_progress") && state.StopReason != "no_progress" {
				t.Fatal("did not stop on original fingerprint")
			}
			if tc.mode == "expiry" && (len(state.RefreshedPlans) != 1 || len(state.BlockedEvidence) != 1) {
				t.Fatal("expired selection was not invalidated/refreshed once", state.RefreshedPlans, state.BlockedEvidence)
			}
			if tc.mode == "expiry_again" && state.StopReason != "automatic_refresh_limit" {
				t.Fatal("second refresh must stop", state.StopReason)
			}
			if tc.mode == "replan" && (trips.saved == nil || len(trips.saved.Tasks[0].Activities) != 1 || trips.saved.Tasks[0].Activities[0].Time != "16:00") {
				t.Fatal("Main replan did not preserve actual content change")
			}
		})
	}
}
