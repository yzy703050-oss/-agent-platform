package integration

import (
	"encoding/json"
	"regexp"
	"slices"
	"testing"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
)

type interactionAudit struct {
	MainCalls             int            `json:"main_model_calls"`
	SummaryCalls          int            `json:"planning_summary_model_calls"`
	MainReentries         int            `json:"main_reentries"`
	PlanningCalls         int            `json:"planning_model_calls"`
	PolicyCalls           int            `json:"policy_model_calls"`
	MaintenanceCalls      int            `json:"maintenance_model_calls"`
	CompactionCalls       int            `json:"compaction_model_calls"`
	HistoryReads          int            `json:"active_history_reads"`
	PlanReads             int            `json:"current_plan_reads"`
	Followup              bool           `json:"followup_or_confirmation"`
	DuplicateSelections   int            `json:"duplicate_selections"`
	UnexplainedExtraCalls int            `json:"unexplained_extra_calls"`
	ExtraReasons          map[string]int `json:"extra_call_reasons"`
}

// Acceptance-only signal: actual replies are also reviewed with their full
// trace. This does not rewrite production output or serve as a semantic router.
var interactionFollowup = regexp.MustCompile(`[?？]|请.{0,8}(补充|确认|提供)|告诉我|从哪.{0,6}出发|几人出行|住几晚|补齐后再`)

func auditInteraction(s perfSample) interactionAudit {
	a := interactionAudit{ExtraReasons: map[string]int{}, Followup: interactionFollowup.MatchString(s.FinalText)}
	for _, call := range s.Calls {
		if call.Stage == "agent:main" {
			a.MainCalls++
		}
		if call.Stage == "agent:trip_summary" {
			a.SummaryCalls++
		}
		switch {
		case call.Stage == "agent:travel_policy" || call.Stage == "agent:policy" || len(call.RequestedTools) == 1 && call.RequestedTools[0] == "query_policy":
			a.PolicyCalls++
		case call.Stage == "agent:preference_analysis":
			a.MaintenanceCalls++
		case call.Stage == "main:summary":
			a.CompactionCalls++
		default:
			a.PlanningCalls++
		}
		if slices.Contains(call.RequestedTools, "request_replan") {
			a.ExtraReasons["replan"]++
		}
		if call.ErrorCode != "" {
			a.ExtraReasons["provider_error"]++
		}
	}
	for _, event := range s.Events {
		if event.Type == "travel.model_rejected" {
			var rejected struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal(event.Payload, &rejected) == nil && rejected.Reason == "user_input_requested" {
				a.ExtraReasons["interaction_regeneration"]++
			}
		}
		if event.Type != "message" {
			continue
		}
		var msg pm.Message
		if json.Unmarshal(event.Payload, &msg) != nil {
			continue
		}
		for _, call := range msg.ToolCalls {
			var args struct {
				Resource string `json:"resource"`
				Tripplan struct {
					Operation  string                       `json:"operation"`
					Tasks      []map[string]json.RawMessage `json:"tasks"`
					RouteTasks []json.RawMessage            `json:"route_tasks"`
				} `json:"tripplan"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) != nil {
				continue
			}
			if call.Name == "read" && args.Resource != "candidates" {
				if args.Resource == "trip" {
					a.PlanReads++
					a.ExtraReasons["plan_refresh"]++
				} else {
					a.HistoryReads++
				}
			}
			if call.Name == "write_trip" && args.Tripplan.Operation == "amend" {
				if len(args.Tripplan.RouteTasks) > 0 {
					a.ExtraReasons["replan"]++
				}
				for _, task := range args.Tripplan.Tasks {
					if len(task["content"]) > 0 || len(task["conditions"]) > 0 || len(task["items"]) > 0 || len(task["origin"]) > 0 || len(task["destination"]) > 0 {
						a.ExtraReasons["amend"]++
						break
					}
				}
			}
		}
		if msg.Role == "tool" {
			var r pm.TravelToolResult
			if json.Unmarshal([]byte(msg.Content), &r) == nil && (r.Status == "error" || r.Status == "rejected") {
				a.ExtraReasons["tool_feedback"]++
			}
		}
	}
	auditPerfEvents(&s)
	a.DuplicateSelections = s.DuplicateSelections
	a.UnexplainedExtraCalls = max(0, a.PlanningCalls-2)
	allowance := 2*a.ExtraReasons["replan"] + a.ExtraReasons["amend"] + a.ExtraReasons["tool_feedback"] + a.ExtraReasons["interaction_regeneration"] + a.ExtraReasons["provider_error"] + a.ExtraReasons["plan_refresh"]
	a.UnexplainedExtraCalls = max(0, a.UnexplainedExtraCalls-allowance)
	mainLimit := 1
	if a.PolicyCalls > 0 {
		mainLimit = 2
	}
	a.MainReentries = max(0, a.MainCalls-mainLimit)
	return a
}

func TestTravelInteractionAudit(t *testing.T) {
	t.Run("two_calls_can_still_be_a_questionnaire", func(t *testing.T) {
		m, _ := json.Marshal(pm.Message{Role: "assistant", ToolCalls: []pm.ToolCall{{ID: "read", Name: "read", Arguments: `{"request_kind":"lookup","resource":"session_summaries"}`}}})
		s := perfSample{FinalText: "请告诉我出发地和人数，补齐后再安排。", Calls: []travelagent.ModelCallMeasurement{{Stage: "agent:main", RequestedTools: []string{"read"}}, {Stage: "agent:summary"}}, Events: []contracts.Event{{Type: "message", Payload: m}}}
		a := auditInteraction(s)
		if !a.Followup || a.HistoryReads != 1 || a.PlanningCalls != 2 {
			t.Fatalf("two-call questionnaire was not detected: %+v", a)
		}
	})
	t.Run("policy_and_maintenance_are_counted_separately", func(t *testing.T) {
		s := perfSample{Calls: []travelagent.ModelCallMeasurement{{Stage: "agent:main", RequestedTools: []string{"query_policy"}}, {Stage: "agent:policy"}, {Stage: "agent:preference_analysis"}, {Stage: "main:summary"}, {Stage: "agent:main", RequestedTools: []string{"write_trip"}}, {Stage: "agent:trip_review", RequestedTools: []string{"write_trip"}}}}
		a := auditInteraction(s)
		if a.PlanningCalls != 2 || a.PolicyCalls != 2 || a.MaintenanceCalls != 1 || a.CompactionCalls != 1 {
			t.Fatal("auxiliary calls disappeared from cost accounting", a)
		}
	})
	t.Run("real_replan_is_not_a_two_call_limit", func(t *testing.T) {
		s := perfSample{Calls: []travelagent.ModelCallMeasurement{{Stage: "agent:main", RequestedTools: []string{"write_trip"}}, {Stage: "agent:trip_review", RequestedTools: []string{"request_replan"}}, {Stage: "agent:main", RequestedTools: []string{"write_trip"}}, {Stage: "agent:trip_review", RequestedTools: []string{"write_trip"}}}}
		a := auditInteraction(s)
		if a.PlanningCalls != 4 || a.UnexplainedExtraCalls != 0 || a.ExtraReasons["replan"] != 1 {
			t.Fatal("real replan counted as unexplained overhead", a)
		}
	})
	t.Run("extra_plain_summary_has_no_progress", func(t *testing.T) {
		s := perfSample{Calls: []travelagent.ModelCallMeasurement{{Stage: "agent:main", RequestedTools: []string{"write_trip"}}, {Stage: "agent:trip_review", RequestedTools: []string{"write_trip"}}, {Stage: "agent:summary"}}}
		if a := auditInteraction(s); a.UnexplainedExtraCalls != 1 {
			t.Fatal("redundant final model call hidden", a)
		}
	})
}
