package trip

import (
	"agent-platform/backend/internal/model"
	"strings"
	"testing"
)

func TestTravelMissingConditionsAreStatements(t *testing.T) {
	for _, p := range []model.TripPlan{
		{},
		{Tasks: []model.TripTask{{ID: "t", Destination: "上海", DepartureDate: "2026-10-26"}}},
		{Tasks: []model.TripTask{{ID: "t", Origin: "上海", Destination: "杭州", DepartureDate: "2026-10-26", RequiresHotel: true}}},
		{RoundTrip: true, Tasks: []model.TripTask{{ID: "t", Origin: "上海", Destination: "杭州", DepartureDate: "2026-10-26"}}},
	} {
		v, _, err := validatePlan(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, issue := range v.Issues {
			if strings.Contains(issue.Message, "请补充") || strings.Contains(issue.Message, "请明确") {
				t.Errorf("program requested user input: %s", issue.Message)
			}
		}
		if len(p.Tasks) == 1 && p.Tasks[0].Origin == "" {
			for _, issue := range v.Issues {
				if issue.Code == "missing_conditions" && (strings.Contains(issue.Message, "目的地") || strings.Contains(issue.Message, "日期")) {
					t.Errorf("already known destination/date reported missing: %s", issue.Message)
				}
			}
		}
	}
}
