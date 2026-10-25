package travel

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestTravelTerminalSnapshot(t *testing.T) {
	for _, phase := range []string{"completed", "stopped", "paused", "unknown"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			trips := &sourceGateTrips{}
			flow := newSourceFlow(flowConfig{Trips: trips})
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips}})
			if err != nil {
				t.Fatal(err)
			}
			flow.registry = registry
			m := &sourceGateModel{}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 4, NativeFinalize: flow.finalize, NativeBeforeModel: func(ctx context.Context, history []*schema.Message) ([]*schema.Message, string, error) {
				// Loading a snapshot is not a live transition. Paused must not be
				// reactivated, while a saved final response may be replayed safely.
				raw, _ := json.Marshal(FlowState{Version: 1, Phase: phase, Role: "main", FinalText: "已保存的结果。"})
				adk.AddSessionValue(ctx, flowKey, string(raw))
				return flow.beforeModel(ctx, history)
			}})
			if err != nil {
				t.Fatal(err)
			}
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "继续")
			failed := false
			errorCode := ""
			final := ""
			for {
				e, ok := it.Next()
				if !ok {
					break
				}
				failed = failed || e.Err != nil
				var rule *pm.TravelRuleError
				if errors.As(e.Err, &rule) {
					errorCode = rule.Code
				}
				if e.Output != nil && e.Output.MessageOutput != nil {
					msg, err := e.Output.MessageOutput.GetMessage()
					if err == nil {
						final = msg.Content
					}
				}
			}
			wantFailure := phase == "paused" || phase == "unknown"
			if phase == "unknown" && errorCode != "INVALID_EXECUTION_STATE" || phase == "paused" && errorCode != "EXECUTION_STOPPED" {
				t.Errorf("wrong boundary error for %s: %s", phase, errorCode)
			}
			if failed != wantFailure || m.calls.Load() != 0 || trips.writes.Load() != 0 || !wantFailure && final != "已保存的结果。" {
				t.Fatalf("phase=%s failed=%v calls=%d writes=%d final=%q", phase, failed, m.calls.Load(), trips.writes.Load(), final)
			}
		})
	}
}
