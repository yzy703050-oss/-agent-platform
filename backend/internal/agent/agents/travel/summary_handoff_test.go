package travel

import (
	"encoding/json"
	"testing"
)

func TestMainOnceRoleTransitions(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"first", `{}`, "main"},
		{"saved", `{"main_used":true,"planning_started":true,"plans":{"p":{"plan":{"id":"p"}}},"results":[{"name":"write_trip","status":"partial","trip_plan_id":"p"}]}`, "planning_summary"},
		{"legacy_replan_never_reenters_main", `{"main_used":true,"planning_started":true,"plans":{"p":{"plan":{"id":"p"}}},"results":[{"name":"request_replan","status":"ok"}]}`, "planning_summary"},
		{"rejected_proposal", `{"main_used":true,"planning_started":true,"results":[{"name":"write_trip","status":"rejected"}]}`, "planning_summary"},
		{"bad_generation", `{"main_used":true,"generation_failures":1}`, "planning_summary"},
		{"policy_once", `{"main_used":true,"planning_intent":true,"policy_context":{"status":"ok"},"results":[{"name":"query_policy","status":"ok"}]}`, "main"},
		{"policy_already_continued", `{"main_used":true,"policy_continuation_used":true,"planning_intent":true,"policy_context":{"status":"ok"},"results":[{"name":"query_policy","status":"ok"}]}`, "planning_summary"},
		{"failed_policy", `{"main_used":true,"planning_intent":true,"results":[{"name":"query_policy","status":"error"}]}`, "planning_summary"},
		{"lookup", `{"main_used":true,"results":[{"name":"query","status":"ok"}]}`, "summary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s FlowState
			if err := json.Unmarshal([]byte(tc.raw), &s); err != nil {
				t.Fatal(err)
			}
			if got := s.DecideRole(); got != tc.want {
				t.Fatalf("got role %s, want %s", got, tc.want)
			}
			raw, _ := json.Marshal(s)
			var restored FlowState
			_ = json.Unmarshal(raw, &restored)
			if restored.DecideRole() != tc.want {
				t.Fatal("handoff flags lost on restore")
			}
		})
	}
}

func TestStoppedGenerationCannotRebindPlanningTools(t *testing.T) {
	s := FlowState{Role: "planning_summary", MainUsed: true, StopReason: "planning_round_limit_exceeded"}
	if request := profileForState(s, s.Role); request.Role != "summary" {
		t.Fatalf("generation override reopens tools after stop: %+v", request)
	}
}
