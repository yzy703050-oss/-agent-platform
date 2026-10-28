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
					if strings.Count(tool.Desc, TripArgumentHelp) != 1 {
						t.Fatal("missing or repeated original argument help")
					}
					for _, required := range []string{"conditions.arrival_before", "condition_strengths.departure_date", "route_tasks", "POLICY_OBSERVATION_REQUIRED", "BATCH_ABORTED"} {
						if !strings.Contains(tool.Desc, required) {
							t.Errorf("missing field/error guidance %s", required)
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
