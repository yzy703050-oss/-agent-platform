package integration

import (
	"encoding/json"
	"testing"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
)

func TestPlanningSummaryAudit(t *testing.T) {
	message, _ := json.Marshal(pm.Message{Role: "assistant", ToolCalls: []pm.ToolCall{{Name: "write_trip", Arguments: `{"tripplan":{"operation":"amend","route_tasks":[{"origin":"上海","destination":"南京"}]}}`}}})
	s := perfSample{Calls: []travelagent.ModelCallMeasurement{{Stage: "agent:main", RequestedTools: []string{"write_trip"}}, {Stage: "agent:trip_summary", RequestedTools: []string{"write_trip"}}, {Stage: "agent:trip_summary", RequestedTools: []string{"write_trip"}}}, Events: []contracts.Event{{Type: "message", Payload: message}}}
	a := auditInteraction(s)
	raw, _ := json.Marshal(a)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if got["main_model_calls"] != float64(1) || got["planning_summary_model_calls"] != float64(2) || a.ExtraReasons["replan"] != 1 || a.UnexplainedExtraCalls != 0 {
		t.Fatalf("direct summary route repair not accounted: %s", raw)
	}
	s.Calls[2].Stage = "agent:main"
	raw, _ = json.Marshal(auditInteraction(s))
	_ = json.Unmarshal(raw, &got)
	if got["main_reentries"] != float64(1) {
		t.Fatal("extra Main was accepted as legitimate replan", string(raw))
	}
}

func TestReplanCallShapeCountsActualRecovery(t *testing.T) {
	for _, tc := range []struct {
		name              string
		summary, rejected int
		want              bool
	}{
		{"normal", 2, 0, true},
		{"actual_parameter_recovery", 3, 1, true},
		{"unexplained_extra", 3, 0, false},
		{"too_many_after_one_error", 4, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := interactionSample{perfSample: perfSample{ModelCalls: 1 + tc.summary, RootRejections: tc.rejected}, Audit: interactionAudit{MainCalls: 1, SummaryCalls: tc.summary}}
			if got := validReplanCallShape(s); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
