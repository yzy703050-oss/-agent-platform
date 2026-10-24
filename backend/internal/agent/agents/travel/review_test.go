package travel

import (
	"encoding/json"
	"strings"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
)

func TestSourceReviewProjection(t *testing.T) {
	policy := &pm.SourcePolicyContext{Status: "ok"}
	policyJSON := mustMarshal(policy)
	s := FlowState{OriginalQuery: "完整三段差旅", Plans: map[string]pm.TripResult{}, Policy: policy,
		PreviousRoundSummary: "保留全部路线与会议", RawResults: []json.RawMessage{mustMarshal(strings.Repeat("PRIVATE_FULL_AUDIT", 10000))},
		Results:           []pm.TravelToolResult{{CallID: "policy", Name: "query_policy", Status: "ok", Data: policyJSON}, {CallID: "read", Name: "read", Status: "ok", Data: json.RawMessage(`{"trip_plan_id":"p","tasks":["DUPLICATE_READ"]}`)}},
		AdjustmentHistory: []json.RawMessage{json.RawMessage(`{"operation":"propose","trip_plan_id":"p","changes":{"tasks":["DUPLICATE_INITIAL"]}}`)},
	}
	p := pm.TripPlan{ID: "p", Revision: 2, PolicyContext: policy, ConstraintSnapshot: mustMarshal(map[string]any{"policy": policy}), Tasks: []pm.TripTask{}}
	for _, id := range []string{"t1", "t2", "t3"} {
		p.Tasks = append(p.Tasks, pm.TripTask{ID: id, Revision: 1, Origin: "上海", Destination: "杭州", Activities: []pm.TripActivity{{ID: "a-" + id, Purpose: "客户会议", Time: "14:00"}}})
	}
	s.Plans["p"] = pm.TripResult{Plan: p}
	e := pm.TravelEvidence{ID: "q", ResultRevision: 1, Status: "ok", Query: pm.TravelQuery{PlanID: "p", TaskID: "t1", TaskRevision: 1, Domain: "train"}, Candidates: []pm.TravelCandidate{{ID: "KEEP_CANDIDATE_ID", Name: "G1"}}}
	s.Jobs = []traveltools.QueryJob{{ID: "job", ParentCallID: "create", Query: e.Query, Result: &e}}
	s.Results = append(s.Results, pm.TravelToolResult{CallID: "lookup", Name: "query", Status: "ok", Data: mustMarshal([]any{traveltools.NativeEvidenceView(e)})})
	view := buildSourceReview(s)
	raw := string(mustMarshal(view))
	for _, unwanted := range []string{"PRIVATE_FULL_AUDIT", "DUPLICATE_INITIAL", "DUPLICATE_READ"} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("review duplicates durable audit: %s", unwanted)
		}
	}
	for _, wanted := range []string{"t1", "t2", "t3", "a-t1", "14:00", "initial_plan_in_current_snapshot", "policy_in_tripplan", "in_current_snapshot", "tripplan.policy_context"} {
		if !strings.Contains(raw, wanted) {
			t.Errorf("lost review fact/reference %s", wanted)
		}
	}
	previous, ok := view["previous_round_summary"].(map[string]any)
	if !ok || previous["summary"] != s.PreviousRoundSummary {
		t.Fatalf("previous round must retain source object: %v", view["previous_round_summary"])
	}
	facts, ok := view["reply_facts"].(map[string]any)
	if !ok || facts["booking_performed"] != false {
		t.Fatalf("reply facts must be receipt flags, not full validation: %v", view["reply_facts"])
	}
	if len(s.RawResults) != 1 || len(s.RawResults[0]) < 10000 {
		t.Fatal("projection mutated durable audit")
	}
	if strings.Count(raw, "KEEP_CANDIDATE_ID") != 2 || !strings.Contains(raw, "candidate_order") || !strings.Contains(raw, "candidates_in_tripplan") {
		t.Fatal("source query must link identical candidates and retain candidate order", raw)
	}
	if strings.Contains(string(mustMarshal(view["execution_results"])), "KEEP_CANDIDATE_ID") {
		t.Fatal("system execution summary repeated full query body")
	}
	// Rejected writes have no saved plan, but the verifier must see the attempted arguments.
	s = FlowState{LastBatchCalls: []traveltools.NativeCall{{ID: "bad", Name: "write_trip", Arguments: json.RawMessage(`{"tripplan":{"tasks":[{"id":"attempted"}]}}`)}}, LastBatchResults: []pm.TravelToolResult{{CallID: "bad", Name: "write_trip", Status: "error"}}}
	view = buildSourceReview(s)
	if !strings.Contains(string(mustMarshal(view["tripplan"])), "attempted") {
		t.Fatal("rejected attempted plan lost")
	}
}

func TestSourceProgressCanonicalizesJSONB(t *testing.T) {
	p := pm.TripResult{Plan: pm.TripPlan{ID: "p", Tasks: []pm.TripTask{{ID: "t", Conditions: pm.TripConditions{"constraints": json.RawMessage(`{"seat_class":"二等座","train_max_total_cny":500}`)}}}}}
	s := FlowState{Plans: map[string]pm.TripResult{"p": p}}
	s.ObserveReview()
	p.Plan.Tasks[0].Conditions["constraints"] = json.RawMessage(`{"train_max_total_cny":500,"seat_class":"二等座"}`)
	s.Plans["p"] = p
	s.ObserveReview()
	s.ObserveReview()
	if s.StopReason != "no_progress" {
		t.Fatal("JSONB key ordering caused false business progress", s.UnchangedReviews)
	}
}
