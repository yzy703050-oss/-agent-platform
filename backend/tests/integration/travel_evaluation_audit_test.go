package integration

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	pm "agent-platform/backend/internal/model"
)

func evaluatePerfSample(s *perfSample) {
	s.FailureReasons = nil
	s.RejectedBatches = 0
	s.RootRejections = 0
	s.CollateralRejections = 0
	s.DuplicateSelections = 0
	fail := func(reason string) { s.FailureReasons = append(s.FailureReasons, reason) }
	if s.Error != "" {
		fail("execution_failed")
	}
	tasks, trains, hotels, activities := 0, 0, 0, 0
	for _, p := range s.Plans {
		for _, t := range p.Tasks {
			tasks++
			if t.Train != nil {
				trains++
			}
			if t.Hotel != nil {
				hotels++
			}
			activities += len(t.Activities)
		}
	}
	switch s.Case {
	case "query":
		if len(s.Plans) != 0 || s.ProviderCalls["train"] == 0 || s.ProviderCalls["weather"] == 0 {
			fail("query_scope_or_results_incomplete")
		}
	case "oneway":
		if len(s.Plans) != 1 || tasks != 1 || trains != 1 || hotels != 0 || activities != 0 || s.Revisions < 2 {
			fail("oneway_plan_incomplete")
		}
		if len(s.Plans) == 1 && len(s.Plans[0].Tasks) == 1 {
			t := s.Plans[0].Tasks[0]
			if t.Purpose != "business" || t.Origin != "上海" || t.Destination != "杭州" || t.RequiresHotel || s.Plans[0].RoundTrip {
				fail("oneway_business_intent_changed")
			}
			if len(s.ExpectedDates) > 0 && t.DepartureDate != s.ExpectedDates[0] {
				fail("oneway_date_changed")
			}
		}
	case "multicity":
		if len(s.Plans) != 1 || tasks != 3 || trains != 3 || hotels != 2 || activities < 2 {
			fail("multicity_plan_incomplete")
		}
		if len(s.Plans) == 1 && len(s.Plans[0].Tasks) == 3 {
			cities := []string{"上海", "杭州", "南京", "上海"}
			for i, t := range s.Plans[0].Tasks {
				purpose := "business"
				if i == 2 {
					purpose = "return"
				}
				if t.Purpose != purpose || t.Origin != cities[i] || t.Destination != cities[i+1] {
					fail(fmt.Sprintf("task_%d_business_route_changed", i))
				}
				if len(s.ExpectedDates) == 3 {
					if t.DepartureDate != s.ExpectedDates[i] {
						fail(fmt.Sprintf("task_%d_date_changed", i))
					}
					if i < 2 && (t.CheckIn != s.ExpectedDates[i] || t.CheckOut != s.ExpectedDates[i+1]) {
						fail(fmt.Sprintf("task_%d_stay_changed", i))
					}
				}
				if i < 2 {
					matched := false
					for _, a := range t.Activities {
						if a.Date == t.DepartureDate && a.Time == "14:00" && strings.Contains(a.Purpose, []string{"会议", "交流"}[i]) {
							matched = true
						}
					}
					if !matched {
						fail(fmt.Sprintf("task_%d_activity_changed", i))
					}
				}
			}
		}
	case "modify_activity":
		if len(s.Plans) != 1 || tasks != 1 || trains != 1 || len(s.ProviderCalls) != 0 || s.Revisions <= s.SeedRevisions {
			fail("activity_edit_incomplete_or_requeried")
		}
		if len(s.Plans) == 1 && len(s.Plans[0].Tasks) == 1 {
			t := s.Plans[0].Tasks[0]
			changed := false
			for _, a := range t.Activities {
				if a.Time == "16:00" && (len(s.ExpectedDates) == 0 || a.Date == s.ExpectedDates[0]) {
					changed = true
				}
			}
			if !changed {
				fail("activity_not_saved_at_1600")
			}
			if s.SeedTrainRef == nil || t.Train == nil || t.Train.Ref != *s.SeedTrainRef {
				fail("original_train_reference_changed")
			}
		}
	default:
		fail("unknown_case")
	}
	for _, in := range s.ModelInputs {
		if leaksPerfProvenance(in.Messages) {
			fail("model_input_contains_fixture_disclosure")
		}
	}
	if leaksPerfProvenance(s.FinalText) {
		fail("final_output_contains_fixture_disclosure")
	}
	s.SemanticSuccess = len(s.FailureReasons) == 0
	auditPerfEvents(s)
	s.EfficiencySuccess = s.RejectedBatches == 0 && s.DuplicateSelections == 0
	if s.RejectedBatches > 0 {
		fail("rejected_batches")
	}
	if s.DuplicateSelections > 0 {
		fail("duplicate_selections")
	}
	s.Success = s.SemanticSuccess && s.EfficiencySuccess
}

func leaksPerfProvenance(value any) bool {
	raw, _ := json.Marshal(value)
	text := strings.ToLower(string(raw))
	for _, word := range []string{"模拟数据", "演示", "虚构地址", "虚构条款", "虚构政策", "虚构数据", "demo_policy_not_authoritative", `\"is_demo\":true`} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return strings.Contains(text, `"is_demo":true`)
}

func auditPerfEvents(s *perfSample) {
	type observed struct {
		call  pm.ToolCall
		batch int
	}
	calls := []observed{}
	batchByCall := map[string]int{}
	results := map[string]pm.TravelToolResult{}
	batches := map[int]bool{}
	batch := 0
	for _, e := range s.Events {
		if e.Type != "message" {
			continue
		}
		var m pm.Message
		if json.Unmarshal(e.Payload, &m) != nil {
			continue
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			batch++
			for _, c := range m.ToolCalls {
				calls = append(calls, observed{c, batch})
				batchByCall[c.ID] = batch
			}
		}
		if m.Role == "tool" {
			var r pm.TravelToolResult
			if json.Unmarshal([]byte(m.Content), &r) == nil {
				results[m.ToolCallID] = r
				if r.Status == "rejected" {
					batches[batchByCall[m.ToolCallID]] = true
					if r.Error != nil && r.Error.Code == "BATCH_ABORTED" {
						s.CollateralRejections++
					} else {
						s.RootRejections++
					}
				}
			}
		}
	}
	s.RejectedBatches = len(batches)
	last := map[string]map[string]any{}
	for _, item := range calls {
		if item.call.Name != "write_trip" {
			continue
		}
		r := results[item.call.ID]
		if r.Revision <= 0 || r.Status != "partial" && r.Status != "completed" {
			continue
		}
		var args struct {
			Tripplan struct {
				Operation string `json:"operation"`
				PlanID    string `json:"trip_plan_id"`
				Tasks     []struct {
					TaskID string                           `json:"task_id"`
					Refs   map[string]pm.TravelCandidateRef `json:"selection_refs"`
				} `json:"tasks"`
			} `json:"tripplan"`
		}
		if json.Unmarshal([]byte(item.call.Arguments), &args) != nil {
			continue
		}
		id := r.TripPlanID
		if id == "" {
			id = args.Tripplan.PlanID
		}
		if args.Tripplan.Operation != "select" {
			delete(last, id)
			continue
		}
		refs := map[string]any{}
		for _, t := range args.Tripplan.Tasks {
			for domain, ref := range t.Refs {
				refs[t.TaskID+"/"+domain] = ref
			}
		}
		if len(refs) > 0 && reflect.DeepEqual(last[id], refs) {
			s.DuplicateSelections++
		}
		last[id] = refs
	}
}
