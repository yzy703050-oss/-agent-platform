package travel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeToolInstructions(t *testing.T) {
	for _, profile := range []string{"main", "pre_policy", "review", "review_retry", "summary", "planning_summary", "planning_recovery"} {
		t.Run(profile, func(t *testing.T) {
			base, err := SourceProfile(profile)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(base)
			got := withRuntimeToolInstructions(profile, base)
			if len(base) != len(got) {
				t.Fatal("changed tool permissions")
			}
			for i, tool := range got {
				if tool.Name != base[i].Name || !reflect.DeepEqual(tool.ParamsOneOf, base[i].ParamsOneOf) {
					t.Fatal("changed tool name or parameter schema")
				}
				if tool.Name == "write_trip" {
					if profile == "planning_recovery" && strings.Contains(tool.Desc, `"operation":"select"`) {
						t.Fatal("create-only recovery still receives selection/reply example")
					}
					if profile == "planning_summary" && strings.Contains(tool.Desc, "propose：完整提交") {
						t.Fatal("saved-plan summary still receives creation instructions")
					}
					if strings.Contains(tool.Desc, TripArgumentHelp) {
						t.Fatal("frozen monolithic argument text still injected after canonical help")
					}
					for _, required := range []string{"conditions.arrival_before", "condition_strengths.departure_date", "POLICY_OBSERVATION_REQUIRED", "BATCH_ABORTED", "confirmed_conditions", "round_summary"} {
						if !strings.Contains(tool.Desc, required) {
							t.Errorf("missing field/error guidance %s", required)
						}
					}
					if profile == "planning_recovery" {
						if !strings.Contains(tool.Desc, "default_round_trip") {
							t.Fatal("creation lost default round-trip contract")
						}
					} else {
						for _, key := range []string{"route_tasks", "selection_refs对应的train/hotel"} {
							if !strings.Contains(tool.Desc, key) {
								t.Fatalf("modify profile missing %s", key)
							}
						}
					}
				} else if strings.Contains(tool.Desc, "conditions.arrival_before") {
					t.Fatalf("write syntax leaked to %s", tool.Name)
				}
			}
			after, _ := json.Marshal(base)
			if string(before) != string(after) {
				t.Fatal("mutated input profile")
			}
			again, _ := SourceProfile(profile)
			fresh, _ := json.Marshal(again)
			if string(before) != string(fresh) {
				t.Fatal("polluted source profile")
			}
		})
	}
}

func TestRuntimeMainRequestInstructions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		request   ProfileRequest
		forbidden string
	}{
		{"new_plan", ProfileRequest{Role: "main", NewPlanningRequest: true}, `"operation":"select"`},
		{"existing_edit", ProfileRequest{Role: "main", PlanContextComplete: true}, "propose：完整提交"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, err := (&Registry{}).Profile(tc.request)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range tools {
				if tool.Name == "write_trip" && strings.Contains(tool.Desc, tc.forbidden) {
					t.Fatal("Main received tool examples for an unrelated request phase")
				}
			}
		})
	}
}
