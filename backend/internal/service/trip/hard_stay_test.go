package trip

import (
	"errors"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
)

func TestTravelMainCannotChangeCurrentRequestHardNights(t *testing.T) {
	no := false
	base, err := Preview(nil, model.TripCommand{Operation: "propose", RoundTrip: &no, Context: &model.TripDecisionContext{Role: "main"}, Tasks: []model.TripTask{{Origin: "上海", Destination: "杭州", RequiresHotel: true, Conditions: sourceConditions(`{"departure_date":"2099-10-25","check_in":"2099-10-25","nights":1}`)}}}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	base.Plan.ID = "p"
	for _, nights := range []int{1, 2} {
		checkout := time.Date(2099, 10, 25+nights, 0, 0, 0, 0, beijing).Format(time.DateOnly)
		cmd := model.TripCommand{Operation: "amend", PlanID: "p", ExpectedRevision: base.Plan.Revision, Context: &model.TripDecisionContext{Role: "main", HardStayNights: map[string]map[string]int{"p": {"杭州": 1}}}, Changes: []model.TripTaskChange{{TaskID: base.Plan.Tasks[0].ID, Conditions: model.TripConditions{"nights": rawValue(nights), "check_out": rawValue(checkout)}}}}
		_, err := Preview(&base.Plan, cmd, nil, time.Now())
		if (err != nil) != (nights != 1) {
			t.Fatalf("hard nights=%d result=%v", nights, err)
		}
		if nights != 1 {
			var rule *model.TravelRuleError
			if !errors.As(err, &rule) || rule.Code != "HARD_STAY_REQUIREMENT" {
				t.Fatalf("wrong rejection: %v", err)
			}
		}
	}
}
