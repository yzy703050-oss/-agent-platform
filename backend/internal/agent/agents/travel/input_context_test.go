package travel

import (
	"reflect"
	"testing"

	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
)

func TestTravelExplicitTripContext(t *testing.T) {
	id := "b4d4e691-781f-49cb-8bba-4172c82490d2"
	for _, tc := range []struct {
		name, query string
		resume      bool
		want        []string
	}{
		{"explicit_edit", "请先读取已保存行程 " + id + "，只把客户会议从14:00改为16:00，日期不变。", false, []string{id}},
		{"read_only", "只查看行程 " + id, false, []string{}},
		{"negated", "不要修改行程 " + id, false, []string{}},
		{"negated_other_trip", "不要修改行程 11111111-1111-1111-1111-111111111111，把行程 " + id + "的会议改为16:00", false, []string{id}},
		{"hypothetical", "假设修改行程 " + id, false, []string{}},
		{"resume_uses_checkpoint", "修改行程 " + id, true, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := contracts.RunInput{Run: pm.Run{ID: "current"}, Resume: tc.resume, Messages: []pm.Message{{RunID: "excluded", Role: "user", Content: "修改行程 11111111-1111-1111-1111-111111111111"}, {RunID: "current", Role: "user", Content: tc.query}}}
			_, ids := sourceInput(in)
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("provided context IDs=%v want=%v", ids, tc.want)
			}
		})
	}
}

func TestTravelProvidedPlanIsCompleteForFirstMain(t *testing.T) {
	id := "b4d4e691-781f-49cb-8bba-4172c82490d2"
	s := FlowState{OriginalQuery: "把行程 " + id + "的会议改为16:00", Plans: map[string]pm.TripResult{id: {Plan: pm.TripPlan{ID: id}}}}
	if !profileForState(s, "main").PlanContextComplete {
		t.Fatal("first Main already has a complete snapshot but may redundantly read it")
	}
}
