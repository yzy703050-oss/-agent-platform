package travel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type correctionProbeModel struct {
	t           *testing.T
	calls       int
	mode        string
	sawFeedback bool
}

func (m *correctionProbeModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *correctionProbeModel) Generate(ctx context.Context, messages []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	m.calls++
	if m.calls == 1 {
		args := `{"request_kind":"planning","tripplan":{"operation":"propose","tasks":[{"origin":"上海","destination":"杭州","purpose":"business","conditions":{"departure_date":"2026-11-01"}}]}}`
		if m.mode == "arrival" {
			args = `{"request_kind":"planning","tripplan":{"operation":"propose","tasks":[{"origin":"上海","destination":"杭州","conditions":{"departure_date":"2026-11-01","constraints":{"arrival_before":"2026-11-01T13:00:00+08:00"}}}]}}`
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "read", Type: "function", Function: schema.FunctionCall{Name: "read", Arguments: `{"request_kind":"planning","resource":"trip_summaries"}`}}, {ID: "bad", Type: "function", Function: schema.FunctionCall{Name: "write_trip", Arguments: args}}}}, nil
	}
	raw, _ := json.Marshal(messages)
	value, _ := adk.GetSessionValue(ctx, flowKey)
	var state FlowState
	textState, _ := value.(string)
	_ = json.Unmarshal([]byte(textState), &state)
	if state.Phase != "repair_required" {
		m.t.Errorf("rejected batch must reach next model in repair_required, got %s", state.Phase)
	}
	text := string(raw)
	expected := "POLICY_OBSERVATION_REQUIRED"
	hint := "query_policy"
	if m.mode == "arrival" {
		expected = "INVALID_ARGUMENTS"
		hint = "tripplan.tasks[0].conditions.arrival_before"
	}
	if !strings.Contains(text, expected) || !strings.Contains(text, hint) || !strings.Contains(text, "BATCH_ABORTED") || !strings.Contains(text, "保持business") {
		m.t.Errorf("next Generate lacks actionable feedback/prompt: code=%v hint=%v sibling=%v intent=%v", strings.Contains(text, expected), strings.Contains(text, hint), strings.Contains(text, "BATCH_ABORTED"), strings.Contains(text, "保持business"))
	}
	var review struct {
		Previous struct {
			Results []pm.TravelToolResult `json:"results"`
		} `json:"previous_round_summary"`
	}
	if json.Unmarshal([]byte(messages[len(messages)-1].Content), &review) != nil || len(review.Previous.Results) != 2 {
		m.t.Fatal("paired review observations missing")
	}
	for _, r := range review.Previous.Results {
		if r.Error == nil {
			m.t.Fatal("missing structured tool error")
		}
		var root string
		var executed bool
		_ = json.Unmarshal(r.Error.Details["failed_call_id"], &root)
		_ = json.Unmarshal(r.Error.Details["executed"], &executed)
		if root != "bad" || executed {
			m.t.Error("incorrect root/side effect attribution", r)
		}
		if r.CallID == "read" && r.Error.Code != "BATCH_ABORTED" {
			m.t.Error("valid read blamed for other call's field error")
		}
	}
	m.sawFeedback = true
	return schema.AssistantMessage("已收到具体纠错信息。", nil), nil
}
func (m *correctionProbeModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, e := m.Generate(ctx, in, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), e
}

func TestTravelCorrectionReachesNextModel(t *testing.T) {
	for _, mode := range []string{"policy", "arrival"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			trips := &sourceGateTrips{}
			ref := pm.TravelExecution{Scope: pm.Scope{UserID: "u", SessionID: "s"}, ConversationID: "conv", RunID: "run", Attempt: 1}
			store := &sourceJournalStore{decisions: map[string]pm.TravelDecision{}, results: map[string]string{}}
			flow := newSourceFlow(flowConfig{Trips: trips, Execution: ref, OriginalQuery: "安排上海到杭州出差", Decisions: store})
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips, Execution: ref}, Context: flow.decision})
			if err != nil {
				t.Fatal(err)
			}
			flow.registry = registry
			h := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, ref: ref, native: true, flow: flow}
			m := &correctionProbeModel{t: t, mode: mode}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 4, NativeBeforeModel: flow.beforeModel, NativeModelObserved: flow.modelObserved, NativeReserveBatch: flow.reserve, NativeProfile: flow.profile, NativeAfterBatch: flow.afterBatch, NativeRejected: h.recordRejected, Handlers: []adk.ChatModelAgentMiddleware{h}})
			if err != nil {
				t.Fatal(err)
			}
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "安排上海到杭州出差")
			for {
				e, ok := it.Next()
				if !ok {
					break
				}
				if e.Err != nil {
					t.Fatal(e.Err)
				}
			}
			if m.calls != 2 || !m.sawFeedback || trips.writes.Load() != 0 || len(store.results) != 2 {
				t.Fatalf("calls=%d feedback=%v writes=%d durable results=%d", m.calls, m.sawFeedback, trips.writes.Load(), len(store.results))
			}
		})
	}
}
