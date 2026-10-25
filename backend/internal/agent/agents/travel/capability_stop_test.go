package travel

import (
	"context"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
)

type capabilityResultTrips struct {
	sourceGateTrips
	result pm.TripResult
}

func (f *capabilityResultTrips) Apply(ctx context.Context, ref pm.TravelExecution, id string, cmd pm.TripCommand) (pm.TripResult, error) {
	r, err := f.sourceGateTrips.Apply(ctx, ref, id, cmd)
	if err != nil {
		return r, err
	}
	r.Plan.Status = "running"
	r.Plan.StopReason = "provider_capability_missing"
	r.Plan.Tasks[0].Hotel = &pm.TravelSelection{Candidate: pm.TravelCandidate{Name: "杭州商务酒店", City: "杭州"}}
	r.Plan.Validation = pm.TripValidation{Status: "partial", Issues: []pm.TripIssue{{Code: "hotel_price_unknown", TaskID: "t", Blocking: true, Message: "酒店地点不含报价。"}, {Code: "policy_constraint_unverified", TaskID: "t", Blocking: true, Evidence: mustMarshal("hotel_rule")}}}
	r.Validation = r.Plan.Validation
	f.result = r
	return r, nil
}
func (f *capabilityResultTrips) Inspect(context.Context, pm.TravelExecution, string) (pm.TripResult, error) {
	return f.result, nil
}

func TestCapabilityResultStopsWithoutAnotherModelCall(t *testing.T) {
	ctx := context.Background()
	trips := &capabilityResultTrips{}
	ref := pm.TravelExecution{Scope: pm.Scope{UserID: "u", SessionID: "s"}, ConversationID: "c", RunID: "r", Attempt: 1}
	flow := newSourceFlow(flowConfig{Trips: trips, Execution: ref, OriginalQuery: "保留可用安排并保存"})
	registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips, Execution: ref}, Context: flow.decision, OnNativeTrip: flow.recordTrip})
	if err != nil {
		t.Fatal(err)
	}
	flow.registry = registry
	m := &sourceGateModel{}
	kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 4, NativeBeforeModel: flow.beforeModel, NativeModelObserved: flow.modelObserved, NativeReserveBatch: flow.reserve, NativeProfile: flow.profile, NativeAfterBatch: flow.afterBatch, Handlers: []adk.ChatModelAgentMiddleware{&sourceGateIdentity{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}}})
	if err != nil {
		t.Fatal(err)
	}
	it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "保留可用安排并保存")
	final := ""
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		if e.Err != nil {
			t.Fatal(e.Err)
		}
		if e.Output != nil && e.Output.MessageOutput != nil {
			msg, err := e.Output.MessageOutput.GetMessage()
			if err == nil && len(msg.ToolCalls) == 0 {
				final = msg.Content
			}
		}
	}
	if m.calls.Load() != 1 || trips.writes.Load() != 1 || final == "" {
		t.Fatalf("model=%d writes=%d final=%q", m.calls.Load(), trips.writes.Load(), final)
	}
}
