package integration

import (
	"slices"
	"strings"
	"testing"
	"time"

	pm "agent-platform/backend/internal/model"
)

func defaultPlanningFailures(s perfSample, tomorrow string) []string {
	failures := []string{}
	if s.StageCalls["agent:trip_review"] == 0 {
		failures = append(failures, "default_plan_missing_verify")
	}
	if len(s.Plans) != 1 {
		return append(failures, "default_plan_not_saved_once")
	}
	p := s.Plans[0]
	if p.Validation.Status != "complete" {
		failures = append(failures, "default_plan_not_complete")
	}
	if len(p.Tasks) != 2 {
		return append(failures, "default_roundtrip_incomplete")
	}
	goTo, back := p.Tasks[0], p.Tasks[1]
	if goTo.Origin != "重庆" || goTo.Destination != "上海" || back.Origin != "上海" || back.Destination != "重庆" {
		failures = append(failures, "default_route_changed")
	}
	if goTo.DepartureDate != tomorrow || back.DepartureDate <= tomorrow {
		failures = append(failures, "default_dates_invalid")
	}
	if goTo.Train == nil || back.Train == nil {
		failures = append(failures, "default_train_not_selected")
	}
	if !goTo.RequiresHotel || goTo.Hotel == nil || goTo.Nights < 1 || goTo.CheckOut != back.DepartureDate {
		failures = append(failures, "default_hotel_not_selected")
	}
	if len(goTo.Activities) == 0 {
		failures = append(failures, "default_activities_missing")
	}
	if back.Train != nil && back.Train.Candidate.DepartureAt != nil && back.Train.Candidate.DepartureAt.In(time.FixedZone("Asia/Shanghai", 8*3600)).Hour() < 12 {
		for _, activity := range goTo.Activities {
			if activity.Date == back.DepartureDate && activity.Time == "上午" {
				failures = append(failures, "default_return_activity_conflict")
			}
		}
	}
	if !strings.Contains(s.FinalText, "重庆→上海") || !strings.Contains(s.FinalText, "上海→重庆") || !strings.Contains(s.FinalText, "住宿：") {
		failures = append(failures, "default_reply_incomplete")
	}
	for _, field := range []string{"origin", "nights", "check_out", "passengers", "guests", "seat_class"} {
		if goTo.FieldSources[field] == "user" {
			failures = append(failures, "default_falsely_user_confirmed:"+field)
		}
	}
	return failures
}

func TestDefaultPlanningAcceptance(t *testing.T) {
	partial := perfSample{StageCalls: map[string]int{"agent:main": 1}, Plans: []pm.TripPlan{{Tasks: []pm.TripTask{{Destination: "上海"}}, Validation: pm.TripValidation{Status: "needs_input"}}}}
	if failures := defaultPlanningFailures(partial, "2099-10-27"); !slices.Contains(failures, "default_plan_not_complete") || !slices.Contains(failures, "default_plan_missing_verify") {
		t.Fatalf("old sparse partial result passed new acceptance: %v", failures)
	}
	complete := perfSample{FinalText: "重庆→上海：住宿：上海→重庆：", StageCalls: map[string]int{"agent:main": 1, "agent:trip_review": 1}, Plans: []pm.TripPlan{{Tasks: []pm.TripTask{
		{Origin: "重庆", Destination: "上海", DepartureDate: "2099-10-27", Train: &pm.TravelSelection{}, RequiresHotel: true, Hotel: &pm.TravelSelection{}, CheckIn: "2099-10-27", CheckOut: "2099-10-29", Nights: 2, Activities: []pm.TripActivity{{Purpose: "游览", Date: "2099-10-28"}}},
		{Origin: "上海", Destination: "重庆", DepartureDate: "2099-10-29", Train: &pm.TravelSelection{}},
	}, Validation: pm.TripValidation{Status: "complete"}}}}
	if failures := defaultPlanningFailures(complete, "2099-10-27"); len(failures) != 0 {
		t.Fatalf("complete default route failed: %v", failures)
	}
	complete.FinalText = "已保存活动变化，其余安排不变。"
	if failures := defaultPlanningFailures(complete, "2099-10-27"); !slices.Contains(failures, "default_reply_incomplete") {
		t.Fatalf("local amendment reply hid the initial full plan: %v", failures)
	}
	complete.FinalText = "重庆→上海：住宿：上海→重庆："
	returnTime := time.Date(2099, 10, 29, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	complete.Plans[0].Tasks[1].Train.Candidate.DepartureAt = &returnTime
	complete.Plans[0].Tasks[0].Activities = []pm.TripActivity{{Purpose: "上午游览", Date: "2099-10-29", Time: "上午"}}
	if failures := defaultPlanningFailures(complete, "2099-10-27"); !slices.Contains(failures, "default_return_activity_conflict") {
		t.Fatalf("morning activity was accepted with morning long-distance return: %v", failures)
	}
	complete.Plans[0].Tasks[0].Activities = []pm.TripActivity{{Purpose: "游览", Date: "2099-10-28", Time: "上午"}}
	complete.Plans[0].Tasks[0].Hotel = nil
	if failures := defaultPlanningFailures(complete, "2099-10-27"); !slices.Contains(failures, "default_hotel_not_selected") {
		t.Fatalf("missing hotel passed: %v", failures)
	}
}
