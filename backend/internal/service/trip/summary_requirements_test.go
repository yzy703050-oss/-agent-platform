package trip

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
)

func TestPlanningSummaryRouteAndHardRequirements(t *testing.T) {
	now := time.Date(2099, 10, 24, 8, 0, 0, 0, beijing)
	yes := true
	base, err := Preview(nil, model.TripCommand{Operation: "propose", RoundTrip: &yes, Context: &model.TripDecisionContext{Role: "main"}, ConfirmedConditions: sourceConditions(`{"end_date":"2099-10-29","constraints":{"total_budget_cny":2000}}`), FieldSources: map[string]string{"end_date": "user", "total_budget_cny": "user"}, Tasks: []model.TripTask{
		{Origin: "上海", Destination: "杭州", Purpose: "visit", RequiresHotel: true, Conditions: sourceConditions(`{"departure_date":"2099-10-27","check_in":"2099-10-27","check_out":"2099-10-28","nights":1}`), FieldSources: map[string]string{"departure_date": "user", "nights": "user", "check_in": "derived", "check_out": "derived"}, ConditionStrengths: map[string]model.ConstraintStrength{"departure_date": model.Soft, "check_in": model.Soft, "check_out": model.Soft, "nights": model.Hard}, Activities: []model.TripActivity{{Purpose: "西湖", Description: "西湖游览", Date: "2099-10-28", Time: "下午", Source: "user", Strength: model.Hard}}},
		{Origin: "杭州", Destination: "上海", Purpose: "return", Conditions: sourceConditions(`{"departure_date":"2099-10-29"}`), FieldSources: map[string]string{"departure_date": "user"}},
	}}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	base.Plan.ID = "p"
	ptr := func(value string) *string { return &value }
	for _, mode := range []string{"valid", "return_date", "budget", "drop_activity", "move_activity", "nights", "reuse_destination_id_for_transit"} {
		t.Run(mode, func(t *testing.T) {
			p, _ := clone(base.Plan)
			rows := []model.TripTaskChange{
				{Origin: ptr("上海"), Destination: ptr("南京"), Purpose: ptr("transit"), Conditions: sourceConditions(`{"departure_date":"2099-10-28"}`)},
				{TaskID: p.Tasks[0].ID, Origin: ptr("南京"), Destination: ptr("杭州"), Conditions: sourceConditions(`{"departure_date":"2099-10-28","check_in":"2099-10-28","check_out":"2099-10-29"}`)},
				{TaskID: p.Tasks[1].ID, Origin: ptr("杭州"), Destination: ptr("上海"), Conditions: sourceConditions(`{"departure_date":"2099-10-29"}`)},
			}
			cmd := model.TripCommand{Operation: "amend", PlanID: "p", ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "planning_summary", HardStayNights: map[string]map[string]int{"p": {"杭州": 1}}}, RouteTasks: &rows}
			switch mode {
			case "reuse_destination_id_for_transit":
				// A copied hard night must not silently follow the destination's
				// identity onto a newly inserted transit leg.
				rows[0].TaskID = p.Tasks[0].ID
				rows[0].Conditions["check_in"] = rawValue("2099-10-28")
				rows[0].Conditions["check_out"] = rawValue("2099-10-29")
				empty := []model.TripActivity{}
				rows[0].Activities = &empty
				rows[1].TaskID = ""
				rows[1].RequiresHotel = &yes
				rows[1].Conditions["nights"] = rawValue(1)
				a := append([]model.TripActivity(nil), p.Tasks[0].Activities...)
				for i := range a {
					a[i].ID = ""
				}
				rows[1].Activities = &a
			case "return_date":
				rows[2].Conditions["departure_date"] = rawValue("2099-10-30")
			case "budget":
				cmd.ConfirmedConditions = sourceConditions(`{"constraints":{"total_budget_cny":3000}}`)
			case "drop_activity":
				a := []model.TripActivity{}
				rows[1].Activities = &a
			case "move_activity":
				a := append([]model.TripActivity(nil), p.Tasks[0].Activities...)
				a[0].Time = "晚上"
				rows[1].Activities = &a
			case "nights":
				rows[1].Conditions["nights"] = rawValue(2)
				rows[1].Conditions["check_out"] = rawValue("2099-10-30")
			}
			before, _ := json.Marshal(p)
			out, err := Preview(&p, cmd, nil, now)
			if mode == "valid" {
				if err != nil || len(out.Plan.Tasks) != 3 {
					t.Fatalf("summary cannot do authorized route repair: %v", err)
				}
			} else {
				var rule *model.TravelRuleError
				if !errors.As(err, &rule) || (rule.Code != "SUMMARY_HARD_REQUIREMENT" && rule.Code != "HARD_STAY_REQUIREMENT") {
					t.Fatalf("hard requirement %s not specifically protected: %v", mode, err)
				}
			}
			after, _ := json.Marshal(p)
			if string(before) != string(after) {
				t.Fatal("preview mutated saved input")
			}
		})
	}
}
